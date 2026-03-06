//go:build integration_test

// redis_test.go contains all RedisStore integration tests.
//
// All RedisStore tests live here. They are compiled only when the
// integration_test build tag is active (e.g. `go test -tags integration_test`
// or `make test-integration`). When the tag is absent the file is excluded entirely.
//
// Tests that require a live Redis instance call newRedisStore(t), which
// internally calls redisURL(t) and skips automatically when neither
// TEST_REDIS_URL nor REDIS_URL is set. The two constructor tests
// (InvalidURL, UnreachableHost) do not need a live Redis instance and
// always run when the build tag is active.
package kvstore_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/config"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// redisURL returns the Redis URL for integration tests.
// It reads TEST_REDIS_URL first, then falls back to REDIS_URL.
// The test is skipped when neither variable is set.
func redisURL(t *testing.T) string {
	t.Helper()
	u := config.TestRedisURL()
	if u == "" {
		t.Skip("skipping Redis integration test: TEST_REDIS_URL and REDIS_URL are both unset")
	}
	return u
}

func newRedisStore(t *testing.T) *kvstore.RedisStore {
	t.Helper()
	s, err := kvstore.NewRedisStore(redisURL(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// flushTestKey ensures isolation between tests by deleting a key at the start
// and end of each test.
func flushTestKey(t *testing.T, s *kvstore.RedisStore, key string) {
	t.Helper()
	_ = s.Delete(context.Background(), key)
	t.Cleanup(func() { _ = s.Delete(context.Background(), key) })
}

// cancelledCtx returns an already-cancelled context.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// ── NewRedisStore ─────────────────────────────────────────────────────────────

// TestRedisStore_NewRedisStore_InvalidURL_Integration verifies that a malformed
// URL is rejected before any network dial is attempted.
func TestRedisStore_NewRedisStore_InvalidURL_Integration(t *testing.T) {
	t.Parallel()
	_, err := kvstore.NewRedisStore("not-a-valid-redis-url")
	require.Error(t, err)
	require.ErrorContains(t, err, "parse redis url")
}

// TestRedisStore_NewRedisStore_UnreachableHost_Integration verifies that a dial
// failure is surfaced as an error. Port 19999 is never used in CI or development.
// Covers: redis.go Ping error path + client.Close() on failure.
func TestRedisStore_NewRedisStore_UnreachableHost_Integration(t *testing.T) {
	t.Parallel()
	_, err := kvstore.NewRedisStore("redis://127.0.0.1:19999/0")
	require.Error(t, err)
	require.ErrorContains(t, err, "connect to redis")
}

// ── Get / Set ─────────────────────────────────────────────────────────────────

func TestRedisStore_Get_MissingKey_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	flushTestKey(t, s, "missing-key")

	_, err := s.Get(context.Background(), "missing-key")
	require.ErrorIs(t, err, kvstore.ErrNotFound)
}

func TestRedisStore_Set_Get_HappyPath_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	flushTestKey(t, s, "rk-set-get")

	require.NoError(t, s.Set(context.Background(), "rk-set-get", "hello", time.Minute))

	got, err := s.Get(context.Background(), "rk-set-get")
	require.NoError(t, err)
	require.Equal(t, "hello", got)
}

func TestRedisStore_Set_ZeroTTL_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	flushTestKey(t, s, "rk-zero-ttl")

	require.NoError(t, s.Set(context.Background(), "rk-zero-ttl", "v", 0))

	got, err := s.Get(context.Background(), "rk-zero-ttl")
	require.NoError(t, err)
	require.Equal(t, "v", got)
}

// TestRedisStore_Set_NegativeTTL_ReturnsError_Integration verifies that a
// negative TTL is rejected, consistent with InMemoryStore behaviour.
func TestRedisStore_Set_NegativeTTL_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	err := s.Set(context.Background(), "rk-neg-ttl", "v", -time.Second)
	require.Error(t, err, "negative TTL must be rejected by RedisStore")
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestRedisStore_Delete_Idempotent_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	flushTestKey(t, s, "rk-del")

	require.NoError(t, s.Delete(context.Background(), "rk-del"))
	require.NoError(t, s.Set(context.Background(), "rk-del", "v", time.Minute))
	require.NoError(t, s.Delete(context.Background(), "rk-del"))
	require.NoError(t, s.Delete(context.Background(), "rk-del"))

	_, err := s.Get(context.Background(), "rk-del")
	require.ErrorIs(t, err, kvstore.ErrNotFound)
}

// ── Exists ────────────────────────────────────────────────────────────────────

