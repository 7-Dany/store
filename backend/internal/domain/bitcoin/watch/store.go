package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// watchSetTTL is the Redis SET TTL for the watch address set.
// Set atomically inside the Lua script when at least one new address is added.
// Both watchSetTTL and lastActiveTTL are intentionally equal — they represent
// the same inactivity window: a user's data is eligible for eviction after
// 30 minutes of no watch activity.
const watchSetTTL = 30 * time.Minute

// lastActiveTTL is the Redis TTL for the last_active key.
// Refreshed on every POST /watch call (even re-registration of existing
// addresses). See watchSetTTL comment — both share the same duration by design.
const lastActiveTTL = 30 * time.Minute

// Store is the data layer for the watch feature.
//
// Redis operations use the WatchCapStore and related interfaces.
// Audit writes use pool to call InsertAuditLog directly — the bitcoin domain
// has no SQL queries of its own, so audit rows go through the shared audit table.
type Store struct {
	kv      kvstore.WatchCapStore
	sets    kvstore.SetStore // for GetWatchSetSize in the reconciliation goroutine
	counter kvstore.AtomicCounterStore
	pubsub  kvstore.PubSubStore
	pool    *pgxpool.Pool
	q       *db.Queries // pre-allocated; nil when pool is nil (unit-test stores)
	network string      // "testnet4" or "mainnet"; used as a metric label
}

// NewStore constructs a Store.
//
// kv must implement kvstore.WatchCapStore (satisfied by *kvstore.RedisStore).
// sets must implement kvstore.SetStore (same RedisStore instance) — used by
// GetWatchSetSize in the reconciliation goroutine.
// counter must implement kvstore.AtomicCounterStore (same RedisStore instance).
// pubsub must implement kvstore.PubSubStore (same RedisStore instance).
// pool is the shared connection pool used for audit-log writes. Pass nil in
// unit tests that do not exercise WriteAuditLog.
//
// In routes.go all four kvstore interfaces are satisfied by separate type
// assertions on the single deps.KVStore value, which is a *kvstore.RedisStore
// at runtime.
func NewStore(kv kvstore.WatchCapStore, sets kvstore.SetStore, counter kvstore.AtomicCounterStore, pubsub kvstore.PubSubStore, pool *pgxpool.Pool, network string) *Store {
	var q *db.Queries
	if pool != nil {
		q = db.New(pool)
	}
	return &Store{
		kv:      kv,
		sets:    sets,
		counter: counter,
		pubsub:  pubsub,
		pool:    pool,
		q:       q,
		network: network,
	}
}

// ── Redis key helpers ─────────────────────────────────────────────────────────

// setKey returns the Redis key for the user's watch address SET.
// Uses the hash tag {btc:user:{userID}} so all three user-scoped keys land
// on the same Redis Cluster slot.
func setKey(userID string) string {
	return fmt.Sprintf("{btc:user:%s}:addresses", userID)
}

// regAtKey returns the Redis key for the user's first-registration timestamp.
// Set once (NX) and never refreshed — anchors the 7-day registration window.
func regAtKey(userID string) string {
	return fmt.Sprintf("{btc:user:%s}:registered_at", userID)
}

// lastActiveKey returns the Redis key for the user's last-activity timestamp.
// Refreshed on every POST /watch call, including re-registration of existing addresses.
func lastActiveKey(userID string) string {
	return fmt.Sprintf("{btc:user:%s}:last_active", userID)
}

// globalCountKey is the cross-instance advisory watch address counter.
// Incremented when at least one new address is added; reconciled every
// reconciliationInterval. TTL is permanent (0) — see note below.
//
// Advisory counter note: this key has TTL=0 (no expiry). If a FLUSHDB,
// keyspace eviction, or Redis restart removes it, subsequent INCR calls
// restart from zero. Any upstream logic reading this counter will see a
// falsely low value for up to reconciliationInterval (15 min) until the
// reconciliation goroutine rewrites the correct total. The counter is
// intentionally advisory — callers must tolerate transient undercount.
const globalCountKey = "btc:global:watch_count"

// invalidationChannel returns the pub/sub channel name for cache invalidation.
func invalidationChannel(userID string) string {
	return fmt.Sprintf("btc:watch:invalidate:%s", userID)
}

// ── Storer methods ────────────────────────────────────────────────────────────

