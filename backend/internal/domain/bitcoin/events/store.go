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
// pool is used for non-fatal DB writes (InsertSSETokenIssuance). Pass nil in
// unit tests that do not exercise RecordTokenIssuance.
type Store struct {
	kv   kvstore.OnceStore
	pool *pgxpool.Pool
	q    *db.Queries // pre-allocated from pool; nil when pool is nil
}

// NewStore constructs a Store.
//
// kv must satisfy kvstore.OnceStore (implemented by *kvstore.RedisStore).
// In routes.go: deps.KVStore.(kvstore.OnceStore) — panics at startup if Redis
// is not configured, same pattern as bitcoin/watch.
//
// pool is the shared connection pool. Pass nil in unit tests.
func NewStore(kv kvstore.OnceStore, pool *pgxpool.Pool) *Store {
	var q *db.Queries
	if pool != nil {
		q = db.New(pool)
	} else {
		// Warn in production if no pool is provided — audit writes will silently
		// no-op, which is intentional in tests but indicates a misconfiguration
		// in production deployments.
		slog.Warn("events.NewStore: no DB pool provided — audit and token-issuance writes are disabled")
	}
	return &Store{kv: kv, pool: pool, q: q}
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

// GetUserWatchAddresses returns all active address-watch targets registered by userID.
func (s *Store) GetUserWatchAddresses(ctx context.Context, userID, network string) ([]string, error) {
	if s.q == nil {
		return nil, fmt.Errorf("events.GetUserWatchAddresses: no database pool configured")
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("events.GetUserWatchAddresses: invalid user id: %w", err)
	}
	rows, err := s.q.ListActiveBitcoinWatchAddressesByUser(ctx, db.ListActiveBitcoinWatchAddressesByUserParams{
		UserID:  pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
		Network: network,
	})
	if err != nil {
		return nil, telemetry.Store("GetUserWatchAddresses.list", err)
	}
	addrs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Valid {
			addrs = append(addrs, row.String)
		}
	}
	return addrs, nil
}

// UpsertWatchBitcoinTxStatus persists one watch-discovered txstatus row per matched address.
func (s *Store) UpsertWatchBitcoinTxStatus(ctx context.Context, in TrackedStatusUpsertInput) error {
	if s.q == nil {
		return fmt.Errorf("events.UpsertWatchBitcoinTxStatus: no database pool configured")
	}
	uid, err := uuid.Parse(in.UserID)
	if err != nil {
		return fmt.Errorf("events.UpsertWatchBitcoinTxStatus: invalid user id: %w", err)
	}
	var totalAmountSat int64
	for _, out := range in.Outputs {
		totalAmountSat += out.AmountSat
	}

	feeRateSatVByte, err := toPgNumeric(in.FeeRateSatVByte)
	if err != nil {
		return fmt.Errorf("events.UpsertWatchBitcoinTxStatus: invalid fee rate: %w", err)
	}

	row, err := s.q.UpsertWatchBitcoinTxStatus(ctx, db.UpsertWatchBitcoinTxStatusParams{
		UserID:          pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
		Network:         in.Network,
		Txid:            in.TxID,
		AmountSat:       totalAmountSat,
		FeeRateSatVbyte: feeRateSatVByte,
		FirstSeenAt:     pgtype.Timestamptz{Time: in.FirstSeenAt, Valid: true},
		LastSeenAt:      pgtype.Timestamptz{Time: in.LastSeenAt, Valid: true},
	})
	if err != nil {
		return telemetry.Store("UpsertWatchBitcoinTxStatus.exec", err)
	}

	for _, out := range in.Outputs {
		if err := s.q.UpsertBitcoinTxStatusRelatedAddress(ctx, db.UpsertBitcoinTxStatusRelatedAddressParams{
			TxStatusID: row.ID,
			Address:    out.Address,
			AmountSat:  out.AmountSat,
		}); err != nil {
			return telemetry.Store("UpsertBitcoinTxStatusRelatedAddress.exec", err)
		}
	}
	return nil
}

