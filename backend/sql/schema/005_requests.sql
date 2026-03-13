-- +goose Up
-- +goose StatementBegin

/*
 * 005_requests.sql — Approval request workflow schema.
 *
 * Provides a flexible request / approval / execution pipeline with:
 * requests — the core request record with JSONB payload
 * request_status_history — immutable log of every status transition
 * request_type_schemas — JSON Schema rules for validating request_data per type
 * request_required_approvers— per-request approval role configuration
 * request_approvals — immutable approval/rejection action records
 * request_notifications — per-channel delivery queue for request lifecycle events
 * request_rate_limits — per-user, per-type sliding window rate counters
 * request_sla_config — SLA thresholds per request_type
 * request_sla_violations — detected SLA breaches
 *
 * Depends on: 001_core.sql (users), 003_rbac.sql (roles)
 */


/* ─────────────────────────────────────────────────────────────
 ENUMS
 ───────────────────────────────────────────────────────────── */

-- Lifecycle states for a request. Legal transition graph:
-- pending → approved → executing → completed | failed
-- pending | approved | executing → rejected | cancelled
-- Terminal states (rejected, cancelled, completed, failed) are immutable;
-- enforced by trg_prevent_terminal_status_change.
CREATE TYPE request_status_enum AS ENUM (
 'pending', -- submitted, awaiting approvals
 'approved', -- quorum reached; ready for execution
 'rejected', -- explicitly rejected by an approver
 'cancelled', -- withdrawn by the requester or expired
 'executing', -- handler is actively running the action
 'completed', -- handler finished successfully
 'failed' -- handler encountered an unrecoverable error
);

COMMENT ON TYPE request_status_enum IS
 'Request lifecycle states. Terminal: rejected, cancelled, completed, failed.';

-- Actions an approver can record against a pending request.
CREATE TYPE approval_action_enum AS ENUM (
 'approved', -- approver grants the request
 'rejected' -- approver denies the request
);

COMMENT ON TYPE approval_action_enum IS
 'Actions an approver can take on a pending request.';

-- Events that cause a notification row to be inserted in request_notifications.
CREATE TYPE notification_type_enum AS ENUM (
 'request_created', -- requester and approvers are notified on submission
 'request_approved', -- requester is notified when quorum is reached
 'request_rejected', -- requester is notified when any approver rejects
 'request_executed', -- interested parties are notified on completion
 'request_failed', -- interested parties are notified on failure
 'request_cancelled' -- interested parties are notified on cancellation
);

COMMENT ON TYPE notification_type_enum IS
 'Events that produce a notification row in request_notifications.';

-- Delivery channels for notifications. One row per event per channel is created.
CREATE TYPE delivery_channel_enum AS ENUM (
 'email', -- email message to the user's registered address
 'sms', -- SMS text message to the user's phone number
 'push', -- mobile push notification
 'in_app' -- in-app notification shown in the notification centre
);

COMMENT ON TYPE delivery_channel_enum IS
 'Delivery channels for request_notifications. One row per event per channel.';

-- SLA violation kinds. New kinds require ALTER TYPE … ADD VALUE (no table rewrite on PG 12+).
CREATE TYPE sla_violation_type_enum AS ENUM (
 'pending_timeout', -- request sat in pending status longer than pending_max_hours
 'execution_timeout' -- execution took longer than execution_max_minutes
);

COMMENT ON TYPE sla_violation_type_enum IS
 'SLA breach categories. Extend with ALTER TYPE … ADD VALUE.';


/* ─────────────────────────────────────────────────────────────
 REQUEST TYPE SCHEMAS
 ───────────────────────────────────────────────────────────── */

-- Stores the JSON Schema for each request_type's request_data payload.
-- The application layer validates incoming request_data against this schema before INSERT.
CREATE TABLE request_type_schemas (
 -- Keyed by the same discriminator used in requests.request_type.
 request_type VARCHAR(100) PRIMARY KEY,

 -- JSON Schema object defining the valid shape of request_data for this type.
 json_schema JSONB NOT NULL,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- json_schema must be a JSON object (not an array or scalar).
 CONSTRAINT chk_rts_schema_is_object
 CHECK (jsonb_typeof(json_schema) = 'object')
);

COMMENT ON TABLE request_type_schemas IS
 'JSON Schema validation rules for request_data keyed by request_type.';
COMMENT ON COLUMN request_type_schemas.json_schema IS
 'JSON Schema for this request_type''s request_data payload.';


