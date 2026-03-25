package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data layer for the events feature.
//
// kv is the primary Redis client. It must satisfy kvstore.OnceStore because
// ConsumeJTI uses OnceStore.ConsumeOnce; OnceStore embeds Store so it also
// covers Set/Get/Delete for session-SID operations. At runtime this is the
// same *kvstore.RedisStore used by all other bitcoin sub-packages.
//
// sets is the same Redis client cast to kvstore.SetStore, used by
// GetUserWatchAddresses to SScan the watch address set for a user.
//
// pool is used for non-fatal DB writes (InsertSSETokenIssuance). Pass nil in
// unit tests that do not exercise RecordTokenIssuance.
type Store struct {
	kv   kvstore.OnceStore
	sets kvstore.SetStore
	pool *pgxpool.Pool
	q    *db.Queries // pre-allocated from pool; nil when pool is nil
}

// NewStore constructs a Store.
//
// kv must satisfy kvstore.OnceStore (implemented by *kvstore.RedisStore).
// In routes.go: deps.KVStore.(kvstore.OnceStore) — panics at startup if Redis
// is not configured, same pattern as bitcoin/watch.
//
// sets must satisfy kvstore.SetStore (also implemented by *kvstore.RedisStore);
// at runtime it is the same underlying value as kv — dual type-asserted in routes.go.
//
// pool is the shared connection pool. Pass nil in unit tests.
func NewStore(kv kvstore.OnceStore, sets kvstore.SetStore, pool *pgxpool.Pool) *Store {
	var q *db.Queries
	if pool != nil {
		q = db.New(pool)
	} else {
		// Warn in production if no pool is provided — audit writes will silently
		// no-op, which is intentional in tests but indicates a misconfiguration
		// in production deployments.
		slog.Warn("events.NewStore: no DB pool provided — audit and token-issuance writes are disabled")
	}
	return &Store{kv: kv, sets: sets, pool: pool, q: q}
}

// ── Redis key helpers (file-private) ─────────────────────────────────────────

// sidKey returns the Redis key for the server-side session ID at token issuance.
// Key format: "btc:token:sid:{jti}"
// TTL: BTC_SSE_TOKEN_TTL (set at StoreSessionSID; deleted at GetDelSessionSID).
func sidKey(jti string) string { return "btc:token:sid:" + jti }

// jtiKey returns the Redis key for the JTI one-time use gate.
// Key format: "btc:token:jti:{jti}"
// Created atomically by ConsumeJTI (OnceStore.ConsumeOnce SET NX PX).
func jtiKey(jti string) string { return "btc:token:jti:" + jti }

// ── Redis key helpers (watch set) ────────────────────────────────────────────

// userAddressSetKey returns the Redis set key for a user's registered watch addresses.
// Key format: "{btc:user:{userID}}:addresses"
// This is the same key written by the bitcoin/watch domain.
func userAddressSetKey(userID string) string {
	return "{btc:user:" + userID + "}:addresses"
}

// ── Storer methods ────────────────────────────────────────────────────────────

// StoreSessionSID writes sessionID to Redis under btc:token:sid:{jti}.
// Called at POST /events/token step 4, BEFORE the JWT is signed.
// If this SET fails, the handler returns 503 and issues no token.
func (s *Store) StoreSessionSID(ctx context.Context, jti, sessionID string, ttl time.Duration) error {
	if err := s.kv.Set(ctx, sidKey(jti), sessionID, ttl); err != nil {
		return telemetry.KVStore("StoreSessionSID.set", err)
	}
	return nil
}

// GetDelSessionSID reads and deletes the session ID stored under btc:token:sid:{jti}.
// Called at GET /events step 4 (SID verification).
//
// Implementation note: Redis GET then DELETE rather than a native GETDEL because
// kvstore.Store does not expose a single-round-trip GETDEL. The two-step approach
// is acceptable here because:
//   - kvstore.ErrNotFound (key missing/expired) maps to 503 fail-closed regardless.
//   - Any race between Get and Delete is irrelevant to security: the JTI
//     ConsumeOnce (step 7) is the authoritative one-time-use gate. GetDelSessionSID
//     is defense-in-depth only.
//
// Returns telemetry.KVStore-wrapped error on Redis failure.
// Returns kvstore.ErrNotFound if the key is absent (expired or never set).
func (s *Store) GetDelSessionSID(ctx context.Context, jti string) (string, error) {
	key := sidKey(jti)
	val, err := s.kv.Get(ctx, key)
	if err != nil {
		return "", telemetry.KVStore("GetDelSessionSID.get", err)
	}
	// Best-effort delete — if this fails the TTL will expire the key naturally.
	_ = s.kv.Delete(ctx, key)
	return val, nil
}

