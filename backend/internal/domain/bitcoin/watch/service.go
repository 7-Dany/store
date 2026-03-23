package watch

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// var log is the package-level logger for the watch feature.
var log = telemetry.New("watch")

// reconciliationInterval is how often the background goroutine recomputes
// btc:global:watch_count from a full SCAN of all watch-address SET keys.
// Exposed as a named constant so it appears in searches and can be tuned
// without grepping for magic literals.
const reconciliationInterval = 15 * time.Minute

// scanPageSleep is a brief pause inserted between SCAN pages during the
// reconciliation goroutine to spread Redis load across time and avoid a
// burst of ~1 M SCARD commands in a tight loop at scale.
const scanPageSleep = 5 * time.Millisecond

// ── Storer interface ──────────────────────────────────────────────────────────

// Storer is the data-access contract for the watch service.
type Storer interface {
	// RunWatchCap atomically checks the 7-day window and per-user cap, then
	// SADDs the addresses. Returns (success, newCount, addedCount) from the
	// Lua script.
	//   success ==  1: completed (addedCount may be 0)
	//   success ==  0: count cap exceeded; set unchanged
	//   success == -1: 7-day registration window expired
	RunWatchCap(ctx context.Context, userID string, limit int, addresses []string) (success, newCount, addedCount int64, err error)

	// IncrGlobalWatchCount increments the cross-instance advisory counter.
	// Non-fatal — counter drift is corrected by the reconciliation goroutine.
	IncrGlobalWatchCount(ctx context.Context) error

	// PublishCacheInvalidation publishes a cache-eviction signal so all
	// instances drop their local watch cache for this user.
	PublishCacheInvalidation(ctx context.Context, userID string) error

	// ListWatchAddressKeys returns one page of watch-address SET keys for
	// the reconciliation goroutine.
	//
	// Naming note: Redis-layer scan operations use the verb "List" rather than
	// "Scan" at the Storer boundary to stay consistent with the RULES.md §2.3
	// read-method naming convention (List* for paginated reads).
	ListWatchAddressKeys(ctx context.Context, cursor uint64, count int64) (keys []string, nextCursor uint64, err error)

	// GetWatchSetSize returns the member count of the given watch SET key.
	//
	// Naming note: uses "Get" prefix per RULES.md §2.3 for single-value reads,
	// rather than the raw Redis verb "SCard".
	GetWatchSetSize(ctx context.Context, key string) (int64, error)

	// WriteAuditLog inserts one row into auth_audit_log.
	// Errors are non-fatal — audit write failures never fail the primary operation.
	WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service implements the business logic for POST /bitcoin/watch.
type Service struct {
	store   Storer
	rec     bitcoinshared.BitcoinRecorder
	network string // "testnet4" or "mainnet"
	limit   int    // BTC_MAX_WATCH_PER_USER

	// Lifecycle fields for the reconciliation goroutine.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewService constructs a Service. It does NOT start the reconciliation
// goroutine — call Start() immediately after construction (done in routes.go).
//
// ctx must be the application root context — the goroutine exits when it is
// cancelled.
// limit is BTC_MAX_WATCH_PER_USER from config.
// rec satisfies BitcoinRecorder — pass deps.Metrics.
func NewService(ctx context.Context, store Storer, rec bitcoinshared.BitcoinRecorder, network string, limit int) *Service {
	svcCtx, cancel := context.WithCancel(ctx)
	return &Service{
		store:   store,
		rec:     rec,
		network: network,
		limit:   limit,
		ctx:     svcCtx,
		cancel:  cancel,
	}
}

// Start begins the background reconciliation goroutine.
// Must be called exactly once after NewService, from routes.go or a test helper.
// Shutdown stops the goroutine gracefully and waits for it to exit.
func (s *Service) Start() {
	// C-01: wg.Add BEFORE go func() in the calling frame so that a concurrent
	// Shutdown/wg.Wait cannot return before the goroutine has called wg.Done().
	s.wg.Add(1)
	go s.reconcileGlobalWatchCount()
}

// Shutdown signals the reconciliation goroutine to stop and waits for it to exit.
// Called by the bitcoin domain assembler during graceful shutdown.
func (s *Service) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// WriteAuditLog delegates to the store so the handler layer can write audit
// rows through the Servicer interface without importing the store directly.
// This is a layer-compliance delegation: the handler must not call store methods
// directly (RULES.md §2.1), so audit writes are routed through the service even
// though no business logic is applied here.
func (s *Service) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	return s.store.WriteAuditLog(ctx, event, userID, sourceIP, metadata)
}

// ── Watch ─────────────────────────────────────────────────────────────────────

// Watch registers the given addresses in the user's Redis watch set, enforcing
// the per-user count cap and 7-day registration window.
//
// The addresses in in.Addresses must already be normalised (trimmed +
// selectively lowercased) by the handler before this method is called.
//
// Re-registration semantics: submitting addresses that are already registered is
// a no-op — no audit event, no counter increment, no cache invalidation. Only
// newly added addresses trigger side-effects.
//
// On success (added_count > 0) the following run atomically from the caller's
// perspective (they are NOT in a single Redis transaction — they are independent
// calls; partial failure is logged but does not fail the request):
//  1. Increment cross-instance global watch counter
//  2. Publish cache invalidation event
//  3. Write EventBitcoinAddressWatched audit row
func (s *Service) Watch(ctx context.Context, in WatchInput) (WatchResult, error) {
	success, _, addedCount, err := s.store.RunWatchCap(ctx, in.UserID, s.limit, in.Addresses)
	if err != nil {
		return WatchResult{}, bitcoinshared.ErrRedisUnavailable
	}

	switch success {
	case 0:
		// Security: WithoutCancel so a client disconnect cannot abort the audit
		// write for a cap-exceeded event.
		auditCtx := context.WithoutCancel(ctx)
		_ = s.store.WriteAuditLog(auditCtx, audit.EventBitcoinWatchLimitExceeded, in.UserID, in.SourceIP,
			map[string]string{"reason": "count_cap"})
		return WatchResult{}, bitcoinshared.ErrWatchLimitExceeded

	case -1:
		// Security: WithoutCancel so a client disconnect cannot abort the audit
		// write for a window-expired event.
		auditCtx := context.WithoutCancel(ctx)
		_ = s.store.WriteAuditLog(auditCtx, audit.EventBitcoinWatchLimitExceeded, in.UserID, in.SourceIP,
			map[string]string{"reason": "registration_window_expired"})
		return WatchResult{}, bitcoinshared.ErrWatchRegistrationExpired
	}

	// success == 1 — Lua completed.
	// Re-registration (addedCount == 0): return success silently with no side-effects.
	if addedCount > 0 {
		// Non-fatal side-effects: log errors but do not fail the request.

		// Security: WithoutCancel so a client disconnect cannot abort the
		// global watch counter increment.
		incrCtx := context.WithoutCancel(ctx)
		if err := s.store.IncrGlobalWatchCount(incrCtx); err != nil {
			log.Warn(incrCtx, "watch: failed to increment global watch count", "error", err)
		}

		// Security: WithoutCancel so a client disconnect cannot abort the
		// cache invalidation publish.
		pubCtx := context.WithoutCancel(ctx)
		if err := s.store.PublishCacheInvalidation(pubCtx, in.UserID); err != nil {
			log.Warn(pubCtx, "watch: failed to publish cache invalidation", "error", err)
		}

		// Security: WithoutCancel so a client disconnect cannot abort the audit write.
		auditCtx := context.WithoutCancel(ctx)
		_ = s.store.WriteAuditLog(auditCtx, audit.EventBitcoinAddressWatched, in.UserID, in.SourceIP,
			map[string]string{"added_count": strconv.FormatInt(addedCount, 10)})
	}

	return WatchResult{Watching: in.Addresses}, nil
}

// ── Reconciliation goroutine ──────────────────────────────────────────────────

// reconcileGlobalWatchCount recomputes the cross-instance advisory global watch
// count by scanning all *:addresses SET keys every reconciliationInterval and
// writing the result back to btc:global:watch_count. This corrects any drift
// from missed decrements (e.g. expiry without a corresponding counter decrement).
//
// The goroutine exits when s.ctx is cancelled (via Shutdown).
func (s *Service) reconcileGlobalWatchCount() {
	defer s.wg.Done()

	ticker := time.NewTicker(reconciliationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Recover from any panic in the scan/sum helpers so a transient
			// kvstore panic cannot crash the process.
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Error(s.ctx, "watch: reconciliation goroutine panic (recovered)",
							"panic", r)
					}
				}()

				total, err := s.scanAndSumWatchKeys(s.ctx)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return
					}
					log.Error(s.ctx, "watch: global watch count reconciliation failed", "error", err)
					return
				}
				s.rec.SetGlobalWatchCountEstimate(s.network, float64(total))
			}()

		case <-s.ctx.Done():
			return
		}
	}
}

