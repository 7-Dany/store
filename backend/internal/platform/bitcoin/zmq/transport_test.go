package zmq

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ═════════════════════════════════════════════════════════════════════════════
// Helpers
// ═════════════════════════════════════════════════════════════════════════════

// connFromBytes wraps data in a zmtpConn for read-path tests.
// The tcp field is nil; callers must not invoke methods that write to tcp
// (i.e. do not call RecvMessage, handshake, or handleIncomingCommand with PING).
func connFromBytes(data []byte) *zmtpConn {
	return &zmtpConn{r: bufio.NewReaderSize(bytes.NewReader(data), zmtpReadBuf)}
}

// encodeShortFrame builds the raw wire bytes for a ZMTP short frame:
// flags(1) + size(1 byte) + body. Callers must ensure len(body) <= 255.
func encodeShortFrame(flags byte, body []byte) []byte {
	out := []byte{flags, byte(len(body))}
	return append(out, body...)
}

// encodeLongFrame builds the raw wire bytes for a ZMTP long frame:
// flags|flagLong(1) + size(8 bytes big-endian) + body.
func encodeLongFrame(flags byte, body []byte) []byte {
	var sz [8]byte
	binary.BigEndian.PutUint64(sz[:], uint64(len(body)))
	out := append([]byte{flags | flagLong}, sz[:]...)
	return append(out, body...)
}

// buildValidGreeting64 constructs a 64-byte greeting with the given major
// version and security mechanism string (left-padded with zeros to 20 bytes).
func buildValidGreeting64(major, minor byte, mech string) [64]byte {
	var g [64]byte
	g[0] = 0xFF
	g[8] = 0x01
	g[9] = 0x7F
	g[10] = major
	g[11] = minor
	g[32] = 0x01 // as-server flag: we are connecting to a server
	copy(g[12:32], mech)
	return g
}

// ═════════════════════════════════════════════════════════════════════════════
// buildGreeting
// ═════════════════════════════════════════════════════════════════════════════

func TestBuildGreeting_Length(t *testing.T) {
	t.Parallel()
	require.Len(t, buildGreeting(), 64)
}

func TestBuildGreeting_Signature(t *testing.T) {
	t.Parallel()
	g := buildGreeting()
	require.Equal(t, byte(0xFF), g[0], "signature prefix must be 0xFF")
	require.Equal(t, byte(0x7F), g[9], "signature suffix must be 0x7F")
}

func TestBuildGreeting_PaddingBytes(t *testing.T) {
	t.Parallel()
	g := buildGreeting()
	// Bytes [1..7] are zero; byte [8] = 0x01 (big-endian encoding of the value 1).
	for i := 1; i < 8; i++ {
		require.Equal(t, byte(0x00), g[i], "padding byte[%d] must be 0x00", i)
	}
	require.Equal(t, byte(0x01), g[8], "byte[8] must be 0x01")
}

func TestBuildGreeting_Version(t *testing.T) {
	t.Parallel()
	g := buildGreeting()
	require.Equal(t, byte(3), g[10], "major version must be 3")
	require.Equal(t, byte(1), g[11], "minor version must be 1")
}

func TestBuildGreeting_Mechanism(t *testing.T) {
	t.Parallel()
	g := buildGreeting()
	require.Equal(t, []byte("NULL"), g[12:16])
	for i := 16; i < 32; i++ {
		require.Equal(t, byte(0x00), g[i], "mechanism padding byte[%d] must be 0x00", i)
	}
}