// TouchBitcoinTxStatusMempool marks all rows for (user_id, txid) as mempool.
func (s *Store) TouchBitcoinTxStatusMempool(ctx context.Context, userID, network, txid string, feeRateSatVByte float64, lastSeenAt time.Time) error {
	if s.q == nil {
		return fmt.Errorf("events.TouchBitcoinTxStatusMempool: no database pool configured")
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("events.TouchBitcoinTxStatusMempool: invalid user id: %w", err)
	}
	feeRate, err := toPgNumeric(feeRateSatVByte)
	if err != nil {
		return fmt.Errorf("events.TouchBitcoinTxStatusMempool: invalid fee rate: %w", err)
	}

	if _, err := s.q.TouchBitcoinTxStatusMempool(ctx, db.TouchBitcoinTxStatusMempoolParams{
		FeeRateSatVbyte: feeRate,
		LastSeenAt:      pgtype.Timestamptz{Time: lastSeenAt, Valid: true},
		UserID:          pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
		Network:         network,
		Txid:            txid,
	}); err != nil {
		return telemetry.Store("TouchBitcoinTxStatusMempool.exec", err)
	}
	return nil
}

// ConfirmBitcoinTxStatus marks tracked rows as confirmed.
func (s *Store) ConfirmBitcoinTxStatus(ctx context.Context, userID, network, txid, blockHash string, confirmations int, blockHeight int64, confirmedAt time.Time) error {
	if s.q == nil {
		return fmt.Errorf("events.ConfirmBitcoinTxStatus: no database pool configured")
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("events.ConfirmBitcoinTxStatus: invalid user id: %w", err)
	}
	if _, err := s.q.ConfirmBitcoinTxStatus(ctx, db.ConfirmBitcoinTxStatusParams{
		Confirmations: int32(confirmations),
		ConfirmedAt:   pgtype.Timestamptz{Time: confirmedAt, Valid: true},
		BlockHash:     pgtype.Text{String: blockHash, Valid: true},
		BlockHeight:   blockHeight,
		UserID:        pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
		Network:       network,
		Txid:          txid,
	}); err != nil {
		return telemetry.Store("ConfirmBitcoinTxStatus.exec", err)
	}
	return nil
}

// MarkBitcoinTxStatusReplaced marks tracked rows as replaced.
func (s *Store) MarkBitcoinTxStatusReplaced(ctx context.Context, userID, network, replacedTxID, replacementTxID string, replacedAt time.Time) error {
	if s.q == nil {
		return fmt.Errorf("events.MarkBitcoinTxStatusReplaced: no database pool configured")
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("events.MarkBitcoinTxStatusReplaced: invalid user id: %w", err)
	}
	if _, err := s.q.MarkBitcoinTxStatusReplaced(ctx, db.MarkBitcoinTxStatusReplacedParams{
		ReplacementTxid: pgtype.Text{String: replacementTxID, Valid: true},
		ReplacedAt:      pgtype.Timestamptz{Time: replacedAt, Valid: true},
		UserID:          pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
		Network:         network,
		ReplacedTxid:    replacedTxID,
	}); err != nil {
		return telemetry.Store("MarkBitcoinTxStatusReplaced.exec", err)
	}
	return nil
}

// ListBitcoinTxStatusUsersByTxID returns the users that currently track txid.
func (s *Store) ListBitcoinTxStatusUsersByTxID(ctx context.Context, network, txid string) ([]string, error) {
	if s.q == nil {
		return nil, fmt.Errorf("events.ListBitcoinTxStatusUsersByTxID: no database pool configured")
	}
	rows, err := s.q.ListBitcoinTxStatusUsersByTxID(ctx, db.ListBitcoinTxStatusUsersByTxIDParams{
		Network: network,
		Txid:    txid,
	})
	if err != nil {
		return nil, telemetry.Store("ListBitcoinTxStatusUsersByTxID.list", err)
	}
	users := make([]string, 0, len(rows))
	for _, row := range rows {
		users = append(users, uuid.UUID(row.Bytes).String())
	}
	return users, nil
}

// ListActiveBitcoinTransactionWatchUsersByTxID returns the users that actively watch txid.
func (s *Store) ListActiveBitcoinTransactionWatchUsersByTxID(ctx context.Context, network, txid string) ([]string, error) {
	if s.q == nil {
		return nil, fmt.Errorf("events.ListActiveBitcoinTransactionWatchUsersByTxID: no database pool configured")
	}
	rows, err := s.q.ListActiveBitcoinTransactionWatchUsersByTxID(ctx, db.ListActiveBitcoinTransactionWatchUsersByTxIDParams{
		Network: network,
		Txid:    pgtype.Text{String: txid, Valid: true},
	})
	if err != nil {
		return nil, telemetry.Store("ListActiveBitcoinTransactionWatchUsersByTxID.list", err)
	}
	users := make([]string, 0, len(rows))
	for _, row := range rows {
		users = append(users, uuid.UUID(row.Bytes).String())
	}
	return users, nil
}

func toPgNumeric(v float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	err := n.Scan(v)
	return n, err
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
