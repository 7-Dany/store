package watch

// White-box service tests — package watch (not watch_test) so the test doubles
// can satisfy the unexported Storer interface defined in service.go without
// an export shim.
//
// NOTE: this file must NOT import bitcoinsharedtest (or any package that
// imports watch). Because this file is package watch, not watch_test, doing
// so would create the import cycle:
//
//	watch → bitcoinsharedtest → watch
//
// The fake storer is therefore defined locally below instead of reusing
// bitcoinsharedtest.WatchFakeStorer.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
)

// ── local fake storer ─────────────────────────────────────────────────────────

// fakeStorer is a hand-written implementation of Storer for service unit tests.
// Each method delegates to its Fn field when non-nil; otherwise it returns the
// zero value and nil error so tests only configure the fields they care about.
//
// Defined here (not in bitcoinsharedtest) to avoid the import cycle:
// service_test.go (package watch) → bitcoinsharedtest → watch.
type fakeStorer struct {
	RunWatchCapFn              func(ctx context.Context, userID string, limit int, addresses []string) (int64, int64, int64, error)
	IncrGlobalWatchCountFn     func(ctx context.Context) error
	PublishCacheInvalidationFn func(ctx context.Context, userID string) error
	ListWatchAddressKeysFn     func(ctx context.Context, cursor uint64, count int64) ([]string, uint64, error)
	GetWatchSetSizeFn          func(ctx context.Context, key string) (int64, error)
	WriteAuditLogFn            func(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

// compile-time check that *fakeStorer satisfies Storer.
var _ Storer = (*fakeStorer)(nil)

func (f *fakeStorer) RunWatchCap(ctx context.Context, userID string, limit int, addresses []string) (int64, int64, int64, error) {
	if f.RunWatchCapFn != nil {
		return f.RunWatchCapFn(ctx, userID, limit, addresses)
	}
	return 1, int64(len(addresses)), int64(len(addresses)), nil
}

func (f *fakeStorer) IncrGlobalWatchCount(ctx context.Context) error {
	if f.IncrGlobalWatchCountFn != nil {
		return f.IncrGlobalWatchCountFn(ctx)
	}
	return nil
}

func (f *fakeStorer) PublishCacheInvalidation(ctx context.Context, userID string) error {
	if f.PublishCacheInvalidationFn != nil {
		return f.PublishCacheInvalidationFn(ctx, userID)
	}
	return nil
}

func (f *fakeStorer) ListWatchAddressKeys(ctx context.Context, cursor uint64, count int64) ([]string, uint64, error) {
	if f.ListWatchAddressKeysFn != nil {
		return f.ListWatchAddressKeysFn(ctx, cursor, count)
	}
	return nil, 0, nil
}

func (f *fakeStorer) GetWatchSetSize(ctx context.Context, key string) (int64, error) {
	if f.GetWatchSetSizeFn != nil {
		return f.GetWatchSetSizeFn(ctx, key)
	}
	return 0, nil
}

func (f *fakeStorer) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	if f.WriteAuditLogFn != nil {
		return f.WriteAuditLogFn(ctx, event, userID, sourceIP, metadata)
	}
	return nil
}

// ── test helpers ──────────────────────────────────────────────────────────────

// newSvc builds a Service for unit tests and starts its reconciliation goroutine.
// The context is cancelled by t.Cleanup so the goroutine exits cleanly after
// each test.
func newSvc(t *testing.T, store Storer, rec bitcoinshared.BitcoinRecorder) *Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, store, rec, "testnet4", 100)
	svc.Start() // routes.go calls Start(); tests must do the same
	t.Cleanup(func() {
		cancel()
		svc.Shutdown()
	})
	return svc
}

// auditCapture is a helper that configures a fakeStorer to record every
// WriteAuditLog call. Returns the slice pointer; entries accumulate in-place.
type auditCapture struct {
	events    []audit.EventType
	userIDs   []string
	metadatas []map[string]string
	ctxs      []context.Context
}