func TestRedisStore_Exists_PresentAndMissing_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	flushTestKey(t, s, "rk-exists")

	ok, err := s.Exists(context.Background(), "rk-exists")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.Set(context.Background(), "rk-exists", "v", time.Minute))

	ok, err = s.Exists(context.Background(), "rk-exists")
	require.NoError(t, err)
	require.True(t, ok)
}

// ── Keys ──────────────────────────────────────────────────────────────────────

func TestRedisStore_Keys_PrefixFilter_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	ctx := context.Background()
	flushTestKey(t, s, "scan:foo:1")
	flushTestKey(t, s, "scan:foo:2")
	flushTestKey(t, s, "scan:bar:1")

	require.NoError(t, s.Set(ctx, "scan:foo:1", "a", time.Minute))
	require.NoError(t, s.Set(ctx, "scan:foo:2", "b", time.Minute))
	require.NoError(t, s.Set(ctx, "scan:bar:1", "c", time.Minute))

	keys, err := s.Keys(ctx, "scan:foo:")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"scan:foo:1", "scan:foo:2"}, keys)
}

func TestRedisStore_Keys_EmptyPrefix_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	ctx := context.Background()
	flushTestKey(t, s, "ep:a")
	flushTestKey(t, s, "ep:b")

	require.NoError(t, s.Set(ctx, "ep:a", "1", time.Minute))
	require.NoError(t, s.Set(ctx, "ep:b", "2", time.Minute))

	keys, err := s.Keys(ctx, "ep:")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ep:a", "ep:b"}, keys)
}

// TestRedisStore_Keys_TrulyEmptyPrefix_UsesWildcard_Integration covers the
// `pattern = "*"` branch (redis.go) that fires when prefix is the empty string.
func TestRedisStore_Keys_TrulyEmptyPrefix_UsesWildcard_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	ctx := context.Background()
	flushTestKey(t, s, "wc:a")
	flushTestKey(t, s, "wc:b")

	require.NoError(t, s.Set(ctx, "wc:a", "1", time.Minute))
	require.NoError(t, s.Set(ctx, "wc:b", "2", time.Minute))

	// Passing "" triggers the pattern = "*" branch, which returns all keys.
	keys, err := s.Keys(ctx, "")
	require.NoError(t, err)
	require.Contains(t, keys, "wc:a")
	require.Contains(t, keys, "wc:b")
}

// ── TokenBlocklist ────────────────────────────────────────────────────────────

func TestRedisStore_BlockToken_ZeroTTL_IsNoop_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	jti := "redis-jti-zero"
	flushTestKey(t, s, "blocklist:jti:"+jti)

	require.NoError(t, s.BlockToken(context.Background(), jti, 0))

	blocked, err := s.IsTokenBlocked(context.Background(), jti)
	require.NoError(t, err)
	require.False(t, blocked)
}

func TestRedisStore_BlockToken_And_IsTokenBlocked_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	jti := "redis-jti-blocked"
	flushTestKey(t, s, "blocklist:jti:"+jti)

	require.NoError(t, s.BlockToken(context.Background(), jti, time.Minute))

	blocked, err := s.IsTokenBlocked(context.Background(), jti)
	require.NoError(t, err)
	require.True(t, blocked)
}

func TestRedisStore_IsTokenBlocked_UnknownJTI_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	jti := "redis-jti-unknown"
	flushTestKey(t, s, "blocklist:jti:"+jti)

	blocked, err := s.IsTokenBlocked(context.Background(), jti)
	require.NoError(t, err)
	require.False(t, blocked)
}

// TestRedisStore_BlockToken_NegativeTTL_IsNoop_Integration verifies that a
// negative TTL is treated as a no-op, consistent with InMemoryStore.
func TestRedisStore_BlockToken_NegativeTTL_IsNoop_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	jti := "redis-jti-neg-ttl"
	flushTestKey(t, s, "blocklist:jti:"+jti)

	require.NoError(t, s.BlockToken(context.Background(), jti, -time.Second))

	blocked, err := s.IsTokenBlocked(context.Background(), jti)
	require.NoError(t, err)
	require.False(t, blocked)
}

