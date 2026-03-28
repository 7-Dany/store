package zmq

// conn.go implements a minimal ZMTP 3.1 SUB connection using only Go.
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

	// ZmtpMaxFrameBody is a sanity cap on frame body length. Frames larger
	// than this are rejected without allocating memory for the body.
	// Bitcoin Core's ZMQ frames are at most ~100 bytes; 1 MiB is generous.
	zmtpMaxFrameBody = 1 << 20 // 1 MiB

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

// ── zmtpConn ──────────────────────────────────────────────────────────────────

// zmtpConn is a ZMTP 3.1 SUB connection to a single Bitcoin Core ZMQ endpoint.
// It is not safe for concurrent use — each reader goroutine owns its own conn.
type zmtpConn struct {
	tcp net.Conn
	r   *bufio.Reader
}

// dialZMTP establishes a TCP connection to endpoint (format "tcp://host:port"),
// performs the full ZMTP 3.1 NULL handshake as a SUB socket, and sends a
// SUBSCRIBE command for topic. Returns a ready-to-receive connection.
//
// DialZMTP respects ctx for the dial phase. Once the TCP connection is open,
// the handshake runs under its own internal deadline (zmtpHandshakeTimeout).
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
	// end-to-end latency minimal.
	if tc, ok := tcp.(*net.TCPConn); ok {
		if err := tc.SetNoDelay(true); err != nil {
			logger.Debug(ctx, "zmq: failed to enable TCP_NODELAY", "endpoint", endpoint, "error", err)
		}
	}

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
// All other command frames (e.g., ERROR) are logged and skipped.
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

// ── Handshake ─────────────────────────────────────────────────────────────────

// handshake performs the complete ZMTP 3.1 NULL handshake:
//  1. Exchange 64-byte greetings.
//  2. Send our READY (Socket-Type: SUB), read server's READY.
//  3. Send SUBSCRIBE command for topic.
//
// The handshake runs under zmtpHandshakeTimeout from start to finish.
func (c *zmtpConn) handshake(ctx context.Context, topic []byte) error {
	if err := c.tcp.SetDeadline(time.Now().Add(zmtpHandshakeTimeout)); err != nil {
		return fmt.Errorf("set handshake deadline: %w", err)
	}
	defer func() {
		if err := c.tcp.SetDeadline(time.Time{}); err != nil {
			logger.Debug(ctx, "zmq: clear handshake deadline failed", "error", err)
		}
	}()

	// ── Step 1: exchange greetings ────────────────────────────────────────
	// ZMTP requires both sides to send simultaneously; in practice, with TCP
	// buffering and 64-byte greetings, sending then reading is safe and avoids
	// the complexity of concurrent goroutines during setup.
	logger.Debug(ctx, "zmq: handshake step 1 -- sending greeting")
	if _, err := c.tcp.Write(buildGreeting()); err != nil {
		return fmt.Errorf("send greeting: %w", err)
	}
	if err := c.readAndValidateGreeting(ctx); err != nil {
		return fmt.Errorf("read greeting: %w", err)
	}
	logger.Debug(ctx, "zmq: handshake step 1 -- greeting exchanged OK")

	// ── Step 2: exchange READY commands ──────────────────────────────────
	logger.Debug(ctx, "zmq: handshake step 2 -- sending READY SUB")
	if _, err := c.tcp.Write(buildReady("SUB")); err != nil {
		return fmt.Errorf("send READY: %w", err)
	}
	if err := c.readReadyCommand(ctx); err != nil {
		return fmt.Errorf("read READY: %w", err)
	}
	logger.Debug(ctx, "zmq: handshake step 2 -- READY exchanged OK")

	// ── Step 3: subscribe ─────────────────────────────────────────────────
	logger.Debug(ctx, "zmq: handshake step 3 -- sending SUBSCRIBE", "topic", string(topic))
	if _, err := c.tcp.Write(buildSubscribe(topic)); err != nil {
		return fmt.Errorf("send SUBSCRIBE: %w", err)
	}
	logger.Debug(ctx, "zmq: handshake step 3 -- SUBSCRIBE sent OK")

	return nil
}

// ── Message and frame reading ─────────────────────────────────────────────────

// readMessage reads frames until it has assembled a complete multipart message
// (the last frame has MORE=0). Intervening command frames are handled in-place:
// PING is answered with PONG; all others are ignored.
func (c *zmtpConn) readMessage(ctx context.Context) ([][]byte, error) {
	var frames [][]byte
	for {
		flags, body, err := c.readFrame(ctx)
		if err != nil {
			return nil, err
		}

		if flags&flagCommand != 0 {
			if err := c.handleIncomingCommand(ctx, body); err != nil {
				return nil, fmt.Errorf("handle command: %w", err)
			}
			continue
		}

		frames = append(frames, body)
		if flags&flagMore == 0 {
			return frames, nil
		}
	}
}