func TestBuildGreeting_AsServerAndFiller(t *testing.T) {
	t.Parallel()
	g := buildGreeting()
	// as-server = 0 (client), filler = all zeros.
	for i := 32; i < 64; i++ {
		require.Equal(t, byte(0x00), g[i], "filler byte[%d] must be 0x00", i)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// buildCommand
// ═════════════════════════════════════════════════════════════════════════════

func TestBuildCommand_ShortBody_FlagAndSize(t *testing.T) {
	t.Parallel()
	data := []byte("hello")
	out := buildCommand("PING", data)
	// flags = flagCommand (no flagLong)
	require.Equal(t, flagCommand, out[0], "short frame must use flagCommand only")
	// bodyLen = 1 (nameLen) + 4 ("PING") + 5 ("hello") = 10
	require.Equal(t, byte(10), out[1], "short frame size byte must equal bodyLen")
}

func TestBuildCommand_ShortBody_NameAndData(t *testing.T) {
	t.Parallel()
	data := []byte("world")
	out := buildCommand("TEST", data)
	// [flags, size, nameLen, 'T', 'E', 'S', 'T', 'w', 'o', 'r', 'l', 'd']
	require.Equal(t, byte(4), out[2], "name-length prefix must be 4")
	require.Equal(t, []byte("TEST"), out[3:7])
	require.Equal(t, data, out[7:])
}

func TestBuildCommand_LongBody_FlagAndSize(t *testing.T) {
	t.Parallel()
	// bodyLen = 1 + 4 + len(data). We need bodyLen > 255, so len(data) > 250.
	data := bytes.Repeat([]byte("x"), 300)
	out := buildCommand("LONG", data)
	// flags = flagCommand | flagLong
	require.Equal(t, flagCommand|flagLong, out[0])
	// 8-byte big-endian size: 1 + 4 + 300 = 305
	wantSize := uint64(305)
	gotSize := binary.BigEndian.Uint64(out[1:9])
	require.Equal(t, wantSize, gotSize)
	// Name and data follow.
	require.Equal(t, byte(4), out[9])
	require.Equal(t, []byte("LONG"), out[10:14])
	require.Equal(t, data, out[14:])
}

func TestBuildCommand_EmptyData(t *testing.T) {
	t.Parallel()
	out := buildCommand("PONG", nil)
	// bodyLen = 1 + 4 + 0 = 5
	require.Equal(t, flagCommand, out[0])
	require.Equal(t, byte(5), out[1])
	require.Equal(t, byte(4), out[2])
	require.Equal(t, []byte("PONG"), out[3:7])
	require.Len(t, out, 7)
}

func TestBuildCommand_EmptyName(t *testing.T) {
	t.Parallel()
	out := buildCommand("", []byte("data"))
	// bodyLen = 1 + 0 + 4 = 5
	require.Equal(t, flagCommand, out[0])
	require.Equal(t, byte(5), out[1])
	require.Equal(t, byte(0), out[2], "name-length must be 0")
	require.Equal(t, []byte("data"), out[3:])
}

// ═════════════════════════════════════════════════════════════════════════════
// buildReady
// ═════════════════════════════════════════════════════════════════════════════

func TestBuildReady_SUB_ContainsSocketType(t *testing.T) {
	t.Parallel()
	out := buildReady("SUB")
	// The frame should contain the key "Socket-Type" and the value "SUB".
	require.Contains(t, string(out), "Socket-Type")
	require.Contains(t, string(out), "SUB")
	require.Equal(t, flagCommand, out[0])
}

func TestBuildReady_PUB_ContainsPUB(t *testing.T) {
	t.Parallel()
	out := buildReady("PUB")
	require.Contains(t, string(out), "PUB")
}

func TestBuildReady_MetadataStructure(t *testing.T) {
	t.Parallel()
	socketType := "SUB"
	out := buildReady(socketType)

	// Parse the command manually to verify the metadata encoding.
	// Skip: flags(1) + size(1) + nameLen(1) + "READY"(5) = 8 bytes
	meta := out[8:]
	// key-length (1 byte) + key + value-length (4 bytes big-endian) + value
	require.Equal(t, byte(len("Socket-Type")), meta[0])
	require.Equal(t, []byte("Socket-Type"), meta[1:12])
	wantVLen := binary.BigEndian.Uint32(meta[12:16])
	require.Equal(t, uint32(len(socketType)), wantVLen)
	require.Equal(t, []byte(socketType), meta[16:16+len(socketType)])
}

// ═════════════════════════════════════════════════════════════════════════════
// buildSubscribe
// ═════════════════════════════════════════════════════════════════════════════

func TestBuildSubscribe_HashblockTopic(t *testing.T) {
	t.Parallel()
	topic := []byte("hashblock")
	out := buildSubscribe(topic)
	// Must be buildCommand("SUBSCRIBE", topic).
	require.Equal(t, buildCommand("SUBSCRIBE", topic), out)
	require.Contains(t, string(out), "SUBSCRIBE")
	require.Contains(t, string(out), "hashblock")
}

func TestBuildSubscribe_EmptyTopic_SubscribesToAll(t *testing.T) {
	t.Parallel()
	out := buildSubscribe(nil)
	// Empty topic = subscribe to everything.
	require.Equal(t, buildCommand("SUBSCRIBE", nil), out)
}

// ═════════════════════════════════════════════════════════════════════════════
// parseCommandBody
// ═════════════════════════════════════════════════════════════════════════════

func TestParseCommandBody_EmptyBody_ReturnsFalse(t *testing.T) {
	t.Parallel()
	_, _, ok := parseCommandBody(nil)
	require.False(t, ok)
	_, _, ok = parseCommandBody([]byte{})
	require.False(t, ok)
}

func TestParseCommandBody_BodyTooShortForName_ReturnsFalse(t *testing.T) {
	t.Parallel()
	// nameLen = 5 but only 3 bytes of name provided.
	body := append([]byte{5}, []byte("abc")...)
	_, _, ok := parseCommandBody(body)
	require.False(t, ok)
}

func TestParseCommandBody_ValidBody_ReturnsNameAndData(t *testing.T) {
	t.Parallel()
	// nameLen(1) + "READY"(5) + "somedata"(8)
	body := append([]byte{5}, append([]byte("READY"), []byte("somedata")...)...)
	name, data, ok := parseCommandBody(body)
	require.True(t, ok)
	require.Equal(t, "READY", name)
	require.Equal(t, []byte("somedata"), data)
}

func TestParseCommandBody_ZeroLengthName(t *testing.T) {
	t.Parallel()
	// nameLen = 0, data = "hello"
	body := append([]byte{0}, []byte("hello")...)
	name, data, ok := parseCommandBody(body)
	require.True(t, ok)
	require.Equal(t, "", name)
	require.Equal(t, []byte("hello"), data)
}

func TestParseCommandBody_NameExactlyFillsBody_EmptyData(t *testing.T) {
	t.Parallel()
	body := append([]byte{4}, []byte("PING")...)
	name, data, ok := parseCommandBody(body)
	require.True(t, ok)
	require.Equal(t, "PING", name)
	require.Len(t, data, 0)
}

// ═════════════════════════════════════════════════════════════════════════════
// readFrame
// ═════════════════════════════════════════════════════════════════════════════

func TestReadFrame_ShortFrame_ReturnsBodyAndFlags(t *testing.T) {
	t.Parallel()
	body := []byte("hello")
	wire := encodeShortFrame(flagMore, body)
	c := connFromBytes(wire)
	flags, got, err := c.readFrame(context.Background())
	require.NoError(t, err)
	require.Equal(t, flagMore, flags)
	require.Equal(t, body, got)
}

func TestReadFrame_LongFrame_ReturnsBodyAndFlags(t *testing.T) {
	t.Parallel()
	body := bytes.Repeat([]byte("x"), 10)
	wire := encodeLongFrame(flagMore, body)
	c := connFromBytes(wire)
	flags, got, err := c.readFrame(context.Background())
	require.NoError(t, err)
	require.Equal(t, flagMore|flagLong, flags)
	require.Equal(t, body, got)
}

func TestReadFrame_CommandFrame_FlagsPreserved(t *testing.T) {
	t.Parallel()
	body := buildCommand("READY", nil)[2:] // body only (skip flags+size)
	// Wrap as a short frame with flagCommand set.
	wire := encodeShortFrame(flagCommand, body)
	c := connFromBytes(wire)
	flags, _, err := c.readFrame(context.Background())
	require.NoError(t, err)
	require.Equal(t, flagCommand, flags)
}

func TestReadFrame_EmptyBody_Succeeds(t *testing.T) {
	t.Parallel()
	wire := encodeShortFrame(0x00, nil)
	c := connFromBytes(wire)
	flags, got, err := c.readFrame(context.Background())
	require.NoError(t, err)
	require.Equal(t, byte(0x00), flags)
	require.Empty(t, got)
}

func TestReadFrame_OversizedBody_ReturnsError(t *testing.T) {
	t.Parallel()
	// Build a long-frame header claiming zmtpMaxFrameBody+1 bytes.
	oversize := uint64(zmtpMaxFrameBody) + 1
	var sz [8]byte
	binary.BigEndian.PutUint64(sz[:], oversize)
	wire := append([]byte{flagLong}, sz[:]...)
	// Do NOT append actual body bytes — the check fires before the read.
	c := connFromBytes(wire)
	_, _, err := c.readFrame(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds")
}

func TestReadFrame_EOFOnFlagsByte_ReturnsError(t *testing.T) {
	t.Parallel()
	c := connFromBytes(nil) // empty reader
	_, _, err := c.readFrame(context.Background())
	require.ErrorIs(t, err, io.EOF)
}

func TestReadFrame_EOFDuringShortSizeByte_ReturnsError(t *testing.T) {
	t.Parallel()
	// Only the flags byte, no size byte.
	c := connFromBytes([]byte{flagMore})
	_, _, err := c.readFrame(context.Background())
	require.Error(t, err)
}

func TestReadFrame_EOFDuringLongSizeField_ReturnsError(t *testing.T) {
	t.Parallel()
	// flags = flagLong set, but only 3 of the 8 size bytes are present.
	c := connFromBytes([]byte{flagLong, 0, 0, 0})
	_, _, err := c.readFrame(context.Background())
	require.Error(t, err)
}

func TestReadFrame_EOFDuringBody_ReturnsError(t *testing.T) {
	t.Parallel()
	// Header claims 5 bytes of body but only 2 are provided.
	wire := append([]byte{flagMore, 5}, []byte("ab")...)
	c := connFromBytes(wire)
	_, _, err := c.readFrame(context.Background())
	require.Error(t, err)
}

// ═════════════════════════════════════════════════════════════════════════════
// readAndValidateGreeting
// ═════════════════════════════════════════════════════════════════════════════

func TestReadAndValidateGreeting_ValidNULL_Succeeds(t *testing.T) {
	t.Parallel()
	g := buildValidGreeting64(3, 1, "NULL")
	c := connFromBytes(g[:])
	require.NoError(t, c.readAndValidateGreeting(context.Background()))
}

func TestReadAndValidateGreeting_MajorVersion4_Succeeds(t *testing.T) {
	t.Parallel()
	// Major version ≥ 3 is accepted for forward-compat.
	g := buildValidGreeting64(4, 0, "NULL")
	c := connFromBytes(g[:])
	require.NoError(t, c.readAndValidateGreeting(context.Background()))
}

func TestReadAndValidateGreeting_WrongSignatureByte0_ReturnsError(t *testing.T) {
	t.Parallel()
	g := buildValidGreeting64(3, 1, "NULL")
	g[0] = 0x00 // corrupt signature prefix
	c := connFromBytes(g[:])
	require.Error(t, c.readAndValidateGreeting(context.Background()))
}

func TestReadAndValidateGreeting_WrongSignatureByte8_IgnoredPerRFC(t *testing.T) {
	t.Parallel()
	// Per ZMTP 3.1 RFC §3.3, bytes [1..8] are reserved padding and MUST be ignored.
	// A corrupted byte [8] should not cause an error.
	g := buildValidGreeting64(3, 1, "NULL")
	g[8] = 0x00 // corrupt signature byte [8] — should be ignored
	c := connFromBytes(g[:])
	require.NoError(t, c.readAndValidateGreeting(context.Background()))
}

func TestReadAndValidateGreeting_WrongSignatureByte9_ReturnsError(t *testing.T) {
	t.Parallel()
	g := buildValidGreeting64(3, 1, "NULL")
	g[9] = 0x00 // corrupt signature suffix
	c := connFromBytes(g[:])
	require.Error(t, c.readAndValidateGreeting(context.Background()))
}

func TestReadAndValidateGreeting_MajorVersionTooOld_ReturnsError(t *testing.T) {
	t.Parallel()
	g := buildValidGreeting64(2, 0, "NULL")
	c := connFromBytes(g[:])
	err := c.readAndValidateGreeting(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "too old")
}

func TestReadAndValidateGreeting_NonNULLMechanism_ReturnsError(t *testing.T) {
	t.Parallel()
	for _, mech := range []string{"PLAIN", "CURVE", "GSSAPI"} {
		g := buildValidGreeting64(3, 1, mech)
		c := connFromBytes(g[:])
		err := c.readAndValidateGreeting(context.Background())
		require.Error(t, err, "mechanism %q should be rejected", mech)
		require.Contains(t, err.Error(), mech)
	}
}

func TestReadAndValidateGreeting_ShortRead_ReturnsError(t *testing.T) {
	t.Parallel()
	// Provide only 32 bytes instead of 64.
	c := connFromBytes(make([]byte, 32))
	require.Error(t, c.readAndValidateGreeting(context.Background()))
}

func TestReadAndValidateGreeting_EmptyReader_ReturnsError(t *testing.T) {
	t.Parallel()
	c := connFromBytes(nil)
	require.Error(t, c.readAndValidateGreeting(context.Background()))
}

// ═════════════════════════════════════════════════════════════════════════════
// readExpectedCommand
// ═════════════════════════════════════════════════════════════════════════════

func TestReadExpectedCommand_CorrectName_Succeeds(t *testing.T) {
	t.Parallel()
	frame := buildReady("PUB")
	c := connFromBytes(frame)
	require.NoError(t, c.readReadyCommand(context.Background()))
}

func TestReadExpectedCommand_WrongName_ReturnsError(t *testing.T) {
	t.Parallel()
	frame := buildCommand("ERROR", nil)
	c := connFromBytes(frame)
	err := c.readReadyCommand(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "READY")
	require.Contains(t, err.Error(), "ERROR")
}

func TestReadExpectedCommand_MessageFrameNotCommand_ReturnsError(t *testing.T) {
	t.Parallel()
	// A message frame (flagMore set, no flagCommand).
	wire := encodeShortFrame(flagMore, []byte("data"))
	c := connFromBytes(wire)
	err := c.readReadyCommand(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "command frame")
}

func TestReadExpectedCommand_MalformedCommandBody_ReturnsError(t *testing.T) {
	t.Parallel()
	// A command frame whose body is empty (no nameLen byte).
	wire := encodeShortFrame(flagCommand, nil)
	c := connFromBytes(wire)
	require.Error(t, c.readReadyCommand(context.Background()))
}

func TestReadExpectedCommand_EmptyReader_ReturnsError(t *testing.T) {
	t.Parallel()
	c := connFromBytes(nil)
	require.Error(t, c.readReadyCommand(context.Background()))
}

func TestReadReadyCommand_WrongSocketType_ReturnsError(t *testing.T) {
	t.Parallel()
	for _, st := range []string{"PUSH", "DEALER", "PULL", "ROUTER"} {
		frame := buildReady(st)
		c := connFromBytes(frame)
		err := c.readReadyCommand(context.Background())
		require.Error(t, err, "Socket-Type %s should be rejected", st)
		require.Contains(t, err.Error(), "expected Socket-Type PUB")
		require.Contains(t, err.Error(), st)
	}
}

func TestReadReadyCommand_MissingSocketType_ReturnsError(t *testing.T) {
	t.Parallel()
	// Build a READY command with no Socket-Type metadata key.
	frame := buildCommand("READY", nil) // empty metadata
	c := connFromBytes(frame)
	err := c.readReadyCommand(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Socket-Type")
	require.Contains(t, err.Error(), "missing")
}

// ═════════════════════════════════════════════════════════════════════════════
// parseReadyMetadata
// ═════════════════════════════════════════════════════════════════════════════

func TestParseReadyMetadata_Empty_ReturnsEmptyMap(t *testing.T) {
	t.Parallel()
	m := parseReadyMetadata(nil)
	require.Empty(t, m)
}

func TestParseReadyMetadata_SinglePair_Succeeds(t *testing.T) {
	t.Parallel()
	// Build metadata: key="Socket-Type"(11) value="PUB"(3)
	var data []byte
	data = append(data, 11)  // key length
	data = append(data, "Socket-Type"...)
	data = append(data, 0, 0, 0, 3)  // value length big-endian
	data = append(data, "PUB"...)
	m := parseReadyMetadata(data)
	require.Equal(t, "PUB", m["Socket-Type"])
}

func TestParseReadyMetadata_TruncatedAfterKeyLength_StopsGracefully(t *testing.T) {
	t.Parallel()
	// Just the key length byte, no key.
	data := []byte{5}
	m := parseReadyMetadata(data)
	require.Empty(t, m)
}

func TestParseReadyMetadata_TruncatedAfterKey_StopsGracefully(t *testing.T) {
	t.Parallel()
	// Key length but missing value length.
	var data []byte
	data = append(data, 3)  // key length
	data = append(data, "foo"...)
	// Missing 4-byte value length
	m := parseReadyMetadata(data)
	require.Empty(t, m)
}

func TestParseReadyMetadata_TruncatedAfterValueLength_StopsGracefully(t *testing.T) {
	t.Parallel()
	// Key and value length, but missing actual value.
	var data []byte
	data = append(data, 3)              // key length
	data = append(data, "foo"...)
	data = append(data, 0, 0, 0, 5)    // value length = 5
	// Only 2 bytes of value (need 5)
	data = append(data, "ab"...)
	m := parseReadyMetadata(data)
	require.Empty(t, m)
}

func TestParseReadyMetadata_MultiplePairs_AllParsed(t *testing.T) {
	t.Parallel()
	// Two pairs: Socket-Type=PUB, Identity=test
	var data []byte
	// Pair 1: Socket-Type=PUB
	data = append(data, 11)
	data = append(data, "Socket-Type"...)
	data = append(data, 0, 0, 0, 3)
	data = append(data, "PUB"...)
	// Pair 2: Identity=test
	data = append(data, 8)
	data = append(data, "Identity"...)
	data = append(data, 0, 0, 0, 4)
	data = append(data, "test"...)
	m := parseReadyMetadata(data)
	require.Equal(t, "PUB", m["Socket-Type"])
	require.Equal(t, "test", m["Identity"])
}

// ═════════════════════════════════════════════════════════════════════════════
// handleIncomingCommand
// ═════════════════════════════════════════════════════════════════════════════

// TestHandleIncomingCommand_PING_SendsCorrectPONG verifies that the PONG
// response echoes only the ping context — NOT the 2-byte TTL field.
// This is the spec-correctness fix: ZMTP 3.1 §4 requires PONG body = ping-context only.
func TestHandleIncomingCommand_PING_SendsCorrectPONG(t *testing.T) {
	t.Parallel()

	// Use net.Pipe() so we can observe what is written to the "wire".
	client, server := net.Pipe()
	defer func() { require.NoError(t, client.Close()) }()
	defer func() { require.NoError(t, server.Close()) }()

	c := &zmtpConn{
		tcp: client,
		r:   bufio.NewReaderSize(client, zmtpReadBuf),
	}

	// Build a PING frame body: nameLen(1) + "PING"(4) + TTL(2) + context
	ttl := []byte{0x00, 0x14}   // TTL = 20
	pingCtx := []byte("ctx123") // ping context to be echoed
	pingBody := append([]byte{4, 'P', 'I', 'N', 'G'}, ttl...)
	pingBody = append(pingBody, pingCtx...)

	received := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 256)
		require.NoError(t, server.SetDeadline(time.Now().Add(time.Second)))
		n, err := io.ReadFull(server, buf[:len(buildCommand("PONG", pingCtx))])
		require.NoError(t, err)
		received <- buf[:n]
	}()

	err := c.handleIncomingCommand(context.Background(), pingBody)
	require.NoError(t, err)

	got := <-received
	// The PONG must echo only pingCtx, not the TTL.
	want := buildCommand("PONG", pingCtx)
	require.Equal(t, want, got, "PONG must echo ping-context only, not TTL+context")
}

func TestHandleIncomingCommand_PING_NoContext_SendsEmptyPONG(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() { require.NoError(t, client.Close()) }()
	defer func() { require.NoError(t, server.Close()) }()

	c := &zmtpConn{tcp: client, r: bufio.NewReaderSize(client, zmtpReadBuf)}

	// PING with TTL only, no context.
	pingBody := []byte{4, 'P', 'I', 'N', 'G', 0x00, 0x0A} // TTL=10, empty context

	want := buildCommand("PONG", nil)
	received := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 256)
		require.NoError(t, server.SetDeadline(time.Now().Add(time.Second)))
		n, err := io.ReadFull(server, buf[:len(want)])
		require.NoError(t, err)
		received <- buf[:n]
	}()

	require.NoError(t, c.handleIncomingCommand(context.Background(), pingBody))
	require.Equal(t, want, <-received, "PONG for TTL-only PING must have empty context")
}