// RunWatchCap atomically enforces the per-user cap and 7-day window.
// Returns (success, newCount, addedCount) directly from the Lua script.
func (s *Store) RunWatchCap(ctx context.Context, userID string, limit int, addresses []string) (success, newCount, addedCount int64, err error) {
	success, newCount, addedCount, err = s.kv.RunWatchCapScript(
		ctx,
		setKey(userID),
		regAtKey(userID),
		lastActiveKey(userID),
		limit,
		watchSetTTL,
		lastActiveTTL,
		addresses,
	)
	if err != nil {
		return 0, 0, 0, telemetry.KVStore("RunWatchCap.lua_eval", err)
	}
	return success, newCount, addedCount, nil
}

// IncrGlobalWatchCount increments the cross-instance advisory counter by 1.
// Errors are non-fatal — counter drift is corrected by the reconciliation goroutine.
func (s *Store) IncrGlobalWatchCount(ctx context.Context) error {
	// TTL=0 → permanent key (never expires — the reconciler resets it periodically).
	if _, err := s.counter.AtomicIncrement(ctx, globalCountKey, 0); err != nil {
		return telemetry.KVStore("IncrGlobalWatchCount.incr", err)
	}
	return nil
}

// PublishCacheInvalidation publishes a cache invalidation event so all instances
// evict their local in-memory watch cache for this user on the next event.
// Errors are non-fatal — subscribers will reload from Redis at next cache miss.
func (s *Store) PublishCacheInvalidation(ctx context.Context, userID string) error {
	if _, err := s.pubsub.Publish(ctx, invalidationChannel(userID), "1"); err != nil {
		return telemetry.KVStore("PublishCacheInvalidation.publish", err)
	}
	return nil
}

// ListWatchAddressKeys returns one page of watch-address SET keys for the
// reconciliation goroutine. Pass cursor=0 to start; iteration is complete
// when nextCursor returns 0.
//
// Named List* per RULES.md §2.3 (paginated read). The underlying Redis
// operation is SCAN TYPE set.
func (s *Store) ListWatchAddressKeys(ctx context.Context, cursor uint64, count int64) (keys []string, nextCursor uint64, err error) {
	keys, nextCursor, err = s.kv.ScanWatchAddressKeys(ctx, cursor, count)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, 0, err
		}
		return nil, 0, telemetry.KVStore("ListWatchAddressKeys.scan_type", err)
	}
	return keys, nextCursor, nil
}

// GetWatchSetSize returns the number of members in the given watch SET key.
// Used by the reconciliation goroutine to sum up the global count.
//
// Named Get* per RULES.md §2.3 (single-value read). The underlying Redis
// operation is SCARD.
func (s *Store) GetWatchSetSize(ctx context.Context, key string) (int64, error) {
	n, err := s.sets.SCard(ctx, key)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, err
		}
		return 0, telemetry.KVStore("GetWatchSetSize.scard", err)
	}
	return n, nil
}

// WriteAuditLog inserts one row into auth_audit_log.
// The bitcoin domain has no SQL store of its own; audit rows go through the
// shared auth_audit_log table via the existing InsertAuditLog query.
// Errors are non-fatal — audit write failures never fail the primary operation.
//
// userID may be empty (anonymous path, e.g. rate-limit hit before JWT auth).
// When empty, pgUserID is set to {Valid: false} which inserts NULL into the
// user_id column. The auth_audit_log.user_id column must therefore be nullable.
//
// Provider is set to db.AuthProviderEmail as a neutral sentinel value — the
// bitcoin domain has no auth-provider concept. Audit queries that filter on
// provider='email' will erroneously include these rows; if that becomes
// problematic, introduce a db.AuthProviderSystem constant.
func (s *Store) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	if s.q == nil {
		return fmt.Errorf("watch.WriteAuditLog: no database pool configured")
	}

	var pgUserID pgtype.UUID
	if userID != "" {
		uid, err := uuid.Parse(userID)
		if err == nil {
			pgUserID = pgtype.UUID{Bytes: [16]byte(uid), Valid: true}
		}
	}

	var ipAddr *netip.Addr
	if sourceIP != "" {
		if parsed, err := netip.ParseAddr(sourceIP); err == nil {
			ipAddr = &parsed
		}
	}

	metaBytes := []byte("{}")
	if len(metadata) > 0 {
		if b, err := json.Marshal(metadata); err == nil {
			metaBytes = b
		} else {
			// json.Marshal on map[string]string should never fail in practice,
			// but log a warning if it does so the silent fall-back is observable.
			log.Warn(ctx, "watch.WriteAuditLog: failed to marshal metadata; using {}",
				"error", err)
		}
	}

	return s.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    pgUserID,
		EventType: string(event),
		Provider:  db.AuthProviderEmail, // neutral value; bitcoin has no auth_provider concept
		IpAddress: ipAddr,
		UserAgent: pgtype.Text{}, // no user-agent in bitcoin domain audit events
		Metadata:  metaBytes,
	})
}