// TestRedisStore_BlockToken_ExpiresAfterTTL_Integration verifies that a
// blocked token is no longer reported as blocked after its TTL elapses,
// confirming the EX wiring in RedisStore.BlockToken.
func TestRedisStore_BlockToken_ExpiresAfterTTL_Integration(t *testing.T) {
	// Not parallel — this test sleeps.
	s := newRedisStore(t)
	ctx := context.Background()

	jti := "redis-jti-expiry"
	flushTestKey(t, s, "blocklist:jti:"+jti)

	require.NoError(t, s.BlockToken(ctx, jti, 500*time.Millisecond))

	blocked, err := s.IsTokenBlocked(ctx, jti)
	require.NoError(t, err)
	require.True(t, blocked, "token must be blocked immediately after BlockToken")

	time.Sleep(700 * time.Millisecond)

	blocked, err = s.IsTokenBlocked(ctx, jti)
	require.NoError(t, err)
	require.False(t, blocked, "token must no longer be blocked after TTL elapses")
}

// ── AtomicBucketAllow ─────────────────────────────────────────────────────────

func TestRedisStore_AtomicBucketAllow_ConsumesTokens_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-bucket"
	flushTestKey(t, s, key)

	// burst=2: first two requests allowed, third denied.
	ctx := context.Background()
	allowed1, err := s.AtomicBucketAllow(ctx, key, 1, 2, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed1)

	allowed2, err := s.AtomicBucketAllow(ctx, key, 1, 2, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed2)

	allowed3, err := s.AtomicBucketAllow(ctx, key, 1, 2, time.Minute)
	require.NoError(t, err)
	require.False(t, allowed3)
}

// TestRedisStore_AtomicBucketAllow_Concurrent_AdmitsExactlyBurst_Integration
// verifies the Lua script's atomic guarantee: with burst=N and N+M concurrent
// callers exactly N must be allowed and M denied, never N+k due to a race.
func TestRedisStore_AtomicBucketAllow_Concurrent_AdmitsExactlyBurst_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-bucket-concurrent"
	flushTestKey(t, s, key)

	const burst = 10
	const extra = 10 // callers beyond burst that must be denied
	ctx := context.Background()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
		denied  int
	)
	wg.Add(burst + extra)
	for range burst + extra {
		go func() {
			defer wg.Done()
			ok, err := s.AtomicBucketAllow(ctx, key, 1, burst, time.Minute)
			require.NoError(t, err)
			mu.Lock()
			if ok {
				allowed++
			} else {
				denied++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	require.Equal(t, burst, allowed, "exactly burst tokens must be admitted")
	require.Equal(t, extra, denied, "all callers beyond burst must be denied")
}

// ── AtomicBackoffIncrement / AtomicBackoffAllow ───────────────────────────────

func TestRedisStore_AtomicBackoffIncrement_And_Allow_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-backoff"
	flushTestKey(t, s, key)

	ctx := context.Background()

	// Before any failures, key is allowed.
	allowed, remaining, err := s.AtomicBackoffAllow(ctx, key)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Zero(t, remaining)

	// First failure: unlocks_at = now + baseDelay.
	// Use 500ms so the unlock window is safely open even when the Go wall clock
	// leads the Redis TIME source by a few hundred milliseconds (common in
	// WSL2 / Docker environments where the clocks are not tightly synchronised).
	unlocksAt, failures, err := s.AtomicBackoffIncrement(ctx, key, 500*time.Millisecond, time.Minute, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, failures)
	require.True(t, unlocksAt.After(time.Now()))

	// Key is now blocked.
	allowed, remaining, err = s.AtomicBackoffAllow(ctx, key)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Positive(t, remaining)

	// Wait for the backoff to clear: sleep until the actual unlock time
	// returned by AtomicBackoffIncrement plus a generous buffer. unlocksAt is
	// derived from Redis server TIME, while time.Until uses Go's wall clock.
	// 500ms absorbs clock skew that would cause a 100ms buffer to fall short.
	sleepFor := time.Until(unlocksAt) + 500*time.Millisecond
	if sleepFor > 0 {
		time.Sleep(sleepFor)
	}

	allowed, _, err = s.AtomicBackoffAllow(ctx, key)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestRedisStore_AtomicBackoffIncrement_ExponentialGrowth_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-backoff-exp"
	flushTestKey(t, s, key)

	ctx := context.Background()
	base := 50 * time.Millisecond
	maxD := time.Hour

	_, f1, err := s.AtomicBackoffIncrement(ctx, key, base, maxD, time.Hour)
	require.NoError(t, err)
	require.Equal(t, 1, f1)

	_, f2, err := s.AtomicBackoffIncrement(ctx, key, base, maxD, time.Hour)
	require.NoError(t, err)
	require.Equal(t, 2, f2)

	_, f3, err := s.AtomicBackoffIncrement(ctx, key, base, maxD, time.Hour)
	require.NoError(t, err)
	require.Equal(t, 3, f3)
}

func TestRedisStore_AtomicBackoffIncrement_RespectsMaxDelay_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-backoff-max"
	flushTestKey(t, s, key)

	ctx := context.Background()
	maxD := 200 * time.Millisecond

	var unlocksAt time.Time
	for range 20 {
		var err error
		unlocksAt, _, err = s.AtomicBackoffIncrement(ctx, key, 10*time.Millisecond, maxD, time.Hour)
		require.NoError(t, err)
	}

	// The unlock time should not exceed now + maxDelay + small margin.
	require.True(t, unlocksAt.Before(time.Now().Add(maxD+50*time.Millisecond)))
}

