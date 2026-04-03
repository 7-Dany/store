package zmq

// transport.go implements a minimal ZMTP 3.1 SUB connection using only Go.
//
// Scope: TCP transport, NULL security, SUB socket type.
// We deliberately support nothing else — Bitcoin Core's ZMQ publisher uses
// exactly this combination and nothing beyond it.
//
// Protocol reference:
//   https://rfc.zeromq.org/spec:37/ZMTP/   (ZMTP 3.1)
//   https://rfc.zeromq.org/spec:23/ZMTP/   (framing)
//
// Wire layout in summary:
//
//   Handshake
//   ─────────
//   1. Exchange 64-byte greetings (both sides send immediately).
//   2. Exchange READY commands carrying Socket-Type metadata.
//   3. Client sends SUBSCRIBE command with topic bytes.
//
//   Messages (after handshake)
//   ──────────────────────────
//   Bitcoin Core sends 3-frame multipart messages:
//     frame 1  topic bytes  ("hashblock" or "hashtx")   MORE=1
//     frame 2  32-byte hash (little-endian internal order)  MORE=1
//     frame 3  4-byte sequence number (little-endian uint32) MORE=0

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	// ZmtpDialTimeout is the maximum time allowed for the initial TCP dial.
	zmtpDialTimeout = 5 * time.Second

	// ZmtpHandshakeTimeout is the maximum time allowed for the full ZMTP
	// handshake (greeting + READY exchange + SUBSCRIBE command) after the
	// TCP connection is established. Bitcoin Core responds within
	// milliseconds; 5 s is extremely generous.
	zmtpHandshakeTimeout = 5 * time.Second

	// ZmtpReadPoll is the read deadline applied on each call to RecvMessage.
	// When no message arrives within this interval, RecvMessage checks
	// ctx.Err() and retries. Context cancellation is therefore detected
	// within one poll interval — adequate for a financial event stream.
	zmtpReadPoll = 250 * time.Millisecond

	// ZmtpWriteDeadline is the write deadline for PONG responses. Reuses the
	// poll constant so a blocked peer's send buffer cannot stall the read loop.
	zmtpWriteDeadline = zmtpReadPoll

	// ZmtpMaxFrameBody is a sanity cap on frame body length. Frames larger
	// than this are rejected without allocating memory for the body.
	// Bitcoin Core's ZMQ frames are at most ~100 bytes; 1 MiB is generous.
	zmtpMaxFrameBody = 1 << 20 // 1 MiB

	// ZmtpMaxMultipartFrames is the maximum number of frames allowed in a
	// single multipart message. Bitcoin Core always sends exactly 3 frames
	// (topic, hash/rawtx, sequence). A misbehaving peer or hijacked connection
	// that sends unbounded MORE-flagged frames is rejected immediately.
	zmtpMaxMultipartFrames = 3

	// ZmtpReadBuf is the size of the bufio.Reader wrapping the TCP socket.
	// Bitcoin Core sends all three frames of a multipart message together
	// in a single TCP segment. A 4 KiB buffer fills on the first system
	// call and subsequent frame reads come from memory.
	zmtpReadBuf = 4096
)

// Frame flag bits (ZMTP 3.1 §2.2).
const (
	flagMore    byte = 0x01 // more frames follow in this message
	flagLong    byte = 0x02 // size field is 8 bytes big-endian (vs 1 byte)
	flagCommand byte = 0x04 // command frame, not a message frame
)

// ErrPartialMessageTimeout is a sentinel error used to distinguish a timeout
// that occurred during a multipart message read (partial message) from a
// timeout that occurred before any frames were read. This allows RecvMessage
// to trigger a reconnect for corrupt connections instead of retrying forever.
//
// Callers can check errors.Is(err, ErrPartialMessageTimeout) to detect when
// a connection became corrupt mid-message and should be re-established.
var ErrPartialMessageTimeout = errors.New("zmtp: timeout during multipart message")

// errPartialMessageTimeout is the internal alias for the exported sentinel.
var errPartialMessageTimeout = ErrPartialMessageTimeout

// ── zmtpConn ──────────────────────────────────────────────────────────────────

