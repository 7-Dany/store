/* ============================================================
   sql/queries/worker.sql
   Queries for background worker jobs.
   ============================================================ */


/* ── Background purge worker ── */

-- name: GetAccountsDueForPurge :many
-- Returns up to 100 user IDs whose grace period has expired.
-- The worker processes these in a loop, purging each in its own transaction.
SELECT id
FROM users
WHERE deleted_at < NOW() - INTERVAL '30 days'
LIMIT 100;


-- name: HardDeleteUser :exec
-- Permanently deletes a user row. All child rows (refresh_tokens, user_sessions,
-- one_time_tokens, user_identities, auth_audit_log, user_roles) are removed via
-- CASCADE. Must be called AFTER InsertPurgeLog within the same transaction (D-14).
DELETE FROM users
WHERE id = @user_id::uuid;


-- name: InsertPurgeLog :exec
-- Writes a permanent record of the purge before the user row is deleted (D-15).
-- metadata is a JSONB blob — callers pass {"deleted_at": "<RFC3339>"} at minimum.
INSERT INTO account_purge_log (user_id, metadata)
VALUES (@user_id::uuid, @metadata::jsonb);