// scanAndSumWatchKeys iterates all watch-address SET keys via SCAN and returns
// the total member count. Partial failures on individual GetWatchSetSize calls
// are logged and skipped so one bad key doesn't invalidate the whole scan.
//
// A scanPageSleep pause is inserted between SCAN pages to spread Redis load
// and avoid bursting ~1 M SCARD commands in a tight loop at scale (PERF-1).
func (s *Service) scanAndSumWatchKeys(ctx context.Context) (int64, error) {
	var total int64
	var cursor uint64
	for {
		keys, nextCursor, err := s.store.ListWatchAddressKeys(ctx, cursor, 100)
		if err != nil {
			return 0, telemetry.Service("scanAndSumWatchKeys.scan", err)
		}
		for _, key := range keys {
			// Check for cancellation inside the inner loop so that shutdown is
			// not delayed by the full set of GetWatchSetSize round-trips on a
			// large key page.
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			default:
			}

			n, err := s.store.GetWatchSetSize(ctx, key)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return 0, err
				}
				log.Warn(ctx, "watch: reconcile: GetWatchSetSize failed; skipping key",
					"key", key, "error", err)
				continue
			}
			total += n
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
		// Sleep between SCAN pages to prevent a Redis SCARD storm at scale,
		// then check for cancellation before the next page fetch.
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(scanPageSleep):
		}
	}
	return total, nil
}