// zmtpConn is a ZMTP 3.1 SUB connection to a single Bitcoin Core ZMQ endpoint.
// It is not safe for concurrent use — each reader goroutine owns its own conn.
type zmtpConn struct {
	tcp             net.Conn
	r               *bufio.Reader
	negotiatedMajor byte // negotiated ZMTP major version (min of ours and peer's)
	negotiatedMinor byte // negotiated ZMTP minor version
}

// dialZMTP establishes a TCP connection to endpoint (format "tcp://host:port"),
// performs the full ZMTP 3.1 NULL handshake as a SUB socket, and sends a
// SUBSCRIBE command for topic. Returns a ready-to-receive connection.
//
// DialZMTP respects ctx for the dial phase. After the TCP connection is open,
// a watchdog goroutine closes the connection if ctx is cancelled, reducing
// the worst-case handshake stall from zmtpHandshakeTimeout (5 s) to the OS
// TCP teardown time. The handshake itself also runs under zmtpHandshakeTimeout.
func dialZMTP(ctx context.Context, endpoint string, topic []byte) (*zmtpConn, error) {
	host := strings.TrimPrefix(endpoint, "tcp://")

	logger.Debug(ctx, "zmq: dialing ZMTP endpoint",
		"endpoint", endpoint, "topic", string(topic))

	d := net.Dialer{Timeout: zmtpDialTimeout}
	tcp, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		logger.Debug(ctx, "zmq: TCP dial failed",
			"endpoint", endpoint, "error", err)
		return nil, fmt.Errorf("dial %s: %w", endpoint, err)
	}

	logger.Debug(ctx, "zmq: TCP connection established",
		"endpoint", endpoint, "local", tcp.LocalAddr(), "remote", tcp.RemoteAddr())

	// TCP_NODELAY: our frames are tiny (< 50 bytes each). Disabling Nagle
	// ensures they are sent immediately rather than coalesced, keeping
	// end-to-end latency minimal. Failure to set this is logged at Warn level
	// because it will degrade latency in production.
	if tc, ok := tcp.(*net.TCPConn); ok {
		if err := tc.SetNoDelay(true); err != nil {
			logger.Warn(ctx, "zmq: failed to enable TCP_NODELAY — will have higher latency",
				"endpoint", endpoint, "error", err)
		}
		// SO_KEEPALIVE: enable TCP keepalives to detect dead connections
		// (network partition, peer OOM-kill without RST). Without keepalives,
		// a silently-dead TCP connection is never detected at the socket level.
		// RecvMessage's 250 ms read deadline would timeout and retry forever
		// without ever triggering the reconnect path.
		if err := tc.SetKeepAlive(true); err != nil {
			logger.Warn(ctx, "zmq: failed to enable SO_KEEPALIVE — dead connections may not be detected",
				"endpoint", endpoint, "error", err)
		} else if err := tc.SetKeepAlivePeriod(30 * time.Second); err != nil {
			logger.Warn(ctx, "zmq: failed to set SO_KEEPALIVE period",
				"endpoint", endpoint, "error", err)
		}
	}

	// Watchdog: close the TCP connection immediately when ctx is cancelled.
	// This reduces the worst-case handshake stall from zmtpHandshakeTimeout
	// (5 s) to the OS TCP teardown time, which matters for fast shutdown and
	// tests that cancel the context quickly.
	//
	// The watchdog goroutine is NOT tracked by any WaitGroup and exits
	// synchronously (within nanoseconds) via close(connClosed). It is safe
	// to create this untracked goroutine because:
	// 1. It only performs a channel select and either closes the tcp conn or
	//    receives from connClosed.
	// 2. The deferred close(connClosed) signals the goroutine to exit. The
	//    goroutine is not joined — it performs a single tcp.Close() syscall
	//    and exits within nanoseconds. net.Conn.Close() is concurrency-safe,
	//    so a race between the caller's close and the watchdog's close is
	//    benign, but tests using goleak must allow for this brief goroutine.
	// 3. It makes only one system call (tcp.Close()) in the ctx.Done path.
	//
	// Tests using goleak should suppress this pattern as a known safe pattern.
	connClosed := make(chan struct{})
	defer close(connClosed)
	go func() {
		select {
		case <-ctx.Done():
			_ = tcp.Close() //nolint:errcheck // best-effort close on context cancellation
		case <-connClosed:
			// dialZMTP returned normally; tcp is owned by the caller.
		}
	}()

	c := &zmtpConn{tcp: tcp, r: bufio.NewReaderSize(tcp, zmtpReadBuf)}

	if err := c.handshake(ctx, topic); err != nil {
		logger.Debug(ctx, "zmq: ZMTP handshake failed",
			"endpoint", endpoint, "error", err)
		if closeErr := tcp.Close(); closeErr != nil {
			logger.Debug(ctx, "zmq: close after handshake failure failed", "endpoint", endpoint, "error", closeErr)
		}
		return nil, fmt.Errorf("handshake with %s: %w", endpoint, err)
	}

	logger.Debug(ctx, "zmq: ZMTP handshake complete — connection ready",
		"endpoint", endpoint, "topic", string(topic))
	return c, nil
}