/* ─────────────────────────────────────────────────────────────
 REQUESTS
 ───────────────────────────────────────────────────────────── */

/*
 * Core request record with flexible JSONB payload and quorum-based approval.
 *
 * requester_id is nullable (ON DELETE SET NULL) so requests survive hard-purges
 * of the requesting user — the approval and forensic trail must not be lost when
 * an account is deleted.
 *
 * approvals_required is kept in sync with SUM(min_required) across
 * request_required_approvers by fn_sync_approvals_required (trigger in 006).
 */
CREATE TABLE requests (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Discriminator: product_creation, vendor_withdrawal, permission_action, etc.
 -- Controls which JSON Schema is used to validate request_data.
 -- FK ensures a request_type_schemas row exists before any request of this type
 -- can be inserted. ON DELETE RESTRICT prevents schema removal while requests exist.
 request_type VARCHAR(100) NOT NULL
  REFERENCES request_type_schemas(request_type) ON DELETE RESTRICT,

 -- Owner of the request; SET NULL on user purge to preserve the request record.
 requester_id UUID REFERENCES users(id) ON DELETE SET NULL,

 -- Current lifecycle state; default 'pending' on creation.
 status request_status_enum NOT NULL DEFAULT 'pending',

 -- Arbitrary payload; structure varies by request_type. Validated against
 -- request_type_schemas before INSERT by the application layer.
 request_data JSONB NOT NULL,

 -- Higher = more urgent. Range -100 to 100. Used to order the approval queue.
 -- Effective priority at claim time adds up to +50 age points to prevent starvation.
 priority INTEGER NOT NULL DEFAULT 0,

 -- Number of approvals needed before status transitions to 'approved'.
 -- Kept in sync with SUM(request_required_approvers.min_required) by trigger.
 approvals_required INTEGER NOT NULL DEFAULT 1,

 CONSTRAINT chk_requests_priority_range
 CHECK (priority BETWEEN -100 AND 100),

 -- Auto-reject timestamp: if the request is still pending at this time, cancel it.
 expires_at TIMESTAMPTZ,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Timestamp when the request reached a terminal state. Required for terminal states.
 resolved_at TIMESTAMPTZ,

 CONSTRAINT chk_requests_data_is_object
 CHECK (jsonb_typeof(request_data) = 'object'),
 CONSTRAINT chk_requests_approvals_positive
 CHECK (approvals_required > 0),
 -- Prevents terminal-state requests from having NULL resolved_at (forensic integrity).
 CONSTRAINT chk_requests_resolved_coherent CHECK (
 status NOT IN ('rejected','cancelled','completed','failed')
 OR resolved_at IS NOT NULL
 )
);

-- General-purpose status filtering (e.g. "all executing requests").
CREATE INDEX idx_requests_status_all ON requests(status);

-- Supports per-user request history queries ("my requests").
CREATE INDEX idx_requests_requester ON requests(requester_id);

-- Supports admin queries filtered by request type (e.g. "all pending vendor withdrawals").
CREATE INDEX idx_requests_type ON requests(request_type);

-- Supports recency-ordered queries.
CREATE INDEX idx_requests_created ON requests(created_at DESC);

-- Supports priority-ordered admin queries (e.g. "most urgent requests").
CREATE INDEX idx_requests_priority ON requests(priority DESC, created_at DESC);

-- GIN index enables JSONB containment (@>) and existence (?) queries on request_data.
CREATE INDEX idx_requests_data ON requests USING GIN(request_data jsonb_ops);

-- Covers the common "how many pending requests does user X have?" query.
CREATE INDEX idx_requests_user_pending ON requests(requester_id, status) WHERE status = 'pending';

-- Supports the expiry-sweep job that auto-cancels expired pending requests.
CREATE INDEX idx_requests_expired ON requests(expires_at) WHERE status = 'pending' AND expires_at IS NOT NULL;

-- Approval queue ordered by urgency (priority DESC) then submission time (ASC = oldest first).
-- status is a WHERE filter, not a key column, because it is a constant predicate.
CREATE INDEX idx_requests_approval_queue ON requests(priority DESC, created_at ASC) WHERE status = 'pending';

COMMENT ON TABLE requests IS
 'Approval requests with flexible JSONB payloads and quorum support.';