func (c *auditCapture) install(store *fakeStorer) {
	store.WriteAuditLogFn = func(ctx context.Context, event audit.EventType, userID, _ string, metadata map[string]string) error {
		c.events = append(c.events, event)
		c.userIDs = append(c.userIDs, userID)
		c.metadatas = append(c.metadatas, metadata)
		c.ctxs = append(c.ctxs, ctx)
		return nil
	}
}

// ── T-01: Happy path — new addresses added ───────────────────────────────────

func TestWatch_HappyPath(t *testing.T) {
	t.Parallel()

	var incrCalled, publishCalled atomic.Bool
	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, addrs []string) (int64, int64, int64, error) {
			return 1, int64(len(addrs)), int64(len(addrs)), nil
		},
		IncrGlobalWatchCountFn: func(_ context.Context) error {
			incrCalled.Store(true)
			return nil
		},
		PublishCacheInvalidationFn: func(_ context.Context, _ string) error {
			publishCalled.Store(true)
			return nil
		},
	}
	cap := &auditCapture{}
	cap.install(store)
	svc := newSvc(t, store, bitcoinshared.NoopBitcoinRecorder{})

	result, err := svc.Watch(context.Background(), WatchInput{
		UserID: "user-1", Addresses: []string{"tb1qtest"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"tb1qtest"}, result.Watching)
	assert.True(t, incrCalled.Load(), "global counter should be incremented")
	assert.True(t, publishCalled.Load(), "cache invalidation should be published")
	require.Len(t, cap.events, 1)
	assert.Equal(t, audit.EventBitcoinAddressWatched, cap.events[0])
}

// ── T-02: Re-registration — no side-effects ──────────────────────────────────

func TestWatch_Reregistration_NoCacheInvalidation(t *testing.T) {
	t.Parallel()

	var incrCalled, publishCalled atomic.Bool
	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, addrs []string) (int64, int64, int64, error) {
			return 1, int64(len(addrs)), 0, nil // added_count == 0
		},
		IncrGlobalWatchCountFn: func(_ context.Context) error {
			incrCalled.Store(true)
			return nil
		},
		PublishCacheInvalidationFn: func(_ context.Context, _ string) error {
			publishCalled.Store(true)
			return nil
		},
	}
	cap := &auditCapture{}
	cap.install(store)
	svc := newSvc(t, store, bitcoinshared.NoopBitcoinRecorder{})

	result, err := svc.Watch(context.Background(), WatchInput{
		UserID: "user-2", Addresses: []string{"tb1qtest"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"tb1qtest"}, result.Watching)
	assert.False(t, incrCalled.Load(), "counter must NOT be incremented on re-registration")
	assert.False(t, publishCalled.Load(), "invalidation must NOT be published on re-registration")
	assert.Empty(t, cap.events, "no audit event must be written on re-registration")
}

// ── T-03: Count cap hit ───────────────────────────────────────────────────────