// RecvMessage reads the next complete multipart message from Bitcoin Core.
// Returns [][]byte where each element is one frame in wire order:
//
//	[0] topic bytes  ("hashblock" or "hashtx")
//	[1] 32-byte hash (ZMQ little-endian byte order)
//	[2] 4-byte sequence number (little-endian uint32)
//
// RecvMessage blocks until a complete message arrives. When no message has
// arrived within zmtpReadPoll, it checks ctx.Err() and either returns
// ctx.Err() (if cancelled) or resets the deadline and tries again.
//
// Intervening PING command frames are answered with PONG transparently.
// ERROR command frames close the connection per ZMTP 3.1 §2.4.
// All other command frames are logged and skipped.
func (c *zmtpConn) RecvMessage(ctx context.Context) ([][]byte, error) {
	for {
		if err := c.tcp.SetReadDeadline(time.Now().Add(zmtpReadPoll)); err != nil {
			return nil, fmt.Errorf("set read deadline: %w", err)
		}

		msg, err := c.readMessage(ctx)
		if err == nil {
			logger.Debug(ctx, "zmq: message received",
				"frames", len(msg))
			return msg, nil
		}

		// Distinguish a read-deadline timeout from a real error.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			// Check if the timeout error came from a partial-message read.
			// The readMessage function wraps timeout errors from mid-message
			// with errPartialMessageTimeout so we can distinguish them here.
			if errors.Is(err, errPartialMessageTimeout) {
				// Timeout after frame 1 was read — connection is corrupt.
				logger.Warn(ctx, "zmq: timeout during multipart message — closing connection",
					"error", err)
				return nil, err
			}

			// Timeout occurred before any frame was read — safe to retry.
			if ctx.Err() != nil {
				logger.Debug(ctx, "zmq: RecvMessage: context cancelled during poll")
				return nil, ctx.Err()
			}
			// Deadline elapsed but context is still live — poll again.
			continue
		}

		logger.Debug(ctx, "zmq: RecvMessage: read error", "error", err)
		return nil, err
	}
}