// readFrame reads exactly one ZMTP frame from the connection.
// Returns the flags byte and the body bytes.
func (c *zmtpConn) readFrame(ctx context.Context) (flags byte, body []byte, err error) {
	flags, err = c.r.ReadByte()
	if err != nil {
		return 0, nil, fmt.Errorf("read flags byte: %w", err)
	}

	var bodyLen uint64
	if flags&flagLong != 0 {
		// 8-byte big-endian size.
		var buf [8]byte
		if _, err := io.ReadFull(c.r, buf[:]); err != nil {
			return 0, nil, fmt.Errorf("read long frame size: %w", err)
		}
		bodyLen = binary.BigEndian.Uint64(buf[:])
	} else {
		// 1-byte size.
		b, err := c.r.ReadByte()
		if err != nil {
			return 0, nil, fmt.Errorf("read short frame size: %w", err)
		}
		bodyLen = uint64(b)
	}

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
// PING is answered with PONG (ZMTP 3.1 heartbeat, §4). All other commands
// are silently ignored — Bitcoin Core does not currently send any others during
// normal operation, but future ZeroMQ versions may add new ones.
func (c *zmtpConn) handleIncomingCommand(ctx context.Context, body []byte) error {
	name, data, ok := parseCommandBody(body)
	if !ok {
		logger.Debug(ctx, "zmq: received malformed command frame -- skipping")
		return nil // malformed but harmless — skip
	}
	logger.Debug(ctx, "zmq: incoming command frame", "command", name, "data_len", len(data))
	if name == "PING" {
		// ZMTP 3.1 §4: PONG must echo the ping context only — NOT the
		// 2-byte TTL field that precedes it in the PING body.
		//
		// PING body structure: TTL (2 bytes) + ping-context (0–16 bytes)
		// PONG body structure: pong-context = echo of ping-context only
		//
		// Sending TTL+context as PONG data is a spec violation; correct
		// ZeroMQ peers would interpret the first 2 context bytes as part
		// of the echoed context, not as a TTL field.
		var pongContext []byte
		if len(data) >= 2 {
			pongContext = data[2:] // skip TTL, echo context only
		}
		logger.Debug(ctx, "zmq: sending PONG", "context_len", len(pongContext))
		_, err := c.tcp.Write(buildCommand("PONG", pongContext))
		return err
	}
	// All other commands (SUBSCRIBE, ERROR, etc.) are ignored on the receive path.
	return nil
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

	// Validate ZMTP signature: 0xFF ... 0x7F
	if g[0] != 0xFF || g[9] != 0x7F {
		logger.Warn(ctx, "zmq: readAndValidateGreeting: invalid ZMTP signature -- possible non-ZMTP server or corrupt stream",
			"byte0", fmt.Sprintf("%02x", g[0]), "byte9", fmt.Sprintf("%02x", g[9]),
			"expected_byte0", "ff", "expected_byte9", "7f")
		return fmt.Errorf("invalid ZMTP signature (byte[0]=%02x byte[9]=%02x; expected 0xff...0x7f)",
			g[0], g[9])
	}

	// Require ZMTP major version 3+.
	if g[10] < 3 {
		logger.Warn(ctx, "zmq: readAndValidateGreeting: server ZMTP major version too old",
			"server_major", g[10], "required_minimum", 3)
		return fmt.Errorf("server ZMTP major version %d is too old (need ≥ 3)", g[10])
	}

	// Require NULL security mechanism — the only mechanism Bitcoin Core uses.
	mech := strings.TrimRight(string(g[12:32]), "\x00")
	if mech != "NULL" {
		logger.Warn(ctx, "zmq: readAndValidateGreeting: unsupported security mechanism -- Bitcoin Core always uses NULL; check zmq endpoint config",
			"server_mechanism", mech, "required_mechanism", "NULL")
		return fmt.Errorf("server advertises %q security; only NULL is supported "+
			"(Bitcoin Core always uses NULL — check zmq endpoint config)", mech)
	}

	logger.Debug(ctx, "zmq: readAndValidateGreeting: greeting valid",
		"major", g[10], "minor", g[11], "mechanism", mech)
	return nil
}

// readReadyCommand reads one command frame and verifies that it is READY.
// Used to consume the server's READY command during the handshake.
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
	name, _, ok := parseCommandBody(body)
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
	logger.Debug(ctx, "zmq: readReadyCommand: OK", "name", name)
	return nil
}

// ── Frame and command builders ────────────────────────────────────────────────

// buildGreeting constructs our 64-byte ZMTP 3.1 NULL greeting.
//
// Layout:
//
//	[0]     0xFF  — signature prefix
//	[1..7]  0x00  — padding (big-endian body length of old-format frames)
//	[8]     0x01  — padding last byte (encodes length=1 for compat)
//	[9]     0x7F  — signature suffix
//	[10]    0x03  — ZMTP major version
//	[11]    0x01  — ZMTP minor version (3.1)
//	[12..31] "NULL" + 16 zeros — security mechanism (20 bytes)
//	[32]    0x00  — as-server=0 (we are the connecting client)
//	[33..63] 0x00 — filler (31 bytes)
func buildGreeting() []byte {
	var g [64]byte
	g[0] = 0xFF
	g[8] = 0x01 // padding: big-endian 1 in bytes [1..8]
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