COMMENT ON COLUMN requests.request_type IS
 'Discriminator: product_creation, vendor_withdrawal, permission_action, etc. '
 'FK to request_type_schemas ensures a schema row exists before requests of this type can be inserted.';
COMMENT ON COLUMN requests.request_data IS
 'Flexible payload. Structure validated against request_type_schemas.';
COMMENT ON COLUMN requests.priority IS
 'Higher = more urgent. Used to order the approval queue.';
COMMENT ON COLUMN requests.approvals_required IS
 'Number of approvals needed before the request transitions to approved. '
 'Single source of truth; request_required_approvers.min_required is per-role.';
COMMENT ON COLUMN requests.expires_at IS
 'Auto-reject if still pending past this time.';
COMMENT ON COLUMN requests.resolved_at IS
 'Timestamp when the request entered a terminal state (rejected, cancelled, completed, failed).
 NULL while still in-flight. Set by the application layer on status transition.';


/* ─────────────────────────────────────────────────────────────
 REQUEST STATUS HISTORY
 ───────────────────────────────────────────────────────────── */

/*
 * Immutable append-only log of every status transition on a request.
 * Answers: "who changed this request from pending to rejected, and when?"
 *
 * ON DELETE RESTRICT (not CASCADE) on request_id: deleting a request must not
 * silently destroy its status history. Forensic evidence must be preserved; retire
 * requests by transitioning to a terminal state, never by hard DELETE.
 */
CREATE TABLE request_status_history (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- RESTRICT: prevents hard-deleting a request while status history exists.
 request_id UUID NOT NULL REFERENCES requests(id) ON DELETE RESTRICT,

 -- The status the request was in before this transition. NULL on the very first row (no prior state).
 old_status request_status_enum,

 -- The status the request moved to. Never NULL.
 new_status request_status_enum NOT NULL,

 -- Actor who triggered the transition; read from rbac.acting_user session variable.
 -- NULL for automated scheduler transitions (no human actor).
 changed_by UUID REFERENCES users(id) ON DELETE SET NULL,
 changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Optional human-readable context explaining why the transition occurred.
 note TEXT
);

-- Supports per-request audit trail ("what happened to request X?"), ordered by recency.
CREATE INDEX idx_rsh_request ON request_status_history(request_id, changed_at DESC);

-- Supports time-range audit queries across all requests.
CREATE INDEX idx_rsh_changed_at ON request_status_history(changed_at DESC);

COMMENT ON TABLE request_status_history IS
 'Immutable audit log of every status transition on a request. '
 'Populated by trg_record_request_status_history on requests INSERT/UPDATE. '
 'RESTRICT FK prevents hard-deleting a request while status history exists.';
COMMENT ON COLUMN request_status_history.changed_by IS
 'Actor who triggered the transition. Read from rbac.acting_user session variable; '
 'falls back to NULL when unset (e.g. automated scheduler transitions).';


/* ─────────────────────────────────────────────────────────────
 REQUEST REQUIRED APPROVERS
 ───────────────────────────────────────────────────────────── */

/*
 * Specifies which roles can approve each request, with multi-level hierarchy support.
 *
 * ON DELETE RESTRICT on role_id: deleting a role must not silently wipe in-flight
 * approval requirements. Retire a role by reassigning its approval responsibilities first.
 *
 * conditions narrows which approvers within the role qualify (same ABAC vocabulary as
 * role_permissions.conditions).
 *
 * fn_sync_approvals_required (006) keeps requests.approvals_required = SUM(min_required)
 * whenever rows are inserted, updated, or deleted here.
 */
CREATE TABLE request_required_approvers (
 -- CASCADE: removing the request removes its approval requirements.
 request_id UUID REFERENCES requests(id) ON DELETE CASCADE,

 -- RESTRICT: prevents deleting a role that is required for in-flight approvals.
 role_id UUID REFERENCES roles(id) ON DELETE RESTRICT,

 -- Hierarchical tier: 0 = first approver tier, 1 = second, etc.
 approval_level INTEGER NOT NULL DEFAULT 0,

 -- Minimum number of approvals from this role at this level.
 min_required INTEGER NOT NULL DEFAULT 1,

 -- TRUE = all lower levels must be fully satisfied before this level can act.
 must_complete_previous_level BOOLEAN NOT NULL DEFAULT FALSE,

 -- Optional ABAC conditions narrowing which approvers within the role qualify.
 conditions JSONB NOT NULL DEFAULT '{}',

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 PRIMARY KEY (request_id, role_id),

 CONSTRAINT chk_rra_conditions_is_object
 CHECK (jsonb_typeof(conditions) = 'object'),
 CONSTRAINT chk_rra_min_required_positive
 CHECK (min_required > 0),
 CONSTRAINT chk_rra_approval_level_non_negative
 CHECK (approval_level >= 0)
);