// Close closes the underlying TCP connection.
func (c *zmtpConn) Close() error {
	return c.tcp.Close()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// writeAll writes b to w, returning an error if a partial write occurs.
// This ensures that ZMTP frame boundaries are never corrupted by a short write.
func writeAll(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

// ── Handshake ─────────────────────────────────────────────────────────────────

// handshake performs the complete ZMTP 3.1 NULL handshake:
//  1. Exchange 64-byte greetings.
//  2. Send our READY (Socket-Type: SUB), read server's READY.
//  3. Send SUBSCRIBE command for topic.
//
// The handshake runs under zmtpHandshakeTimeout from start to finish.
// A best-effort ERROR command is sent to the peer before closing on any
// post-greeting failure (M1), so the peer can distinguish protocol errors
// from simple connection drops.
func (c *zmtpConn) handshake(ctx context.Context, topic []byte) error {
	if err := c.tcp.SetDeadline(time.Now().Add(zmtpHandshakeTimeout)); err != nil {
		return fmt.Errorf("set handshake deadline: %w", err)
	}
	defer func() {
		if err := c.tcp.SetDeadline(time.Time{}); err != nil {
			// H6: failure to clear deadline leaves a stale deadline on the
			// live connection, causing phantom timeouts — log at Warn.
			logger.Warn(ctx, "zmq: clear handshake deadline failed", "error", err)
		}
	}()

	// ── Step 1: exchange greetings ────────────────────────────────────────
	// ZMTP requires both sides to send simultaneously; in practice, with TCP
	// buffering and 64-byte greetings, sending then reading is safe and avoids
	// the complexity of concurrent goroutines during setup.
	logger.Debug(ctx, "zmq: handshake step 1 -- sending greeting")
	if err := writeAll(c.tcp, buildGreeting()); err != nil {
		return fmt.Errorf("send greeting: %w", err)
	}
	if err := c.readAndValidateGreeting(ctx); err != nil {
		// M1: do NOT send ERROR before greeting exchange completes —
		// the peer hasn't established a connection-level agreement yet.
		return fmt.Errorf("read greeting: %w", err)
	}
	logger.Debug(ctx, "zmq: handshake step 1 -- greeting exchanged OK")

	// ── Step 2: exchange READY commands ──────────────────────────────────
	// Post-greeting: send best-effort ERROR before any error return (M1).
	logger.Debug(ctx, "zmq: handshake step 2 -- sending READY SUB")
	if err := writeAll(c.tcp, buildReady("SUB")); err != nil {
		sendErrorIgnore(c, err.Error())
		return fmt.Errorf("send READY: %w", err)
	}
	if err := c.readReadyCommand(ctx); err != nil {
		sendErrorIgnore(c, err.Error())
		return fmt.Errorf("read READY: %w", err)
	}
	logger.Debug(ctx, "zmq: handshake step 2 -- READY exchanged OK")

	// ── Step 3: subscribe ─────────────────────────────────────────────────
	logger.Debug(ctx, "zmq: handshake step 3 -- sending SUBSCRIBE", "topic", string(topic))
	if err := writeAll(c.tcp, buildSubscribe(topic)); err != nil {
		sendErrorIgnore(c, err.Error())
		return fmt.Errorf("send SUBSCRIBE: %w", err)
	}
	logger.Debug(ctx, "zmq: handshake step 3 -- SUBSCRIBE sent OK")

	return nil
}

// sendErrorIgnore sends a ZMTP ERROR command to the peer on a best-effort
// basis. The write error is silently discarded — ERROR is advisory and the
// connection will be closed regardless.
//
// The reason string is capped at 255 bytes to prevent deeply-wrapped error
// chains from producing unexpectedly large outgoing frames.
func sendErrorIgnore(c *zmtpConn, reason string) {
	// Cap the reason string to avoid large outgoing frames.
	const maxReasonLen = 255
	if len(reason) > maxReasonLen {
		reason = reason[:maxReasonLen]
	}
	_ = writeAll(c.tcp, buildCommand("ERROR", []byte(reason))) //nolint:errcheck // best-effort ERROR send, connection closes regardless
}

// ── Message and frame reading ─────────────────────────────────────────────────

// readMessage reads frames until it has assembled a complete multipart message
// (the last frame has MORE=0). Intervening command frames are handled in-place:
// PING is answered with PONG; ERROR closes the connection; all others are skipped.
//
// A misbehaving peer that sends more than zmtpMaxMultipartFrames with MORE=1
// is rejected immediately. Bitcoin Core always sends exactly 3 frames;
// this guards against unbounded frame accumulation.
//
// PROTOCOL SAFETY: If a timeout error occurs after at least one frame has been
// read (partial multipart message), the error is wrapped with a prefix so that
// RecvMessage can distinguish this from a "no frames read yet" timeout and
// trigger an immediate reconnect instead of retrying.
func (c *zmtpConn) readMessage(ctx context.Context) ([][]byte, error) {
	frames := make([][]byte, 0, zmtpMaxMultipartFrames)
	for {
		flags, body, err := c.readFrame(ctx)
		if err != nil {
			// If we've already read some frames and hit a timeout, wrap it with
			// the sentinel error so RecvMessage knows this is a corrupt connection.
			if len(frames) > 0 {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					return nil, fmt.Errorf("%w (read %d/%d frames): %w", errPartialMessageTimeout,
						len(frames), zmtpMaxMultipartFrames, err)
				}
			}
			return nil, err
		}

		if flags&flagCommand != 0 {
			if err := c.handleIncomingCommand(ctx, body); err != nil {
				return nil, fmt.Errorf("handle command: %w", err)
			}
			continue
		}

		// C1: Guard against multipart message overflow BEFORE allocating/appending
		// the next frame body. A malicious peer sending MORE-flagged frames beyond
		// the limit is rejected without allocating memory for the (N+1)th frame.
		if len(frames) >= zmtpMaxMultipartFrames {
			return nil, fmt.Errorf("zmtp: multipart message exceeds %d frames", zmtpMaxMultipartFrames)
		}
		frames = append(frames, body)
		if flags&flagMore == 0 {
			return frames, nil
		}
	}
}

// readFrame reads exactly one ZMTP frame from the connection.
// Returns the flags byte and the body bytes.
//
// Per ZMTP 3.1 §2.2: reserved flag bits (all bits except 0x01 MORE, 0x02 LONG, 0x04 COMMAND)
// MUST be ignored on receive. This implementation silently ignores reserved bits to ensure
// compatibility with future ZMTP extensions that may set them.
func (c *zmtpConn) readFrame(ctx context.Context) (flags byte, body []byte, err error) {
	// Read the flags byte via io.ReadFull to handle any partial-read condition.
	// Per ZMTP spec, reserved bits in flags are ignored (not error).
	var flagsBuf [1]byte
	if _, err := io.ReadFull(c.r, flagsBuf[:]); err != nil {
		return 0, nil, fmt.Errorf("read flags byte: %w", err)
	}
	flags = flagsBuf[0]

	var bodyLen uint64
	if flags&flagLong != 0 {
		// Long frame: 8-byte big-endian size field.
		var lenBuf [8]byte
		if _, err := io.ReadFull(c.r, lenBuf[:]); err != nil {
			return 0, nil, fmt.Errorf("read long frame size: %w", err)
		}
		bodyLen = binary.BigEndian.Uint64(lenBuf[:])
	} else {
		// Short frame: 1-byte size field.
		var sizeBuf [1]byte
		if _, err := io.ReadFull(c.r, sizeBuf[:]); err != nil {
			return 0, nil, fmt.Errorf("read short frame size: %w", err)
		}
		bodyLen = uint64(sizeBuf[0])
	}

	// C1: cap check fires BEFORE any allocation — a peer claiming 2^63-1 bytes
	// is rejected here without ever reaching make().
	if bodyLen > zmtpMaxFrameBody {
		return 0, nil, fmt.Errorf("frame body length %d exceeds %d-byte cap — possible protocol error",
			bodyLen, zmtpMaxFrameBody)
	}

	body = make([]byte, bodyLen)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return 0, nil, fmt.Errorf("read frame body (%d bytes): %w", bodyLen, err)
	}

	logger.Debug(ctx, "zmq: frame read",
		"flags", fmt.Sprintf("0x%02x", flags),
		"is_command", flags&flagCommand != 0,
		"has_more", flags&flagMore != 0,
		"body_len", bodyLen)
	return flags, body, nil
}

