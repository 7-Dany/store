/* ============================================================
   sql/queries/worker.sql
   Background worker queries for sqlc code generation.

   Covers the account purge worker, which hard-deletes users whose
   30-day soft-delete grace period has expired.

   Purge transaction order (required):
     1. InsertPurgeLog  — write the compliance record first.
     2. HardDeleteUser  — then delete the user row.
   Both statements must run in the same transaction. If HardDeleteUser
   is called before InsertPurgeLog the compliance record will be missing.

   Depends on: 001_core.sql (users, account_purge_log)
   ============================================================ */


/* ── Background purge worker ── */

-- name: GetAccountsDueForPurge :many
-- Returns up to 100 user IDs whose 30-day grace period has expired.
-- The worker processes these in a loop, purging each in its own transaction
-- to bound the blast radius of any single failure.
-- Index: idx_users_pending_deletion ON users(deleted_at) WHERE deleted_at IS NOT NULL
-- avoids a full scan of active (deleted_at IS NULL) rows.
SELECT id
FROM users
WHERE deleted_at < NOW() - INTERVAL '30 days'
LIMIT 100;


-- name: HardDeleteUser :exec
-- Permanently deletes the user row. All child rows are removed via ON DELETE CASCADE:
-- refresh_tokens, user_sessions, one_time_tokens, user_identities, user_secrets,
-- auth_audit_log (user_id SET NULL — row is kept), user_roles.
-- MUST be called AFTER InsertPurgeLog within the same transaction.
DELETE FROM users
WHERE id = @user_id::uuid;


-- name: InsertPurgeLog :exec
-- Writes a permanent compliance record of the purge to account_purge_log.
-- account_purge_log has no FK to users so this row survives the subsequent DELETE.
-- @metadata is a JSONB blob — callers pass {"deleted_at": "<RFC3339>"} at minimum.
-- Recommended additional keys: purged_by (UUID of the worker job), reason (string),
-- anonymized_email (one-way hash of the purged email for re-registration dedup).
INSERT INTO account_purge_log (user_id, metadata)
VALUES (@user_id::uuid, @metadata::jsonb);