-- Reverse lookup: "which requests require approval from role Y?"
CREATE INDEX idx_request_approvers_role ON request_required_approvers(role_id);

-- Supports level-sequenced approval logic ("who approves at level 0?").
CREATE INDEX idx_request_approvers_level ON request_required_approvers(request_id, approval_level);

-- Covering index for the "can user X approve request Y?" eligibility check.
-- Avoids a heap fetch by including the most-needed columns.
CREATE INDEX idx_requests_user_can_approve ON request_required_approvers(role_id, request_id)
 INCLUDE (approval_level, min_required, conditions);

COMMENT ON TABLE request_required_approvers IS
 'Which roles can approve each request, with multi-level hierarchy support.';
COMMENT ON COLUMN request_required_approvers.approval_level IS
 'Hierarchical level: 0 = first, 1 = second, etc.';
COMMENT ON COLUMN request_required_approvers.must_complete_previous_level IS
 'TRUE = all lower levels must be satisfied before this level can act.';
COMMENT ON COLUMN request_required_approvers.conditions IS
 'JSONB conditions that must be met for this role to satisfy the approval requirement. Same key vocabulary as role_permissions.conditions.';


/* ─────────────────────────────────────────────────────────────
 REQUEST APPROVALS
 ───────────────────────────────────────────────────────────── */

/*
 * Immutable record of every approval or rejection action.
 *
 * ON DELETE RESTRICT on request_id: approval records are forensic evidence and
 * must not be deleted when the parent request is hard-deleted.
 *
 * UNIQUE (request_id, approver_id) prevents a race condition where the same
 * approver submits two approval actions concurrently.
 */
CREATE TABLE request_approvals (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- RESTRICT: prevents deleting the request while approval records exist.
 request_id UUID REFERENCES requests(id) ON DELETE RESTRICT,

 -- SET NULL: preserve the approval record even if the approver's account is deleted.
 approver_id UUID REFERENCES users(id) ON DELETE SET NULL,

 -- RESTRICT: preserves the role reference for forensic context.
 role_used UUID REFERENCES roles(id) ON DELETE RESTRICT,

 -- Whether the approver approved or rejected the request.
 action approval_action_enum NOT NULL,

 -- Optional justification or note from the approver.
 comment TEXT,

 approved_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Prevents a race where the same approver approves the same request twice.
 CONSTRAINT uq_approval_per_user_request UNIQUE (request_id, approver_id)
);

-- Speeds up "how many approvals does request X have?" queries.
CREATE INDEX idx_request_approvals_request ON request_approvals(request_id);

-- Supports recency queries across all approvals.
CREATE INDEX idx_request_approvals_recent ON request_approvals(approved_at DESC);

-- Supports "which approvals used role Y?" (audit / role-retirement checks).
CREATE INDEX idx_request_approvals_role ON request_approvals(role_used);

-- Supersedes a plain idx_request_approvals_approver: covers "what has approver X approved, ordered by recency?".
CREATE INDEX idx_request_approvals_approver_date ON request_approvals(approver_id, approved_at DESC);

-- Supports quorum-check query: count approved actions per request.
CREATE INDEX idx_request_approvals_count ON request_approvals(request_id, action) WHERE action = 'approved';

COMMENT ON TABLE request_approvals IS
 'Immutable audit trail of approval/rejection actions. '
 'RESTRICT FK prevents hard-deleting a request while approvals exist.';
COMMENT ON COLUMN request_approvals.role_used IS
 'Role that authorised this approval. RESTRICT preserves the reference even if the role is soft-deleted.';


/* ─────────────────────────────────────────────────────────────
 REQUEST NOTIFICATIONS
 ───────────────────────────────────────────────────────────── */

/*
 * Notification queue for request lifecycle events.
 * One row per event per delivery channel — delivery_channel is NOT NULL.
 *
 * sent_at = NULL means queued for delivery.
 * read_at = NULL means unread in the user's in-app notification centre.
 */