// TestRedisStore_AtomicBackoffIncrement_ConcurrentCallers_Integration verifies
// that N simultaneous increments produce a final failure count of N with no
// lost updates — the atomic Lua script must serialise them.
func TestRedisStore_AtomicBackoffIncrement_ConcurrentCallers_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-backoff-concurrent"
	flushTestKey(t, s, key)

	ctx := context.Background()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _, _ = s.AtomicBackoffIncrement(ctx, key, 10*time.Millisecond, time.Minute, time.Hour)
		}()
	}
	wg.Wait()

	// One extra increment to read the current failure count.
	_, failures, err := s.AtomicBackoffIncrement(ctx, key, 10*time.Millisecond, time.Minute, time.Hour)
	require.NoError(t, err)
	require.Equal(t, goroutines+1, failures, "no lost updates: all goroutine increments must be visible")
}

// ── StartCleanup / Close ──────────────────────────────────────────────────────

func TestRedisStore_StartCleanup_IsNoop_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled — StartCleanup must return promptly

	done := make(chan struct{})
	go func() {
		s.StartCleanup(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("StartCleanup did not return promptly")
	}
}

func TestRedisStore_Close_NoError_Integration(t *testing.T) {
	t.Parallel()
	s, err := kvstore.NewRedisStore(redisURL(t))
	require.NoError(t, err)
	require.NoError(t, s.Close())
}

// ── Cancelled-context error paths ─────────────────────────────────────────────

func TestRedisStore_Get_CtxCancelled_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	_, err := s.Get(cancelledCtx(), "any-key")
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.Get")
}

func TestRedisStore_Set_CtxCancelled_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	err := s.Set(cancelledCtx(), "any-key", "v", time.Minute)
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.Set")
}

func TestRedisStore_Delete_CtxCancelled_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	err := s.Delete(cancelledCtx(), "any-key")
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.Delete")
}

func TestRedisStore_Exists_CtxCancelled_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	_, err := s.Exists(cancelledCtx(), "any-key")
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.Exists")
}

func TestRedisStore_Keys_CtxCancelled_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	_, err := s.Keys(cancelledCtx(), "any-prefix:")
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.Keys")
}

// TestRedisStore_Keys_ClosedClient_ReturnsError_Integration covers the SCAN
// error branch when go-redis does not short-circuit on a cancelled context
// before issuing the command.
func TestRedisStore_Keys_ClosedClient_ReturnsError_Integration(t *testing.T) {
	// Not parallel: we intentionally close the store.
	s, err := kvstore.NewRedisStore(redisURL(t))
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = s.Keys(context.Background(), "any-prefix:")
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.Keys")
}

func TestRedisStore_AtomicBucketAllow_CtxCancelled_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	_, err := s.AtomicBucketAllow(cancelledCtx(), "any-key", 1, 1, time.Minute)
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.AtomicBucketAllow")
}

// BlockToken uses context.WithoutCancel internally, so a cancelled request
// context cannot trigger the error path. Instead we close the underlying
// connection first, which makes the Set command fail regardless of context.
func TestRedisStore_BlockToken_ClosedClient_ReturnsError_Integration(t *testing.T) {
	// Not parallel: we intentionally close the store.
	s, err := kvstore.NewRedisStore(redisURL(t))
	require.NoError(t, err)
	require.NoError(t, s.Close())

	err = s.BlockToken(context.Background(), "jti-closed", time.Minute)
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.BlockToken")
}

func TestRedisStore_AtomicBackoffIncrement_CtxCancelled_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	_, _, err := s.AtomicBackoffIncrement(cancelledCtx(), "any-key", time.Second, time.Minute, time.Hour)
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.AtomicBackoffIncrement")
}