func TestWatch_CountCapHit(t *testing.T) {
	t.Parallel()

	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, _ []string) (int64, int64, int64, error) {
			return 0, 100, 0, nil
		},
	}
	cap := &auditCapture{}
	cap.install(store)
	svc := newSvc(t, store, bitcoinshared.NoopBitcoinRecorder{})

	_, err := svc.Watch(context.Background(), WatchInput{
		UserID: "user-3", Addresses: []string{"tb1qtest"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.True(t, errors.Is(err, bitcoinshared.ErrWatchLimitExceeded))
	require.Len(t, cap.events, 1)
	assert.Equal(t, audit.EventBitcoinWatchLimitExceeded, cap.events[0])
	assert.Equal(t, "count_cap", cap.metadatas[0]["reason"])
}

// ── T-04: Registration window expired ────────────────────────────────────────

func TestWatch_RegistrationWindowExpired(t *testing.T) {
	t.Parallel()

	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, _ []string) (int64, int64, int64, error) {
			return -1, 0, 0, nil
		},
	}
	cap := &auditCapture{}
	cap.install(store)
	svc := newSvc(t, store, bitcoinshared.NoopBitcoinRecorder{})

	_, err := svc.Watch(context.Background(), WatchInput{
		UserID: "user-4", Addresses: []string{"tb1qtest"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.True(t, errors.Is(err, bitcoinshared.ErrWatchRegistrationExpired))
	require.Len(t, cap.events, 1)
	assert.Equal(t, "registration_window_expired", cap.metadatas[0]["reason"])
}

// ── T-05: Redis unavailable ───────────────────────────────────────────────────

func TestWatch_RedisUnavailable(t *testing.T) {
	t.Parallel()

	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, _ []string) (int64, int64, int64, error) {
			return 0, 0, 0, errors.New("connection refused")
		},
	}
	svc := newSvc(t, store, bitcoinshared.NoopBitcoinRecorder{})

	_, err := svc.Watch(context.Background(), WatchInput{
		UserID: "user-5", Addresses: []string{"tb1qtest"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.True(t, errors.Is(err, bitcoinshared.ErrRedisUnavailable))
}

// ── T-06 & T-07: non-fatal side-effect failures ───────────────────────────────

func TestWatch_IncrError_NonFatal(t *testing.T) {
	t.Parallel()

	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, addrs []string) (int64, int64, int64, error) {
			return 1, int64(len(addrs)), int64(len(addrs)), nil
		},
		IncrGlobalWatchCountFn: func(_ context.Context) error {
			return errors.New("redis write error")
		},
	}
	svc := newSvc(t, store, bitcoinshared.NoopBitcoinRecorder{})

	result, err := svc.Watch(context.Background(), WatchInput{
		UserID: "user-6", Addresses: []string{"tb1qtest"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"tb1qtest"}, result.Watching)
}

func TestWatch_PublishError_NonFatal(t *testing.T) {
	t.Parallel()

	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, addrs []string) (int64, int64, int64, error) {
			return 1, int64(len(addrs)), int64(len(addrs)), nil
		},
		PublishCacheInvalidationFn: func(_ context.Context, _ string) error {
			return errors.New("pubsub error")
		},
	}
	svc := newSvc(t, store, bitcoinshared.NoopBitcoinRecorder{})

	result, err := svc.Watch(context.Background(), WatchInput{
		UserID: "user-7", Addresses: []string{"tb1qtest"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"tb1qtest"}, result.Watching)
}

// ── T-08: context.WithoutCancel on audit write (success path) ─────────────────

func TestWatch_AuditWriteUsesWithoutCancel(t *testing.T) {
	t.Parallel()

	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, addrs []string) (int64, int64, int64, error) {
			return 1, int64(len(addrs)), int64(len(addrs)), nil
		},
	}

	var capturedCtx context.Context
	store.WriteAuditLogFn = func(ctx context.Context, _ audit.EventType, _, _ string, _ map[string]string) error {
		capturedCtx = ctx
		return nil
	}

	// Build a service with an already-cancelled context so any non-WithoutCancel
	// ctx would have Done() != nil.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := NewService(cancelledCtx, store, bitcoinshared.NoopBitcoinRecorder{}, "testnet4", 100)
	svc.Start() // goroutine exits immediately (ctx already cancelled)
	defer svc.Shutdown()

	_, _ = svc.Watch(context.Background(), WatchInput{
		UserID: "user-8", Addresses: []string{"tb1qtest"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.NotNil(t, capturedCtx)
	// context.WithoutCancel produces a context whose Done() channel is nil,
	// confirming that cancellation of the parent cannot abort the audit write.
	assert.Nil(t, capturedCtx.Done(), "audit ctx.Done() must be nil — WithoutCancel required")
}

// ── T-09: Both IncrGlobalWatchCount and PublishCacheInvalidation fail simultaneously ──
//
// Verifies the non-fatal contract: even when both side-effect calls fail, Watch
// returns a successful WatchResult with the correct Watching slice.

func TestWatch_BothSideEffectsFail_NonFatal(t *testing.T) {
	t.Parallel()

	store := &fakeStorer{
		RunWatchCapFn: func(_ context.Context, _ string, _ int, addrs []string) (int64, int64, int64, error) {
			return 1, int64(len(addrs)), int64(len(addrs)), nil
		},
		IncrGlobalWatchCountFn: func(_ context.Context) error {
			return errors.New("redis unavailable")
		},
		PublishCacheInvalidationFn: func(_ context.Context, _ string) error {
			return errors.New("pubsub unavailable")
		},
	}
	svc := newSvc(t, store, bitcoinshared.NoopBitcoinRecorder{})

	result, err := svc.Watch(context.Background(), WatchInput{
		UserID: "user-9", Addresses: []string{"tb1qaddr1", "tb1qaddr2"}, Network: "testnet4", SourceIP: "1.2.3.4",
	})

	require.NoError(t, err, "both side-effect failures must not abort the request")
	assert.Equal(t, []string{"tb1qaddr1", "tb1qaddr2"}, result.Watching,
		"Watching must echo the submitted addresses")
}

// ── T-10: reconciliation goroutine exits cleanly ─────────────────────────────

func TestWatch_ReconciliationGoroutine_ExitsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeStorer{
		ListWatchAddressKeysFn: func(_ context.Context, _ uint64, _ int64) ([]string, uint64, error) {
			return nil, 0, nil
		},
	}
	svc := NewService(ctx, store, bitcoinshared.NoopBitcoinRecorder{}, "testnet4", 100)
	svc.Start()

	cancel()

	done := make(chan struct{})
	go func() {
		svc.Shutdown()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown() did not return within 5s")
	}
}

// ── T-11: scanAndSumWatchKeys — multi-page SCAN cursor ───────────────────────
//
// Verifies that scanAndSumWatchKeys correctly iterates across multiple SCAN
// pages (non-zero cursor on first call) and accumulates totals from both pages.

func TestWatch_ScanAndSum_MultiPage(t *testing.T) {
	t.Parallel()

	callCount := 0
	store := &fakeStorer{
		ListWatchAddressKeysFn: func(_ context.Context, cursor uint64, _ int64) ([]string, uint64, error) {
			callCount++
			switch callCount {
			case 1:
				// First page: return one key and a non-zero cursor indicating more pages.
				return []string{"{btc:user:a}:addresses"}, 42, nil
			case 2:
				// Second page: return one key and cursor=0 indicating end of iteration.
				return []string{"{btc:user:b}:addresses"}, 0, nil
			default:
				return nil, 0, errors.New("unexpected ListWatchAddressKeys call")
			}
		},
		GetWatchSetSizeFn: func(_ context.Context, _ string) (int64, error) {
			return 7, nil // each key has 7 members
		},
	}

	// Create a minimal Service without starting the goroutine — call
	// scanAndSumWatchKeys directly to test the method in isolation.
	svc := &Service{store: store, rec: bitcoinshared.NoopBitcoinRecorder{}}
	total, err := svc.scanAndSumWatchKeys(context.Background())

	require.NoError(t, err)
	assert.Equal(t, int64(14), total, "should sum both pages: 7 + 7 = 14")
	assert.Equal(t, 2, callCount, "should have scanned exactly two pages")
}

// ── T-12: scanAndSumWatchKeys — GetWatchSetSize non-cancel error skipped ─────
//
// Verifies that a non-context.Canceled error from GetWatchSetSize is logged and
// skipped rather than aborting the reconciliation, so one bad key does not
// invalidate the entire scan.

func TestWatch_ScanAndSum_GetWatchSetSizeError_Skipped(t *testing.T) {
	t.Parallel()

	store := &fakeStorer{
		ListWatchAddressKeysFn: func(_ context.Context, _ uint64, _ int64) ([]string, uint64, error) {
			return []string{
				"{btc:user:good}:addresses",
				"{btc:user:bad}:addresses",
			}, 0, nil
		},
		GetWatchSetSizeFn: func(_ context.Context, key string) (int64, error) {
			if key == "{btc:user:bad}:addresses" {
				return 0, errors.New("redis WRONGTYPE error")
			}
			return 5, nil
		},
	}

	svc := &Service{store: store, rec: bitcoinshared.NoopBitcoinRecorder{}}
	total, err := svc.scanAndSumWatchKeys(context.Background())

	require.NoError(t, err, "a non-cancel GetWatchSetSize error must not abort scanAndSumWatchKeys")
	assert.Equal(t, int64(5), total, "bad key is skipped; only good key's count included")
}