// handleIncomingCommand processes a command frame received after the handshake.
//
// PING is answered with PONG (ZMTP 3.1 §4 heartbeat). A short write deadline
// (zmtpWriteDeadline) prevents a blocked peer's receive buffer from stalling
// the read loop indefinitely.
//
// ERROR causes the connection to be closed per ZMTP 3.1 §2.4: "A peer that
// receives an ERROR command MUST close the connection." The returned error
// propagates through readMessage → RecvMessage → receiveLoop, where the
// reconnect logic fires.
//
// Per ZMTP 3.1 §2.3: "A peer that receives a command that it did not expect
// MUST close the connection." Unexpected command frames (READY, SUBSCRIBE, etc.)
// and unsolicited PONG (without a prior PING) close the connection with ERROR.
func (c *zmtpConn) handleIncomingCommand(ctx context.Context, body []byte) error {
	name, data, ok := parseCommandBody(body)
	if !ok {
		logger.Debug(ctx, "zmq: received malformed command frame -- closing connection")
		// Malformed command is unexpected per ZMTP spec — close the connection.
		// We don't send ERROR since the body was malformed (can't parse it reliably).
		return fmt.Errorf("zmtp: malformed command frame")
	}
	logger.Debug(ctx, "zmq: incoming command frame", "command", name, "data_len", len(data))

	if name == "ERROR" {
		// ZMTP 3.1 §2.4: connection must be closed on received ERROR.
		// "data" is the error reason string from the peer.
		logger.Warn(ctx, "zmq: received ERROR from peer -- closing connection",
			"reason", string(data))
		return fmt.Errorf("zmtp: peer sent ERROR command: %s", string(data))
	}

	if name == "PING" {
		// ZMTP 3.1 §4: PONG must echo the ping context only — NOT the
		// 2-byte TTL field that precedes it in the PING body.
		//
		// PING body structure: TTL (2 bytes) + ping-context (0–16 bytes)
		// PONG body structure: pong-context = echo of ping-context only
		var pongContext []byte
		if len(data) >= 2 {
			pongContext = data[2:] // skip TTL, echo context only
		}
		ttl := uint16(0)
		if len(data) >= 2 {
			ttl = binary.BigEndian.Uint16(data[0:2])
		}
		logger.Debug(ctx, "zmq: sending PONG", "context_len", len(pongContext), "ping_ttl_ms", ttl)

		// Apply a short write deadline to prevent blocking forever if the
		// peer's receive buffer is full (H3).
		if err := c.tcp.SetWriteDeadline(time.Now().Add(zmtpWriteDeadline)); err != nil {
			return fmt.Errorf("set pong write deadline: %w", err)
		}
		writeErr := writeAll(c.tcp, buildCommand("PONG", pongContext))
		// Always clear the write deadline — failure leaves a stale deadline.
		if clearErr := c.tcp.SetWriteDeadline(time.Time{}); clearErr != nil {
			logger.Warn(ctx, "zmq: clear PONG write deadline failed", "error", clearErr)
		}
		if writeErr != nil {
			logger.Warn(ctx, "zmq: write PONG failed", "error", writeErr)
		}
		return writeErr
	}

	if name == "PONG" {
		// Per ZMTP 3.1 §4: "A peer MUST NOT send a PONG command unless it has
		// received a PING command." Unsolicited PONG is a protocol violation.
		logger.Warn(ctx, "zmq: received unsolicited PONG -- closing connection")
		sendErrorIgnore(c, "unsolicited PONG")
		return fmt.Errorf("zmtp: unsolicited PONG command")
	}

	// Per ZMTP 3.1 §2.3: unexpected commands (READY, SUBSCRIBE, etc.) close the connection.
	logger.Warn(ctx, "zmq: received unexpected command during TRAFFIC -- closing connection",
		"command", name)
	sendErrorIgnore(c, fmt.Sprintf("unexpected command: %s", name))
	return fmt.Errorf("zmtp: unexpected command during TRAFFIC: %s", name)
}