func TestRedisStore_AtomicBackoffAllow_CtxCancelled_ReturnsError_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)

	_, _, err := s.AtomicBackoffAllow(cancelledCtx(), "any-key")
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.AtomicBackoffAllow")
}

// ── NewRedisStore ScriptLoad failure paths ────────────────────────────────────

// fakeFlakyRedisAddr starts a minimal RESP-speaking TCP server and returns its
// address as a redis:// URL.  The server responds to PING (and other
// initialisation commands) with the usual success replies, then:
//
//	"fail_incr"  – returns an error for the *first*  SCRIPT LOAD call
//	"fail_allow" – returns an error for the *second* SCRIPT LOAD call
//
// For the "fail_allow" mode the first SCRIPT LOAD receives a fake 40-char
// SHA so that NewRedisStore proceeds past atomicBackoffIncrementScript
// loading before hitting the injected failure for atomicBackoffAllowScript.
func fakeFlakyRedisAddr(t *testing.T, mode string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		scriptLoads := 0
		r := bufio.NewReader(conn)
		for {
			cmd, err := readRESPCommand(r)
			if err != nil || len(cmd) == 0 {
				return
			}
			upper := strings.ToUpper(cmd[0])
			switch upper {
			case "PING":
				_, _ = conn.Write([]byte("+PONG\r\n"))
			case "HELLO":
				// Simulate a pre-RESP3 Redis that does not know the HELLO
				// command. go-redis v9 falls back to RESP2 silently on an
				// "unknown command" error, but treats -NOPROTO as fatal.
				_, _ = conn.Write([]byte("-ERR unknown command 'HELLO'\r\n"))
			case "SCRIPT":
				if len(cmd) >= 2 && strings.ToUpper(cmd[1]) == "LOAD" {
					scriptLoads++
					shouldFail := mode == "fail_incr" ||
						(mode == "fail_allow" && scriptLoads == 2)
					if shouldFail {
						_, _ = conn.Write([]byte("-ERR injected script load failure\r\n"))
						return
					}
					// Return a placeholder 40-char SHA.
					const fakeSHA = "0000000000000000000000000000000000000000"
					_, _ = fmt.Fprintf(conn, "$40\r\n%s\r\n", fakeSHA)
				}
			default:
				_, _ = conn.Write([]byte("+OK\r\n"))
			}
		}
	}()

	return "redis://" + ln.Addr().String() + "/0"
}

// readRESPCommand reads one RESP multi-bulk (or inline) command from r.
func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return nil, nil
	}
	if line[0] != '*' {
		// Inline command (e.g. PING).
		return strings.Fields(line), nil
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("bad array header: %q", line)
	}
	args := make([]string, 0, n)
	for range n {
		sizeLine, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeLine = strings.TrimRight(sizeLine, "\r\n")
		if len(sizeLine) == 0 || sizeLine[0] != '$' {
			return nil, fmt.Errorf("expected bulk string, got %q", sizeLine)
		}
		size, err := strconv.Atoi(sizeLine[1:])
		if err != nil {
			return nil, err
		}
		bulk := make([]byte, size+2) // +2 for trailing \r\n
		if _, err := io.ReadFull(r, bulk); err != nil {
			return nil, err
		}
		args = append(args, string(bulk[:size]))
	}
	return args, nil
}

// TestRedisStore_NewRedisStore_IncrScriptLoadFails_Integration verifies that a
// failure during atomicBackoffIncrementScript loading is surfaced as an error
// and the underlying connection is closed (lines 51-54 of redis.go).
func TestRedisStore_NewRedisStore_IncrScriptLoadFails_Integration(t *testing.T) {
	t.Parallel()
	addr := fakeFlakyRedisAddr(t, "fail_incr")
	_, err := kvstore.NewRedisStore(addr)
	require.Error(t, err)
	require.ErrorContains(t, err, "load backoff increment script")
}

// TestRedisStore_NewRedisStore_AllowScriptLoadFails_Integration verifies that a
// failure during atomicBackoffAllowScript loading is surfaced as an error and
// the underlying connection is closed (lines 56-59 of redis.go).
func TestRedisStore_NewRedisStore_AllowScriptLoadFails_Integration(t *testing.T) {
	t.Parallel()
	addr := fakeFlakyRedisAddr(t, "fail_allow")
	_, err := kvstore.NewRedisStore(addr)
	require.Error(t, err)
	require.ErrorContains(t, err, "load backoff allow script")
}

// ── AtomicBucketAllow clamping ────────────────────────────────────────────────