CREATE TABLE request_notifications (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- CASCADE: notifications are removed when the request or user is deleted.
 request_id UUID REFERENCES requests(id) ON DELETE CASCADE,
 user_id UUID REFERENCES users(id) ON DELETE CASCADE,

 -- Which lifecycle event triggered this notification.
 notification_type notification_type_enum NOT NULL,

 -- Which delivery channel this row represents. One row per channel per event.
 delivery_channel delivery_channel_enum NOT NULL,

 -- NULL = queued, not yet delivered. Non-NULL = successfully delivered at this time.
 sent_at TIMESTAMPTZ,

 -- NULL = unread. Set when the user views the notification in the app.
 read_at TIMESTAMPTZ,

 -- Short subject/title for the notification (e.g. email subject line).
 title VARCHAR(255),

 -- Full notification body text.
 message TEXT,

 -- At least one of title or message must be non-NULL to ensure deliverable content.
 CONSTRAINT chk_rn_has_content
 CHECK (title IS NOT NULL OR message IS NOT NULL),

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Supports "inbox for user X, grouped by read/unread status".
CREATE INDEX idx_request_notifications_user ON request_notifications(user_id, read_at);

-- Supports "all notifications for request X" (admin / requester view).
CREATE INDEX idx_request_notifications_request ON request_notifications(request_id);

-- Used by the delivery worker to find queued (unsent) notifications.
CREATE INDEX idx_request_notifications_unsent ON request_notifications(sent_at) WHERE sent_at IS NULL;

-- Supports the unread inbox query: user X's unread notifications, most recent first.
CREATE INDEX idx_request_notifications_user_unread ON request_notifications(user_id, created_at DESC) WHERE read_at IS NULL;

-- Supports the cleanup job that deletes delivered notifications past their retention window.
CREATE INDEX idx_request_notifications_cleanup
 ON request_notifications(created_at)
 WHERE sent_at IS NOT NULL;

COMMENT ON TABLE request_notifications IS
 'Notification queue for request status changes with per-channel delivery tracking. '
 'One row per event per delivery channel — delivery_channel is NOT NULL.';
COMMENT ON COLUMN request_notifications.sent_at IS
 'NULL = queued, non-NULL = delivered.';
COMMENT ON COLUMN request_notifications.read_at IS
 'NULL = unread. Set when the user views the notification.';


/* ─────────────────────────────────────────────────────────────
 REQUEST RATE LIMITS
 ───────────────────────────────────────────────────────────── */

/*
 * Per-user, per-type sliding window counters for request creation rate limiting.
 * Counters must be incremented atomically (requests_created_count = requests_created_count + 1)
 * to avoid lost updates under concurrent burst submissions.
 * When the window expires (window_start older than the window duration), the application
 * resets the counter and window_start on the next request.
 */
CREATE TABLE request_rate_limits (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_type VARCHAR(100) NOT NULL,

 -- Number of requests created within the current window. Increment atomically.
 requests_created_count INTEGER NOT NULL DEFAULT 0,

 -- Start of the current rolling window. Reset when the window expires.
 window_start TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Timestamp of the most recent request creation within this window.
 last_request_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 PRIMARY KEY (user_id, request_type),
 CONSTRAINT chk_rrl_count_non_negative CHECK (requests_created_count >= 0)
);

-- Supports per-user window lookup (most common query pattern).
CREATE INDEX idx_rate_limits_window ON request_rate_limits(user_id, window_start);

-- Supports cleanup job that removes stale window rows older than the window duration.
CREATE INDEX idx_rate_limits_cleanup ON request_rate_limits(window_start);

COMMENT ON TABLE request_rate_limits IS
 'Per-user, per-type sliding window counters for request creation rate limiting. '
 'Stale rows (window_start older than window duration) should be cleaned up periodically.';
COMMENT ON COLUMN request_rate_limits.window_start IS
 'Start of the current rate-limit window. Reset when the window expires.';


/* ─────────────────────────────────────────────────────────────
 REQUEST SLA CONFIG
 ───────────────────────────────────────────────────────────── */

-- SLA thresholds per request_type.
-- ON DELETE RESTRICT on request_type_schemas: SLA config requires the schema to exist first.
CREATE TABLE request_sla_config (
 request_type VARCHAR(100) PRIMARY KEY
 REFERENCES request_type_schemas(request_type) ON DELETE RESTRICT,

 -- Maximum hours a request may sit in 'pending' before a SLA violation is recorded.
 pending_max_hours INTEGER NOT NULL,

 -- Maximum minutes an 'executing' request may run before a SLA violation is recorded.
 execution_max_minutes INTEGER NOT NULL,

 -- Whether to send an approaching-SLA alert before the full SLA is breached.
 notify_approaching_sla BOOLEAN NOT NULL DEFAULT TRUE,

 -- Percentage of the SLA window elapsed at which the approaching alert fires.
 -- e.g. 80 = alert when 80% of pending_max_hours has passed.
 approaching_threshold_percent INTEGER NOT NULL DEFAULT 80,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 CONSTRAINT chk_sla_pending_max_positive
 CHECK (pending_max_hours > 0),
 CONSTRAINT chk_sla_execution_max_positive
 CHECK (execution_max_minutes > 0),
 CONSTRAINT chk_sla_threshold_range
 CHECK (approaching_threshold_percent > 0 AND approaching_threshold_percent <= 100)
);

COMMENT ON TABLE request_sla_config IS
 'SLA thresholds per request_type for alerting and violation detection.';
COMMENT ON COLUMN request_sla_config.approaching_threshold_percent IS
 'Alert is triggered when this percentage of the SLA window has elapsed.';


/* ─────────────────────────────────────────────────────────────
 REQUEST SLA VIOLATIONS
 ───────────────────────────────────────────────────────────── */

-- Records a detected SLA breach for monitoring, on-call alerting, and reporting.
CREATE TABLE request_sla_violations (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- CASCADE: violations are removed if the request is deleted (soft-delete preferred).
 request_id UUID REFERENCES requests(id) ON DELETE CASCADE,

 -- What kind of SLA was breached.
 violation_type sla_violation_type_enum NOT NULL,

 -- When the breach was first detected by the monitoring job.
 detected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- When the breach was acknowledged or resolved. NULL = still unresolved.
 resolved_at TIMESTAMPTZ,

 -- User who acknowledged/resolved the breach. NULL if automated or user was deleted.
 resolved_by UUID REFERENCES users(id) ON DELETE SET NULL,

 -- TRUE once the on-call alert has been sent. Never reset to FALSE; prevents duplicate alerts.
 notified BOOLEAN NOT NULL DEFAULT FALSE,

 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- resolved_at must not predate the detection timestamp.
 CONSTRAINT chk_sla_resolved_after_detected
 CHECK (resolved_at IS NULL OR resolved_at >= detected_at)
);

-- Supports per-request violation history.
CREATE INDEX idx_sla_violations_request ON request_sla_violations(request_id);

-- Hot path for monitoring dashboards: unresolved violations ordered by recency, per type.
CREATE INDEX idx_sla_violations_unresolved ON request_sla_violations(violation_type, detected_at DESC) WHERE resolved_at IS NULL;

COMMENT ON TABLE request_sla_violations IS
 'Records SLA breaches for monitoring, alerting, and reporting.';
COMMENT ON COLUMN request_sla_violations.violation_type IS
 'pending_timeout = request sat in pending too long; execution_timeout = execution took too long.';
COMMENT ON COLUMN request_sla_violations.notified IS
 'TRUE once the on-call alert has been dispatched for this violation. Never reset to FALSE — duplicate notifications are suppressed by this flag.';
COMMENT ON COLUMN request_sla_violations.resolved_by IS
 'User who acknowledged or resolved this SLA breach. NULL if resolved by automated process or user was deleted.';


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS request_sla_violations CASCADE;
DROP TABLE IF EXISTS request_sla_config CASCADE;
DROP TABLE IF EXISTS request_rate_limits CASCADE;
DROP INDEX IF EXISTS idx_request_notifications_cleanup;
DROP TABLE IF EXISTS request_notifications CASCADE;
DROP TABLE IF EXISTS request_approvals CASCADE;
DROP TABLE IF EXISTS request_required_approvers CASCADE;
DROP TABLE IF EXISTS request_type_schemas CASCADE;
DROP TABLE IF EXISTS request_status_history CASCADE;
DROP TABLE IF EXISTS requests CASCADE;

DROP TYPE IF EXISTS sla_violation_type_enum CASCADE;
DROP TYPE IF EXISTS delivery_channel_enum CASCADE;
DROP TYPE IF EXISTS notification_type_enum CASCADE;
DROP TYPE IF EXISTS approval_action_enum CASCADE;
DROP TYPE IF EXISTS request_status_enum CASCADE;

-- +goose StatementEnd