// ConsumeJTI atomically creates btc:token:jti:{jti} if it does not exist.
// Returns (true, nil)  — key created → token not yet consumed (caller proceeds).
// Returns (false, nil) — key existed → token already used → caller returns 401.
// Returns (false, err) — Redis unavailable → caller returns 503.
//
// This is the authoritative JTI replay gate. The ttl MUST be > 0; passing 0
// would allow EX=0 which Redis rejects. The handler guards time.Until(exp) < 1s
// before calling this (see §2 step 3 guard note in events-technical.md).
func (s *Store) ConsumeJTI(ctx context.Context, jti string, ttl time.Duration) (bool, error) {
	consumed, err := s.kv.ConsumeOnce(ctx, jtiKey(jti), ttl)
	if err != nil {
		return false, telemetry.KVStore("ConsumeJTI.consume_once", err)
	}
	return consumed, nil
}

// WriteAuditLog inserts one row into the shared financial_audit_log table.
//
// The events domain has no SQL store of its own; audit rows go through the
// shared financial_audit_log table via the existing InsertAuditLog query,
// following the same pattern as bitcoin/watch.
//
// userID may be empty (e.g. token-parse failure before identity is known).
// When empty, pgUserID is set to {Valid: false} which inserts NULL.
// metadata is marshalled to JSON; an empty map writes "{}".
//
// Provider is set to db.AuthProviderEmail as a neutral sentinel — the bitcoin
// domain has no auth-provider concept.
func (s *Store) WriteAuditLog(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error {
	if s.q == nil {
		return fmt.Errorf("events.WriteAuditLog: no database pool configured")
	}

	var pgUserID pgtype.UUID
	if userID != "" {
		if uid, err := uuid.Parse(userID); err == nil {
			pgUserID = pgtype.UUID{Bytes: [16]byte(uid), Valid: true}
		}
	}

	metaBytes := []byte("{}")
	if len(metadata) > 0 {
		if b, err := json.Marshal(metadata); err == nil {
			metaBytes = b
		}
	}

	return s.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    pgUserID,
		EventType: string(event),
		Provider:  db.AuthProviderEmail, // neutral sentinel; bitcoin has no auth_provider concept
		IpAddress: nil,                  // IP is never written to the immutable audit row (PII)
		UserAgent: pgtype.Text{},
		Metadata:  metaBytes,
	})
}

// GetUserWatchAddresses returns all watch addresses registered by userID.
// It SScan-iterates the Redis set at {btc:user:{userID}}:addresses.
// Returns an empty slice (not an error) when the set is absent.
func (s *Store) GetUserWatchAddresses(ctx context.Context, userID string) ([]string, error) {
	key := userAddressSetKey(userID)
	var all []string
	var cursor uint64
	for {
		// count=500: larger hint reduces round-trips for merchants with large watch sets.
		members, next, err := s.sets.SScan(ctx, key, cursor, "", 500)
		if err != nil {
			return nil, telemetry.KVStore("GetUserWatchAddresses.sscan", err)
		}
		all = append(all, members...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return all, nil
}

// RecordTokenIssuance inserts one row into sse_token_issuances for GDPR IP audit.
//
// This write is NON-FATAL — the caller (service) logs a warning on error and
// continues. A missing DB row only affects GDPR IP-erasure coverage, not security.
//
// Parameters use plain Go types; pgtype conversions happen here, not in the service.
//   - vendorID    — caller passes [16]byte from token.UserIDFromContext
//   - jtiHash     — HMAC-SHA256(jti, BTC_SERVER_SECRET), computed by caller
//   - sourceIPHash — SHA256(ip || dailyRotationKey); nil when IP unavailable
//   - expiresAt   — now + BTC_SSE_TOKEN_TTL
func (s *Store) RecordTokenIssuance(
	ctx context.Context,
	vendorID [16]byte,
	network, jtiHash string,
	sourceIPHash *string,
	expiresAt time.Time,
) error {
	if s.q == nil {
		return fmt.Errorf("events.RecordTokenIssuance: no database pool configured")
	}
	var ipHash pgtype.Text
	if sourceIPHash != nil {
		ipHash = pgtype.Text{String: *sourceIPHash, Valid: true}
	}
	_, err := s.q.InsertSSETokenIssuance(ctx, db.InsertSSETokenIssuanceParams{
		VendorID:     pgtype.UUID{Bytes: vendorID, Valid: true},
		Network:      network,
		JtiHash:      jtiHash,
		SourceIpHash: ipHash,
		ExpiresAt:    pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return telemetry.Store("RecordTokenIssuance.insert", err)
	}
	return nil
}