// TestRedisStore_AtomicBucketAllow_ZeroRate_ClampsToOne_Integration verifies
// that passing rate=0 is clamped to 1 before the rate-limit call, exercising
// the `if r <= 0 { r = 1 }` branch (lines 162-164 of redis.go).
func TestRedisStore_AtomicBucketAllow_ZeroRate_ClampsToOne_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-bucket-zero-rate"
	flushTestKey(t, s, key)

	// rate=0 is clamped to 1; burst=5 gives enough headroom so the call succeeds.
	allowed, err := s.AtomicBucketAllow(context.Background(), key, 0, 5, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
}

// TestRedisStore_AtomicBucketAllow_ZeroBurst_ClampsToOne_Integration verifies
// that passing burst=0 is clamped to 1, exercising the `if b <= 0 { b = 1 }`
// branch (lines 165-167 of redis.go).
func TestRedisStore_AtomicBucketAllow_ZeroBurst_ClampsToOne_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-bucket-zero-burst"
	flushTestKey(t, s, key)

	// burst=0 is clamped to 1; the first call must be allowed.
	allowed, err := s.AtomicBucketAllow(context.Background(), key, 1, 0, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
}

// ── evalScript NOSCRIPT fallback ──────────────────────────────────────────────

// TestRedisStore_EvalScript_NOSCRIPTFallback_Integration exercises the branch
// in evalScript that handles a NOSCRIPT error by re-loading the Lua source
// and falling back to EVAL (lines 289-293 of redis.go).
//
// It achieves this by flushing the Redis script cache after NewRedisStore has
// pre-loaded the scripts, so the next EvalSha call receives NOSCRIPT.
func TestRedisStore_EvalScript_NOSCRIPTFallback_Integration(t *testing.T) {
	// Not parallel: flushes the shared script cache.
	s := newRedisStore(t)
	ctx := context.Background()
	key := "rk-noscript-fallback"
	flushTestKey(t, s, key)

	// Flush the script cache so the pre-loaded SHAs are no longer known to Redis.
	// Use the full parsed options (including any password) so the client can auth.
	redisOpts, err := redis.ParseURL(redisURL(t))
	require.NoError(t, err)
	rc := redis.NewClient(redisOpts)
	defer rc.Close()
	require.NoError(t, rc.ScriptFlush(ctx).Err())

	// AtomicBackoffIncrement uses EvalSha internally; NOSCRIPT triggers the
	// fallback Eval path, which must succeed and return a valid result.
	// Use 2s so unlocksAt (Redis TIME + 2000ms) is safely in the future even
	// when the Go wall clock is a few hundred milliseconds ahead of Redis TIME
	// (common in WSL2 / Docker environments).
	unlocksAt, failures, err := s.AtomicBackoffIncrement(ctx, key, 2*time.Second, time.Minute, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, failures)
	require.True(t, unlocksAt.After(time.Now()))
}

// ── AtomicBackoffIncrement bad-result defensive branches ─────────────────────

// loadBadScript pre-loads a Lua snippet that returns a value incompatible with
// the expected [int64, int64] pair and returns its SHA.
func loadBadScript(t *testing.T, url, src string) string {
	t.Helper()
	opts, err := redis.ParseURL(url)
	require.NoError(t, err)
	rc := redis.NewClient(opts)
	t.Cleanup(func() { _ = rc.Close() })
	sha, err := rc.ScriptLoad(context.Background(), src).Result()
	require.NoError(t, err)
	return sha
}

// TestRedisStore_AtomicBackoffIncrement_BadResultFormat_Integration covers the
// `!ok || len(values) != 2` guard in AtomicBackoffIncrement
// by injecting a Lua script that returns a plain string instead of an array.
func TestRedisStore_AtomicBackoffIncrement_BadResultFormat_Integration(t *testing.T) {
	t.Parallel()
	url := redisURL(t)
	// Script returns a string, not the expected two-element array.
	badSHA := loadBadScript(t, url, `return "not_an_array"`)

	badStore, err := kvstore.NewRedisStoreWithSHAs(url, badSHA, badSHA)
	require.NoError(t, err)
	t.Cleanup(func() { _ = badStore.Close() })

	_, _, err = badStore.AtomicBackoffIncrement(
		context.Background(), "rk-bad-incr-fmt",
		time.Second, time.Minute, time.Hour,
	)
	require.Error(t, err)
	require.ErrorContains(t, err, "unexpected result format")
}

