// Package worker provides background job runners that operate independently of
// the HTTP layer. Currently contains the account purge worker (D-16).
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── PurgeHandler ──────────────────────────────────────────────────────────────

// PurgeHandler hard-deletes user accounts whose 30-day grace period has expired.
// It implements the worker.Handler interface so that job queue Phase 7 can drive
// it via the Dispatcher without any handler refactoring (D-21).
//
// Core logic lives here. The PurgeWorker goroutine below is the only temporary
// piece — it is removed in job queue Phase 7 and replaced by the
// purge_accounts_hourly scheduled job.
type PurgeHandler struct {
	pool *pgxpool.Pool
}

// compile-time check: PurgeHandler must satisfy the local Handler interface.
// TODO(jobqueue-phase-6): swap to `var _ jobqueue.Handler = (*PurgeHandler)(nil)`
// once internal/platform/jobqueue exists. The local worker.Handler uses `payload any`;
// jobqueue.Handler uses `job jobqueue.Job`. Update the Handle signature at the same time:
//   func (h *PurgeHandler) Handle(ctx context.Context, _ jobqueue.Job) error
var _ Handler = (*PurgeHandler)(nil)

// NewPurgeHandler constructs a PurgeHandler backed by pool.
func NewPurgeHandler(pool *pgxpool.Pool) *PurgeHandler {
	return &PurgeHandler{pool: pool}
}

// Handle purges all accounts whose 30-day grace period has expired.
// It loops internally, fetching up to 100 rows per iteration, until the batch
// is exhausted (fewer than 100 rows returned) — then returns nil.
//
// payload is unused; PurgeHandler requires no per-job data.
//
// Failure on a single account is logged and skipped — subsequent accounts in
// the same run are still processed (D-14, D-16).
// TODO(jobqueue-phase-6): change `_ any` to `_ jobqueue.Job` when jobqueue package exists.
func (h *PurgeHandler) Handle(ctx context.Context, _ any) error {
	for {
		ids, err := h.getAccountsDueForPurge(ctx)
		if err != nil {
			return fmt.Errorf("purge.Handle: fetch batch: %w", err)
		}

		if len(ids) > 0 {
			slog.InfoContext(ctx, "purge: processing accounts", "count", len(ids))
		}

		for _, id := range ids {
			if err := h.purgeOne(ctx, id); err != nil {
				// Log and continue — one bad purge must not block the rest.
				slog.ErrorContext(ctx, "purge: failed to purge account",
					"user_id", id.String(),
					"error", err,
				)
			}
		}

		if len(ids) < 100 {
			break // batch exhausted
		}
	}
	return nil
}

// getAccountsDueForPurge fetches up to 100 user IDs whose grace period has expired.
func (h *PurgeHandler) getAccountsDueForPurge(ctx context.Context) ([]uuid.UUID, error) {
	return db.New(h.pool).GetAccountsDueForPurge(ctx)
}

// purgeOne hard-deletes a single user inside its own transaction.
// InsertPurgeLog is written BEFORE HardDeleteUser so that a log row always
// exists even if the DELETE fails (D-14). account_purge_log has no FK to users
// so the log row is durable after the user row is removed (D-15).
func (h *PurgeHandler) purgeOne(ctx context.Context, id uuid.UUID) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin tx for %s: %w", id, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on non-committed tx is always safe

	q := db.New(tx)
	pgID := pgtype.UUID{Bytes: id, Valid: true}

	meta, err := json.Marshal(map[string]string{
		"purged_at": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("purge: marshal metadata for %s: %w", id, err)
	}

	// 1. Write the purge log BEFORE deleting the user row (D-14).
	if err := q.InsertPurgeLog(ctx, db.InsertPurgeLogParams{
		UserID:   pgID,
		Metadata: meta,
	}); err != nil {
		return fmt.Errorf("purge: insert purge log for %s: %w", id, err)
	}

	// 2. Hard-delete the user. All child rows are removed via CASCADE.
	if err := q.HardDeleteUser(ctx, pgID); err != nil {
		return fmt.Errorf("purge: hard delete user %s: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit tx for %s: %w", id, err)
	}

	slog.InfoContext(ctx, "purge: account purged", "user_id", id.String())
	return nil
}

// ── PurgeWorker ───────────────────────────────────────────────────────────────

// PurgeWorker is a thin goroutine wrapper around PurgeHandler.
// It ticks every interval and calls Handle until ctx is cancelled.
//
// REMOVED in job queue Phase 7. The sole change at that point is:
//   - Remove the `go worker.NewPurgeWorker(pool, time.Hour).Start(ctx)` call from server.go
//   - Add:
//       d.Register(KindPurgeAccounts, NewPurgeHandler(pool))
//       s.Add(ScheduleEntry{Kind: KindPurgeAccounts, Interval: time.Hour, ...})
type PurgeWorker struct {
	handler  *PurgeHandler
	interval time.Duration
}

// NewPurgeWorker constructs a PurgeWorker backed by pool.
// interval controls how often Handle is called; use time.Hour for production.
func NewPurgeWorker(pool *pgxpool.Pool, interval time.Duration) *PurgeWorker {
	return &PurgeWorker{
		handler:  NewPurgeHandler(pool),
		interval: interval,
	}
}

// Start runs the purge loop until ctx is cancelled. Launch as a goroutine:
//
//	go worker.NewPurgeWorker(pool, time.Hour).Start(ctx)
func (w *PurgeWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	slog.InfoContext(ctx, "purge worker started", "interval", w.interval)

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "purge worker stopped")
			return
		case <-ticker.C:
			if err := w.handler.Handle(ctx, nil); err != nil {
				slog.ErrorContext(ctx, "purge worker error", "error", err)
			}
		}
	}
}