func TestHandleIncomingCommand_PING_TruncatedTTL_SendsEmptyPONG(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() { require.NoError(t, client.Close()) }()
	defer func() { require.NoError(t, server.Close()) }()

	c := &zmtpConn{tcp: client, r: bufio.NewReaderSize(client, zmtpReadBuf)}

	// PING with only 1 byte of TTL (truncated/malformed) — len(data) < 2.
	pingBody := []byte{4, 'P', 'I', 'N', 'G', 0x00} // only 1 TTL byte

	want := buildCommand("PONG", nil)
	received := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 256)
		require.NoError(t, server.SetDeadline(time.Now().Add(time.Second)))
		n, err := io.ReadFull(server, buf[:len(want)])
		require.NoError(t, err)
		received <- buf[:n]
	}()

	require.NoError(t, c.handleIncomingCommand(context.Background(), pingBody))
	require.Equal(t, want, <-received, "truncated PING must produce empty PONG context")
}

func TestHandleIncomingCommand_NonPING_ReturnsNilAndWritesNothing(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() { require.NoError(t, client.Close()) }()
	defer func() { require.NoError(t, server.Close()) }()

	c := &zmtpConn{tcp: client, r: bufio.NewReaderSize(client, zmtpReadBuf)}

	// ERROR command — should close connection and return error.
	errorBody := []byte{5, 'E', 'R', 'R', 'O', 'R', 'x'}
	err := c.handleIncomingCommand(context.Background(), errorBody)
	require.Error(t, err, "ERROR command must close connection and return error")
	require.Contains(t, err.Error(), "ERROR")
}

func TestHandleIncomingCommand_MalformedBody_ReturnsNil(t *testing.T) {
	t.Parallel()

	// No tcp needed — malformed body returns before any write.
	c := &zmtpConn{} // tcp is nil; handleIncomingCommand must not call Write

	// Empty body and body too short for declared nameLen.
	for _, body := range [][]byte{
		nil,
		{},
		{5, 'P', 'I'}, // nameLen=5 but only 2 name bytes
	} {
		err := c.handleIncomingCommand(context.Background(), body)
		require.NoError(t, err, "malformed body must return nil without panicking")
	}
}

func TestHandleIncomingCommand_SUBSCRIBE_ReturnsNil(t *testing.T) {
	t.Parallel()

	// SUBSCRIBE received on the read path is ignored (we are the subscriber,
	// not the publisher). No tcp write needed.
	c := &zmtpConn{}
	body := []byte{9, 'S', 'U', 'B', 'S', 'C', 'R', 'I', 'B', 'E'}
	require.NoError(t, c.handleIncomingCommand(context.Background(), body))
}