// TestRedisStore_AtomicBackoffIncrement_BadValueTypes_Integration covers the
// `!ok1 || !ok2` guard in AtomicBackoffIncrement (lines 316-318) by injecting
// a Lua script that returns a two-element array of strings instead of int64s.
func TestRedisStore_AtomicBackoffIncrement_BadValueTypes_Integration(t *testing.T) {
	t.Parallel()
	url := redisURL(t)
	// Script returns strings; go-redis decodes them as []any{string, string}.
	badSHA := loadBadScript(t, url, `return {"hello", "world"}`)

	badStore, err := kvstore.NewRedisStoreWithSHAs(url, badSHA, badSHA)
	require.NoError(t, err)
	t.Cleanup(func() { _ = badStore.Close() })

	_, _, err = badStore.AtomicBackoffIncrement(
		context.Background(), "rk-bad-incr-types",
		time.Second, time.Minute, time.Hour,
	)
	require.Error(t, err)
	require.ErrorContains(t, err, "unexpected value types")
}

// ── AtomicBackoffAllow bad-result defensive branches ──────────────────────────

// TestRedisStore_AtomicBackoffAllow_BadResultFormat_Integration covers the
// `!ok || len(values) != 2` guard in AtomicBackoffAllow (lines 332-334) by
// injecting a Lua script that returns a plain string.
func TestRedisStore_AtomicBackoffAllow_BadResultFormat_Integration(t *testing.T) {
	t.Parallel()
	url := redisURL(t)
	badSHA := loadBadScript(t, url, `return "not_an_array"`)

	badStore, err := kvstore.NewRedisStoreWithSHAs(url, badSHA, badSHA)
	require.NoError(t, err)
	t.Cleanup(func() { _ = badStore.Close() })

	_, _, err = badStore.AtomicBackoffAllow(context.Background(), "rk-bad-allow-fmt")
	require.Error(t, err)
	require.ErrorContains(t, err, "unexpected result format")
}

// TestRedisStore_AtomicBackoffAllow_BadValueTypes_Integration covers the
// `!ok1 || !ok2` guard in AtomicBackoffAllow (lines 338-340) by injecting a
// Lua script that returns a two-element array of strings.
func TestRedisStore_AtomicBackoffAllow_BadValueTypes_Integration(t *testing.T) {
	t.Parallel()
	url := redisURL(t)
	badSHA := loadBadScript(t, url, `return {"foo", "bar"}`)

	badStore, err := kvstore.NewRedisStoreWithSHAs(url, badSHA, badSHA)
	require.NoError(t, err)
	t.Cleanup(func() { _ = badStore.Close() })

	_, _, err = badStore.AtomicBackoffAllow(context.Background(), "rk-bad-allow-types")
	require.Error(t, err)
	require.ErrorContains(t, err, "unexpected value types")
}

// ── Keys — SCAN pagination ──────────────────────────────────────────────────

// TestRedisStore_Keys_MultiCursorScan_Integration verifies that Keys correctly
// accumulates results across multiple SCAN cursor iterations. batchSize in
// redis.go is 100, so we must insert more than 100 keys with a shared prefix
// to force the cursor to be non-zero after the first SCAN call, exercising the
// loop-continuation branch.
func TestRedisStore_Keys_MultiCursorScan_Integration(t *testing.T) {
	// Not parallel: inserts many keys; cleanup is handled by t.Cleanup.
	s := newRedisStore(t)
	ctx := context.Background()

	const total = 150
	prefix := "scan:paginate:"
	var expected []string
	for i := range total {
		key := fmt.Sprintf("%s%d", prefix, i)
		expected = append(expected, key)
		flushTestKey(t, s, key)
		require.NoError(t, s.Set(ctx, key, "v", time.Minute))
	}

	keys, err := s.Keys(ctx, prefix)
	require.NoError(t, err)
	require.ElementsMatch(t, expected, keys,
		"all %d keys must be returned across multiple SCAN cursor pages", total)
}

// ── AtomicBackoffIncrement — zero idleTTL ─────────────────────────────────────

// TestRedisStore_AtomicBackoffIncrement_ZeroIdleTTL_Integration verifies that
// passing idleTTL = 0 skips the PEXPIRE call in the Lua script, so the key
// persists indefinitely and a second increment correctly sees failures = 2.
// This exercises the `if ttlMs > 0 then PEXPIRE` guard in
// atomicBackoffIncrementScript.
func TestRedisStore_AtomicBackoffIncrement_ZeroIdleTTL_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-backoff-zero-ttl"
	flushTestKey(t, s, key)
	ctx := context.Background()

	_, f1, err := s.AtomicBackoffIncrement(ctx, key, 50*time.Millisecond, time.Minute, 0)
	require.NoError(t, err)
	require.Equal(t, 1, f1)

	// A second increment must accumulate on top of the first entry, not start
	// fresh — proving the key was not expired by a (skipped) PEXPIRE.
	_, f2, err := s.AtomicBackoffIncrement(ctx, key, 50*time.Millisecond, time.Minute, 0)
	require.NoError(t, err)
	require.Equal(t, 2, f2, "key must persist without TTL so failure count accumulates")
}