// readAndValidateGreeting reads the server's 64-byte greeting and validates
// the ZMTP signature, major version, and security mechanism.
func (c *zmtpConn) readAndValidateGreeting(ctx context.Context) error {
	logger.Debug(ctx, "zmq: readAndValidateGreeting: reading 64-byte greeting")
	var g [64]byte
	if _, err := io.ReadFull(c.r, g[:]); err != nil {
		logger.Debug(ctx, "zmq: readAndValidateGreeting: short read", "error", err)
		return fmt.Errorf("read: %w", err)
	}

	// Validate ZMTP signature per RFC 3.1 §3.3:
	//   - Byte [0] = 0xFF (signature start)
	//   - Byte [9] = 0x7F (signature end)
	//   - Bytes [1..8] are reserved padding — MUST be ignored on receive.
	//   - Byte [32] = as-server flag — ignored on receive (Bitcoin Core's
	//     ZMQ PUB socket sends 0x00 regardless of role).
	if g[0] != 0xFF || g[9] != 0x7F {
		logger.Warn(ctx, "zmq: readAndValidateGreeting: invalid ZMTP signature -- possible non-ZMTP server or corrupt stream",
			"byte0", fmt.Sprintf("%02x", g[0]), "byte9", fmt.Sprintf("%02x", g[9]),
			"expected_byte0", "ff", "expected_byte9", "7f")
		return fmt.Errorf("invalid ZMTP signature (byte[0]=%02x byte[9]=%02x; expected 0xff 0x7f)",
			g[0], g[9])
	}

	// Require ZMTP major version 3+.
	// Negotiate the version: use the minimum of our version (3.1) and the peer's.
	// Per ZMTP spec, if peer sends major=4, both parties downgrade to major=3.
	if g[10] < 3 {
		logger.Warn(ctx, "zmq: readAndValidateGreeting: server ZMTP major version too old",
			"server_major", g[10], "required_minimum", 3)
		return fmt.Errorf("server ZMTP major version %d is too old (need ≥ 3)", g[10])
	}

	// Negotiate the version: we support 3.1, peer advertises their major/minor.
	// Use min(3, peer_major) to ensure compatibility with future ZMTP versions.
	c.negotiatedMajor = min(byte(3), g[10])
	c.negotiatedMinor = min(byte(1), g[11])

	// Require NULL security mechanism — the only mechanism Bitcoin Core uses.
	mech := strings.TrimRight(string(g[12:32]), "\x00")
	if mech != "NULL" {
		logger.Warn(ctx, "zmq: readAndValidateGreeting: unsupported security mechanism -- Bitcoin Core always uses NULL; check zmq endpoint config",
			"server_mechanism", mech, "required_mechanism", "NULL")
		return fmt.Errorf("server advertises %q security; only NULL is supported "+
			"(Bitcoin Core always uses NULL — check zmq endpoint config)", mech)
	}

	logger.Debug(ctx, "zmq: readAndValidateGreeting: greeting valid",
		"peer_major", g[10], "peer_minor", g[11],
		"negotiated_major", c.negotiatedMajor, "negotiated_minor", c.negotiatedMinor,
		"mechanism", mech)
	return nil
}

