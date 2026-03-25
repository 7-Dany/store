package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── computeSID ────────────────────────────────────────────────────────────────

func TestComputeSID_Deterministic(t *testing.T) {
	t.Parallel()
	key := "test-session-secret-32-bytes-long"
	sid1 := computeSID(key, "my-session", "jti-abc")
	sid2 := computeSID(key, "my-session", "jti-abc")
	assert.Equal(t, sid1, sid2)
}

func TestComputeSID_DifferentSessionID_DifferentOutput(t *testing.T) {
	t.Parallel()
	key := "test-session-secret-32-bytes-long"
	assert.NotEqual(t,
		computeSID(key, "session-A", "jti-1"),
		computeSID(key, "session-B", "jti-1"),
	)
}

func TestComputeSID_DifferentJTI_DifferentOutput(t *testing.T) {
	t.Parallel()
	key := "test-session-secret-32-bytes-long"
	assert.NotEqual(t,
		computeSID(key, "session-A", "jti-1"),
		computeSID(key, "session-A", "jti-2"),
	)
}

func TestComputeSID_DifferentKey_DifferentOutput(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t,
		computeSID("key-one-32-bytes-long-padded-xxx", "session", "jti"),
		computeSID("key-two-32-bytes-long-padded-xxx", "session", "jti"),
	)
}

func TestComputeSID_LengthPrefixPreventsColonCollision(t *testing.T) {
	// Without a length prefix, sessionID="abc:x" + jti="z" and
	// sessionID="abc" + jti="x:z" produce the same naive colon-concat message.
	// The length prefix MUST make them distinct.
	t.Parallel()
	key := "test-session-secret-32-bytes-long"
	sid1 := computeSID(key, "abc:x", "z")
	sid2 := computeSID(key, "abc", "x:z")
	assert.NotEqual(t, sid1, sid2, "length prefix must prevent sessionID:jti second-preimage collision")
}

func TestComputeSID_ReturnsHex(t *testing.T) {
	t.Parallel()
	result := computeSID("test-session-secret-32-bytes-long", "s", "j")
	assert.Regexp(t, `^[0-9a-f]{64}$`, result, "computeSID must return a 64-char lowercase hex string (SHA-256 HMAC)")
}

// ── computeIPClaim ────────────────────────────────────────────────────────────

func TestComputeIPClaim_IPv4_BindIPTrue_ReturnsCIDR(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "203.0.113.0/24", computeIPClaim("203.0.113.42", true))
}

func TestComputeIPClaim_IPv4_BindIPFalse_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	assert.Empty(t, computeIPClaim("203.0.113.42", false))
}

func TestComputeIPClaim_IPv6_BindIPTrue_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	assert.Empty(t, computeIPClaim("2001:db8::1", true), "IPv6 must never produce a claim")
}

func TestComputeIPClaim_EmptyIP_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	assert.Empty(t, computeIPClaim("", true))
}

func TestComputeIPClaim_InvalidIP_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	assert.Empty(t, computeIPClaim("not-an-ip", true))
}

func TestComputeIPClaim_MasksToNetworkAddress(t *testing.T) {
	t.Parallel()
	// Host 10.0.0.255 → network 10.0.0.0/24, not 10.0.0.255/24.
	assert.Equal(t, "10.0.0.0/24", computeIPClaim("10.0.0.255", true))
}

func TestComputeIPClaim_LoopbackIPv4(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "127.0.0.0/24", computeIPClaim("127.0.0.1", true))
}

// ── computeJTIHash ────────────────────────────────────────────────────────────

func TestComputeJTIHash_Deterministic(t *testing.T) {
	t.Parallel()
	h1 := computeJTIHash("some-jti", "server-secret-key")
	h2 := computeJTIHash("some-jti", "server-secret-key")
	assert.Equal(t, h1, h2)
}

func TestComputeJTIHash_DifferentJTI_DifferentHash(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t,
		computeJTIHash("jti-1", "secret"),
		computeJTIHash("jti-2", "secret"),
	)
}

func TestComputeJTIHash_DifferentSecret_DifferentHash(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t,
		computeJTIHash("jti", "secret-a"),
		computeJTIHash("jti", "secret-b"),
	)
}

func TestComputeJTIHash_ReturnsHex(t *testing.T) {
	t.Parallel()
	result := computeJTIHash("jti", "secret")
	assert.Regexp(t, `^[0-9a-f]{64}$`, result, "computeJTIHash must return a 64-char lowercase hex string")
}

// ── computeIPHash ─────────────────────────────────────────────────────────────

func TestComputeIPHash_EmptyIP_ReturnsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, computeIPHash("", "rotation-key"))
}

func TestComputeIPHash_NonEmptyIP_ReturnsPointer(t *testing.T) {
	t.Parallel()
	result := computeIPHash("1.2.3.4", "rotation-key")
	assert.NotNil(t, result)
	assert.Regexp(t, `^[0-9a-f]{64}$`, *result, "computeIPHash must return a 64-char lowercase hex string")
}

func TestComputeIPHash_Deterministic(t *testing.T) {
	t.Parallel()
	h1 := computeIPHash("1.2.3.4", "rotation-key")
	h2 := computeIPHash("1.2.3.4", "rotation-key")
	require := assert.New(t)
	require.NotNil(h1)
	require.NotNil(h2)
	assert.Equal(t, *h1, *h2)
}

func TestComputeIPHash_DifferentIP_DifferentHash(t *testing.T) {
	t.Parallel()
	h1 := computeIPHash("1.2.3.4", "key")
	h2 := computeIPHash("1.2.3.5", "key")
	assert.NotEqual(t, *h1, *h2)
}

func TestComputeIPHash_DifferentRotationKey_DifferentHash(t *testing.T) {
	t.Parallel()
	h1 := computeIPHash("1.2.3.4", "key-monday")
	h2 := computeIPHash("1.2.3.4", "key-tuesday")
	assert.NotEqual(t, *h1, *h2, "rotating the daily key must produce a different hash for the same IP")
}
