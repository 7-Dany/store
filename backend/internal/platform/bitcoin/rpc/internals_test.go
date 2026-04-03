package rpc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Credential tests ──────────────────────────────────────────────────────────

func TestCredential_Stringer_ReturnsRedacted(t *testing.T) {
	c := credential("super-secret-password")
	assert.Equal(t, "[redacted]", c.String())
}

func TestCredential_GoString_ReturnsRedacted(t *testing.T) {
	c := credential("super-secret-password")
	assert.Equal(t, "[redacted]", c.GoString())
}

func TestCredential_MarshalText_ReturnsRedacted(t *testing.T) {
	c := credential("super-secret-password")
	b, err := c.MarshalText()
	require.NoError(t, err)
	assert.Equal(t, []byte("[redacted]"), b)
}

func TestCredential_MarshalJSON_ReturnsRedacted(t *testing.T) {
	c := credential("super-secret-password")
	b, err := c.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, []byte(`"[redacted]"`), b)
}

func TestCredential_LogValue_ReturnsRedacted(t *testing.T) {
	c := credential("super-secret-password")
	v := c.LogValue()
	assert.Equal(t, slog.KindString, v.Kind())
	assert.Equal(t, "[redacted]", v.String())
}

func TestNew_CredentialsNeverInError(t *testing.T) {
	_, err := New("127.0.0.1", "bad-port", "my-user", "my-secret-pass", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "my-user")
	assert.NotContains(t, err.Error(), "my-secret-pass")
}

// ── Security tests ────────────────────────────────────────────────────────────

func TestNew_NonLoopbackHost_Panics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host      string
		wantPanic bool
	}{
		{"127.0.0.1", false},
		{"127.1.2.3", false},
		{"0.0.0.0", true},
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"8.8.8.8", true},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			fn := func() { _, _ = New(tc.host, "8332", "user", "pass", nil) }
			if tc.wantPanic {
				require.Panics(t, fn, "host %q should panic", tc.host)
			} else {
				require.NotPanics(t, fn, "host %q should not panic", tc.host)
			}
		})
	}
}

func TestRequireLoopbackHost_IPv6Loopback_Succeeds(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() { requireLoopbackHost("::1", "TEST") })
}

func TestRequireLoopbackHost_Localhost_Succeeds(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() { requireLoopbackHost("localhost", "TEST") })
}

// ── Helper tests ──────────────────────────────────────────────────────────────

func TestContextReader_CancelsRead(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pr, _ := io.Pipe()
	t.Cleanup(func() { pr.Close() })

	cr := &contextReader{ctx: ctx, r: pr}
	buf := make([]byte, 8)
	n, err := cr.Read(buf)
	assert.Equal(t, 0, n)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestContextReader_PassesThrough(t *testing.T) {
	t.Parallel()
	data := []byte("hello")
	cr := &contextReader{ctx: context.Background(), r: bytes.NewReader(data)}
	buf := make([]byte, len(data))
	n, err := cr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, buf)
}

func TestRpcNextBackoff_StaysWithinCeiling(t *testing.T) {
	t.Parallel()
	current := RPCRetryBase
	for range 20 {
		next := rpcNextBackoff(current, RPCRetryCeiling)
		assert.LessOrEqual(t, next, RPCRetryCeiling)
		assert.Greater(t, next, time.Duration(0))
		current = next
	}
}

func TestRpcNextBackoff_Increases(t *testing.T) {
	t.Parallel()
	next := rpcNextBackoff(RPCRetryBase, RPCRetryCeiling)
	assert.Greater(t, next, RPCRetryBase)
}

func TestRpcNextBackoff_JitterIsNonDeterministic(t *testing.T) {
	t.Parallel()
	first := rpcNextBackoff(4*time.Second, RPCRetryCeiling)
	varied := false
	for range 50 {
		if rpcNextBackoff(4*time.Second, RPCRetryCeiling) != first {
			varied = true
			break
		}
	}
	assert.True(t, varied, "jitter must produce variation across calls")
}

func TestRpcNextBackoff_ZeroCeiling_ReturnsZero(t *testing.T) {
	t.Parallel()
	assert.Equal(t, time.Duration(0), rpcNextBackoff(RPCRetryBase, 0))
}

func TestSleepCtx_CompletesDuration(t *testing.T) {
	t.Parallel()
	const sleep = 30 * time.Millisecond
	start := time.Now()
	ok := sleepCtx(context.Background(), sleep)
	assert.True(t, ok)
	assert.GreaterOrEqual(t, time.Since(start), sleep/2)
}

func TestSleepCtx_CancelsEarly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		done <- sleepCtx(ctx, 10*time.Second)
	}()

	<-started
	cancel()

	select {
	case ok := <-done:
		assert.False(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("sleepCtx did not return after cancellation")
	}
}

func TestSleepCtx_ZeroDuration_ReturnsImmediately(t *testing.T) {
	t.Parallel()
	start := time.Now()
	ok := sleepCtx(context.Background(), 0)
	assert.True(t, ok)
	assert.Less(t, time.Since(start), 50*time.Millisecond)
}