// readReadyCommand reads one command frame and verifies that it is a READY
// command with Socket-Type=PUB metadata. A misconfigured endpoint advertising
// a different socket type (PUSH, DEALER, etc.) is rejected immediately with a
// diagnostic error rather than silently hanging until reconnect timeout.
func (c *zmtpConn) readReadyCommand(ctx context.Context) error {
	const want = "READY"
	logger.Debug(ctx, "zmq: readReadyCommand: awaiting command frame", "want", want)
	flags, body, err := c.readFrame(ctx)
	if err != nil {
		logger.Debug(ctx, "zmq: readReadyCommand: frame read failed", "want", want, "error", err)
		return err
	}
	if flags&flagCommand == 0 {
		logger.Warn(ctx, "zmq: readReadyCommand: received message frame instead of command frame -- server may not be a ZeroMQ PUB socket",
			"want", want, "flags", fmt.Sprintf("0x%02x", flags))
		return fmt.Errorf("expected ZMTP command frame, got message frame (flags=0x%02x) — "+
			"server may not be a ZeroMQ PUB socket", flags)
	}
	name, data, ok := parseCommandBody(body)
	if !ok {
		logger.Warn(ctx, "zmq: readReadyCommand: malformed command body",
			"want", want, "body_len", len(body))
		return fmt.Errorf("malformed command frame body (length %d)", len(body))
	}
	if name != want {
		logger.Warn(ctx, "zmq: readReadyCommand: unexpected command name -- possible protocol mismatch",
			"want", want, "got", name)
		return fmt.Errorf("expected %q command, got %q", want, name)
	}

	// H7: validate Socket-Type metadata so a misconfigured PUSH/DEALER endpoint
	// is caught here rather than hanging silently until reconnect timeout.
	meta := parseReadyMetadata(data)
	if st, ok := meta["Socket-Type"]; !ok {
		logger.Warn(ctx, "zmq: readReadyCommand: Socket-Type metadata missing -- check ZMQ endpoint config",
			"want_socket_type", "PUB")
		return fmt.Errorf("zmtp: READY command missing Socket-Type metadata " +
			"(check that the ZMQ endpoint is configured for PUB)")
	} else if st != "PUB" {
		logger.Warn(ctx, "zmq: readReadyCommand: unexpected Socket-Type -- check ZMQ endpoint config",
			"want_socket_type", "PUB", "got_socket_type", st)
		return fmt.Errorf("zmtp: expected Socket-Type PUB in READY command, got %q "+
			"(check that the ZMQ endpoint is configured for PUB, not %s)", st, st)
	}

	logger.Debug(ctx, "zmq: readReadyCommand: OK", "name", name)
	return nil
}