// ── IsTokenBlocked — error propagation ───────────────────────────────────────

// TestRedisStore_IsTokenBlocked_ClosedClient_ReturnsError_Integration verifies
// that IsTokenBlocked propagates the underlying Exists error when the Redis
// client has been closed, confirming the error-wrapping chain end-to-end.
func TestRedisStore_IsTokenBlocked_ClosedClient_ReturnsError_Integration(t *testing.T) {
	// Not parallel: we intentionally close the store.
	s, err := kvstore.NewRedisStore(redisURL(t))
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = s.IsTokenBlocked(context.Background(), "jti-closed-exists")
	require.Error(t, err)
	require.ErrorContains(t, err, "kvstore.Exists")
}

// ── AtomicBackoffAllow — Lua edge branches in integration ─────────────────────

// TestRedisStore_AtomicBackoffAllow_ZeroFailures_Allowed_Integration verifies
// the Lua branch that returns {1, 0} when the failures field is 0, mirroring
// the in-memory test TestInMemoryStore_AtomicBackoffAllow_ZeroFailures_Allowed
// against real Redis.
func TestRedisStore_AtomicBackoffAllow_ZeroFailures_Allowed_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-allow-zero-failures"
	flushTestKey(t, s, key)
	ctx := context.Background()

	// Manually write a hash entry with failures=0 so Redis has the key but
	// the Lua script should treat it as fully unlocked.
	redisOpts, err := redis.ParseURL(redisURL(t))
	require.NoError(t, err)
	rc := redis.NewClient(redisOpts)
	defer rc.Close()
	require.NoError(t, rc.HSet(ctx, key, "failures", 0, "unlocks_at", 0, "last_seen", 0).Err())

	allowed, remaining, err := s.AtomicBackoffAllow(ctx, key)
	require.NoError(t, err)
	require.True(t, allowed, "zero failures must be treated as allowed")
	require.Zero(t, remaining)
}

// TestRedisStore_AtomicBackoffAllow_MissingUnlocksAt_Allowed_Integration
// verifies the Lua branch that returns {1, 0} when the unlocks_at field is
// absent from the hash, exercising the `if not unlocksAtMs then return {1,0}`
// guard in atomicBackoffAllowScript.
func TestRedisStore_AtomicBackoffAllow_MissingUnlocksAt_Allowed_Integration(t *testing.T) {
	t.Parallel()
	s := newRedisStore(t)
	key := "rk-allow-no-unlocks-at"
	flushTestKey(t, s, key)
	ctx := context.Background()

	// Write a hash with failures > 0 but no unlocks_at field.
	redisOpts, err := redis.ParseURL(redisURL(t))
	require.NoError(t, err)
	rc := redis.NewClient(redisOpts)
	defer rc.Close()
	require.NoError(t, rc.HSet(ctx, key, "failures", 3, "last_seen", 0).Err())

	allowed, remaining, err := s.AtomicBackoffAllow(ctx, key)
	require.NoError(t, err)
	require.True(t, allowed, "missing unlocks_at must be treated as allowed")
	require.Zero(t, remaining)
}

// ── Contract tests ────────────────────────────────────────────────────────────

// TestRedisStore_StoreContract_Integration runs the shared Store contract suite
// against RedisStore to catch semantic drift from InMemoryStore.
func TestRedisStore_StoreContract_Integration(t *testing.T) {
	t.Parallel()
	RunStoreContractTests(t, newRedisStore(t))
}

// TestRedisStore_TokenBlocklistContract_Integration runs the shared
// TokenBlocklist contract suite against RedisStore.
func TestRedisStore_TokenBlocklistContract_Integration(t *testing.T) {
	t.Parallel()
	RunTokenBlocklistContractTests(t, newRedisStore(t))
}

// TestRedisStore_AtomicBackoffContract_Integration runs the shared
// AtomicBackoffStore contract suite against RedisStore.
func TestRedisStore_AtomicBackoffContract_Integration(t *testing.T) {
	t.Parallel()
	RunAtomicBackoffContractTests(t, newRedisStore(t))
}