// parseReadyMetadata parses the ZMTP READY command metadata key-value pairs
// from the data portion of the command body.
//
// Wire format (per ZMTP 3.1 §2.5):
//
//	key-length   (1 byte)        — length of the key string
//	key          (key-length bytes)
//	value-length (4 bytes big-endian) — length of the value string
//	value        (value-length bytes)
//
// Parsing is lenient: truncated or malformed pairs are silently skipped rather
// than returning an error, since missing metadata is caught by the caller's
// Socket-Type assertion.
func parseReadyMetadata(data []byte) map[string]string {
	m := make(map[string]string)
	for len(data) > 0 {
		kLen := int(data[0])
		data = data[1:]
		if len(data) < kLen {
			break
		}
		key := string(data[:kLen])
		data = data[kLen:]
		if len(data) < 4 {
			break
		}
		// vLen is kept as uint32 to avoid overflow on 32-bit platforms.
		// Comparison: uint32(len(data)) < vLen is safe and accurate.
		vLen := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		if uint32(len(data)) < vLen { //nolint:gosec // safe: len(data) capped by caller, vLen from wire
			break
		}
		m[key] = string(data[:vLen])
		data = data[vLen:]
	}
	return m
}

// ── Frame and command builders ────────────────────────────────────────────────

// buildGreeting constructs our 64-byte ZMTP 3.1 NULL greeting.
//
// Layout:
//
//	[0]     0xFF  — signature prefix
//	[1..7]  0x00 × 7 | [8] 0x01 — together encode big-endian value 1 for ZMTP 2.x compat
//	[9]     0x7F  — signature suffix
//	[10]    0x03  — ZMTP major version
//	[11]    0x01  — ZMTP minor version (3.1)
//	[12..31] "NULL" + 16 zeros — security mechanism (20 bytes)
//	[32]    0x00  — as-server=0 (we are the connecting client)
//	[33..63] 0x00 — filler (31 bytes)
func buildGreeting() []byte {
	var g [64]byte
	g[0] = 0xFF
	g[8] = 0x01 // padding: big-endian 1 in bytes [1..8] (0x0000000000000001 for ZMTP 2.x compat)
	g[9] = 0x7F
	g[10] = 3 // ZMTP major version
	g[11] = 1 // ZMTP minor version
	copy(g[12:], "NULL")
	// g[32..63] remain zero (as-server=0, filler=0)
	return g[:]
}

// buildCommand constructs a ZMTP command frame.
// Uses a LONG size frame (8-byte size) only when the body exceeds 255 bytes.
func buildCommand(name string, data []byte) []byte {
	nameLen := len(name)
	if nameLen > 255 {
		panic("zmq: command name too long")
	}
	bodyLen := 1 + nameLen + len(data) // 1 byte for name-length prefix

	var hdr []byte
	if bodyLen <= 255 {
		hdr = []byte{flagCommand, byte(bodyLen)}
	} else {
		var sz [8]byte
		binary.BigEndian.PutUint64(sz[:], uint64(bodyLen))
		hdr = append([]byte{flagCommand | flagLong}, sz[:]...)
	}

	out := make([]byte, 0, len(hdr)+1+nameLen+len(data))
	out = append(out, hdr...)
	out = append(out, byte(nameLen))
	out = append(out, name...)
	out = append(out, data...)
	return out
}

// buildReady builds a READY command declaring our socket type.
// Metadata format: key-length(1) + key + value-length(4 big-endian) + value.
func buildReady(socketType string) []byte {
	const key = "Socket-Type"
	meta := make([]byte, 0, 1+len(key)+4+len(socketType))
	meta = append(meta, byte(len(key)))
	meta = append(meta, key...)
	var vlen [4]byte
	//nolint:gosec // socketType is a short protocol constant such as "SUB" or "PUB".
	binary.BigEndian.PutUint32(vlen[:], uint32(len(socketType)))
	meta = append(meta, vlen[:]...)
	meta = append(meta, socketType...)
	return buildCommand("READY", meta)
}

// buildSubscribe builds a SUBSCRIBE command for the given topic.
// In ZMTP, a SUB socket subscribes by sending a SUBSCRIBE command whose
// data payload is the topic prefix to match (empty = subscribe to everything).
func buildSubscribe(topic []byte) []byte {
	return buildCommand("SUBSCRIBE", topic)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// parseCommandBody extracts the command name and remaining data from a command
// frame body. Returns ok=false if the body is too short to be valid.
func parseCommandBody(body []byte) (name string, data []byte, ok bool) {
	if len(body) < 1 {
		return "", nil, false
	}
	nameLen := int(body[0])
	if len(body) < 1+nameLen {
		return "", nil, false
	}
	return string(body[1 : 1+nameLen]), body[1+nameLen:], true
}
