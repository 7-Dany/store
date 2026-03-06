-- +goose Up
-- +goose StatementBegin

-- 005_requests.sql — request workflow schema: requests, approvals, executions,
-- notifications, rate limits, SLA config, and SLA violations.
-- Depends on: 001_core.sql (users), 003_rbac.sql (roles)


-- ------------------------------------------------------------
-- ENUMS
-- ------------------------------------------------------------

-- Valid flow: pending → approved → executing → completed | failed.
-- Terminal states (rejected, cancelled, completed, failed) are immutable — enforced by trigger.
CREATE TYPE request_status_enum AS ENUM (
    'pending',
    'approved',
    'rejected',
    'cancelled',
    'executing',
    'completed',
    'failed'
);

COMMENT ON TYPE request_status_enum IS
    'Request lifecycle states. Terminal: rejected, cancelled, completed, failed.';

CREATE TYPE approval_action_enum AS ENUM (
    'approved',
    'rejected'
);

COMMENT ON TYPE approval_action_enum IS
    'Actions an approver can take on a pending request.';

-- Terminal values: success, failed. retrying → in_progress on next attempt.
CREATE TYPE execution_status_enum AS ENUM (
    'pending',
    'in_progress',
    'success',
    'failed',
    'retrying'
);

COMMENT ON TYPE execution_status_enum IS
    'Status of a single execution attempt. Terminal: success, failed.';

CREATE TYPE notification_type_enum AS ENUM (
    'request_created',
    'request_approved',
    'request_rejected',
    'request_executed',
    'request_failed',
    'request_cancelled'
);

COMMENT ON TYPE notification_type_enum IS
    'Events that produce a notification row in request_notifications.';

CREATE TYPE delivery_channel_enum AS ENUM (
    'email',
    'sms',
    'push',
    'in_app'
);

COMMENT ON TYPE delivery_channel_enum IS
    'Delivery channels for request_notifications. One row per event per channel.';


-- ------------------------------------------------------------
-- REQUESTS
-- ------------------------------------------------------------

CREATE TABLE requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    request_type VARCHAR(100) NOT NULL,
    requester_id UUID REFERENCES users(id) ON DELETE SET NULL,

    status request_status_enum NOT NULL DEFAULT 'pending',

    request_data JSONB NOT NULL,

    priority           INTEGER DEFAULT 0,
    approvals_required INTEGER DEFAULT 1,

    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW(),
    resolved_at TIMESTAMPTZ,

    CONSTRAINT chk_requests_data_is_object
        CHECK (jsonb_typeof(request_data) = 'object'),
    CONSTRAINT chk_requests_approvals_positive
        CHECK (approvals_required > 0)
);

CREATE INDEX idx_requests_status_pending   ON requests(status) WHERE status = 'pending';
CREATE INDEX idx_requests_status_all       ON requests(status);
CREATE INDEX idx_requests_requester        ON requests(requester_id);
CREATE INDEX idx_requests_type             ON requests(request_type);
CREATE INDEX idx_requests_created          ON requests(created_at DESC);
CREATE INDEX idx_requests_priority         ON requests(priority DESC, created_at DESC);
CREATE INDEX idx_requests_data             ON requests USING GIN(request_data);
CREATE INDEX idx_requests_user_pending     ON requests(requester_id, status) WHERE status = 'pending';
CREATE INDEX idx_requests_expired          ON requests(expires_at) WHERE status = 'pending' AND expires_at IS NOT NULL;
-- Approval queue: ordered by urgency then submission time.
CREATE INDEX idx_requests_approval_queue   ON requests(status, priority DESC, created_at ASC) WHERE status = 'pending';

COMMENT ON TABLE  requests IS
    'Approval requests with flexible JSONB payloads and quorum support.';
COMMENT ON COLUMN requests.request_type IS
    'Discriminator: product_creation, vendor_withdrawal, etc.';
COMMENT ON COLUMN requests.request_data IS
    'Flexible payload. Structure validated against request_type_schemas.';
COMMENT ON COLUMN requests.priority IS
    'Higher = more urgent. Used to order the approval queue.';
COMMENT ON COLUMN requests.approvals_required IS
    'Number of approvals needed before the request transitions to approved.';
COMMENT ON COLUMN requests.expires_at IS
    'Auto-reject if still pending past this time.';


-- ------------------------------------------------------------
-- REQUEST TYPE SCHEMAS
-- ------------------------------------------------------------

CREATE TABLE request_type_schemas (
    request_type VARCHAR(100) PRIMARY KEY,
    json_schema  JSONB NOT NULL,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    CONSTRAINT chk_rts_schema_is_object
        CHECK (jsonb_typeof(json_schema) = 'object')
);

COMMENT ON TABLE  request_type_schemas IS
    'JSON Schema validation rules for request_data keyed by request_type.';
COMMENT ON COLUMN request_type_schemas.json_schema IS
    'JSON Schema defining required fields, types, and constraints for this request_type.';


-- ------------------------------------------------------------
-- REQUEST REQUIRED APPROVERS
-- ------------------------------------------------------------

CREATE TABLE request_required_approvers (
    request_id UUID REFERENCES requests(id) ON DELETE CASCADE,
    role_id    UUID REFERENCES roles(id)    ON DELETE CASCADE,

    approval_level              INTEGER DEFAULT 0,
    min_required                INTEGER DEFAULT 1,
    must_complete_previous_level BOOLEAN DEFAULT FALSE,

    conditions JSONB DEFAULT '{}',

    created_at TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (request_id, role_id),

    CONSTRAINT chk_rra_conditions_is_object
        CHECK (jsonb_typeof(conditions) = 'object'),
    CONSTRAINT chk_rra_min_required_positive
        CHECK (min_required > 0),
    CONSTRAINT chk_rra_approval_level_non_negative
        CHECK (approval_level >= 0)
);

CREATE INDEX idx_request_approvers_request ON request_required_approvers(request_id);
CREATE INDEX idx_request_approvers_role    ON request_required_approvers(role_id);
CREATE INDEX idx_request_approvers_level   ON request_required_approvers(request_id, approval_level);
-- Covering index for approval eligibility checks.
CREATE INDEX idx_requests_user_can_approve ON request_required_approvers(role_id, request_id)
    INCLUDE (approval_level, min_required, conditions);

COMMENT ON TABLE  request_required_approvers IS
    'Which roles can approve each request, with multi-level hierarchy support.';
COMMENT ON COLUMN request_required_approvers.approval_level IS
    'Hierarchical level: 0 = first, 1 = second, etc.';
COMMENT ON COLUMN request_required_approvers.must_complete_previous_level IS
    'TRUE = all lower levels must be satisfied before this level can act.';


-- ------------------------------------------------------------
-- REQUEST APPROVALS
-- ------------------------------------------------------------

CREATE TABLE request_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    request_id  UUID REFERENCES requests(id) ON DELETE CASCADE,
    approver_id UUID REFERENCES users(id)    ON DELETE SET NULL,
    role_used   UUID REFERENCES roles(id)    ON DELETE RESTRICT,

    action  approval_action_enum NOT NULL,
    comment TEXT,

    approved_at TIMESTAMPTZ DEFAULT NOW(),

    -- Prevents a race condition where the same user approves twice.
    CONSTRAINT uq_approval_per_user_request UNIQUE (request_id, approver_id)
);

CREATE INDEX idx_request_approvals_request      ON request_approvals(request_id);
CREATE INDEX idx_request_approvals_approver      ON request_approvals(approver_id);
CREATE INDEX idx_request_approvals_recent        ON request_approvals(approved_at DESC);
CREATE INDEX idx_request_approvals_role          ON request_approvals(role_used);
CREATE INDEX idx_request_approvals_approver_date ON request_approvals(approver_id, approved_at DESC);
-- Count of approvals per request.
CREATE INDEX idx_request_approvals_count         ON request_approvals(request_id, action) WHERE action = 'approved';

COMMENT ON TABLE  request_approvals IS
    'Immutable audit trail of approval/rejection actions.';
COMMENT ON COLUMN request_approvals.role_used IS
    'Role that authorised this approval. RESTRICT preserves the reference even if the role is soft-deleted.';


-- ------------------------------------------------------------
-- REQUEST EXECUTIONS
-- ------------------------------------------------------------

CREATE TABLE request_executions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID REFERENCES requests(id) ON DELETE CASCADE,

    idempotency_key  UUID UNIQUE,
    executed_action  VARCHAR(100) NOT NULL,
    execution_result JSONB,

    status        execution_status_enum NOT NULL DEFAULT 'pending',
    error_message TEXT,
    retry_count   INTEGER DEFAULT 0,

    executed_by UUID REFERENCES users(id) ON DELETE SET NULL,

    started_at   TIMESTAMPTZ DEFAULT NOW(),
    completed_at TIMESTAMPTZ,

    CONSTRAINT chk_re_result_is_object
        CHECK (execution_result IS NULL OR jsonb_typeof(execution_result) = 'object'),
    CONSTRAINT uq_re_idempotency_per_request
        UNIQUE (request_id, idempotency_key)
);

CREATE INDEX idx_request_executions_request       ON request_executions(request_id);
CREATE INDEX idx_request_executions_status        ON request_executions(status);
CREATE INDEX idx_request_executions_started       ON request_executions(started_at DESC);
CREATE INDEX idx_request_executions_failed        ON request_executions(status) WHERE status = 'failed';
CREATE INDEX idx_request_executions_action_failed ON request_executions(executed_action, started_at DESC) WHERE status = 'failed';
-- Stalled-job detection: pending/in_progress/retrying rows sorted by age.
CREATE INDEX idx_request_executions_stuck         ON request_executions(status, started_at) WHERE status IN ('pending', 'in_progress', 'retrying');

COMMENT ON TABLE  request_executions IS
    'Tracks execution of approved requests with retry support and idempotency.';
COMMENT ON COLUMN request_executions.idempotency_key IS
    'Prevents duplicate execution attempts for the same request.';
COMMENT ON COLUMN request_executions.executed_action IS
    'What action was taken, e.g. product_created, payment_sent.';
COMMENT ON COLUMN request_executions.execution_result IS
    'JSONB result data: IDs, hashes, metadata, etc.';
COMMENT ON COLUMN request_executions.completed_at IS
    'Set when status transitions to success or failed.';


-- ------------------------------------------------------------
-- REQUEST NOTIFICATIONS
-- ------------------------------------------------------------

CREATE TABLE request_notifications (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID REFERENCES requests(id) ON DELETE CASCADE,
    user_id    UUID REFERENCES users(id)    ON DELETE CASCADE,

    notification_type notification_type_enum NOT NULL,
    delivery_channel  delivery_channel_enum,

    sent_at           TIMESTAMPTZ,
    read_at           TIMESTAMPTZ,
    delivery_attempts INTEGER DEFAULT 0,
    last_attempt_at   TIMESTAMPTZ,
    delivery_error    TEXT,

    title   VARCHAR(255),
    message TEXT,

    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_request_notifications_user        ON request_notifications(user_id, read_at);
CREATE INDEX idx_request_notifications_request     ON request_notifications(request_id);
-- Delivery worker: unsent rows.
CREATE INDEX idx_request_notifications_unsent      ON request_notifications(sent_at) WHERE sent_at IS NULL;
-- Unread inbox query.
CREATE INDEX idx_request_notifications_user_unread ON request_notifications(user_id, created_at DESC) WHERE read_at IS NULL;
-- Retry candidates: rows with a delivery error.
CREATE INDEX idx_request_notifications_failed      ON request_notifications(delivery_attempts, last_attempt_at DESC) WHERE delivery_error IS NOT NULL;

COMMENT ON TABLE  request_notifications IS
    'Notification queue for request status changes with per-channel delivery tracking.';
COMMENT ON COLUMN request_notifications.sent_at IS
    'NULL = queued, non-NULL = delivered.';
COMMENT ON COLUMN request_notifications.read_at IS
    'NULL = unread. Set when the user views the notification.';


-- ------------------------------------------------------------
-- REQUEST RATE LIMITS
-- ------------------------------------------------------------

CREATE TABLE request_rate_limits (
    user_id      UUID         REFERENCES users(id) ON DELETE CASCADE,
    request_type VARCHAR(100),

    requests_created_count INTEGER     DEFAULT 0,
    window_start           TIMESTAMPTZ DEFAULT NOW(),
    last_request_at        TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (user_id, request_type)
);

CREATE INDEX idx_rate_limits_window ON request_rate_limits(user_id, window_start);

COMMENT ON TABLE  request_rate_limits IS
    'Per-user, per-type sliding window counters for request creation rate limiting.';
COMMENT ON COLUMN request_rate_limits.window_start IS
    'Start of the current rate-limit window. Reset when the window expires.';


-- ------------------------------------------------------------
-- REQUEST SLA CONFIG
-- ------------------------------------------------------------

CREATE TABLE request_sla_config (
    request_type VARCHAR(100) PRIMARY KEY,

    pending_max_hours        INTEGER NOT NULL,
    execution_max_minutes    INTEGER NOT NULL,

    notify_approaching_sla          BOOLEAN DEFAULT TRUE,
    approaching_threshold_percent   INTEGER DEFAULT 80,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    CONSTRAINT chk_sla_pending_max_positive
        CHECK (pending_max_hours > 0),
    CONSTRAINT chk_sla_execution_max_positive
        CHECK (execution_max_minutes > 0),
    CONSTRAINT chk_sla_threshold_range
        CHECK (approaching_threshold_percent > 0 AND approaching_threshold_percent <= 100)
);

COMMENT ON TABLE  request_sla_config IS
    'SLA thresholds per request_type for alerting and violation detection.';
COMMENT ON COLUMN request_sla_config.approaching_threshold_percent IS
    'Alert is triggered when this percentage of the SLA window has elapsed.';


-- ------------------------------------------------------------
-- REQUEST SLA VIOLATIONS
-- ------------------------------------------------------------

CREATE TABLE request_sla_violations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID REFERENCES requests(id) ON DELETE CASCADE,

    violation_type VARCHAR(50) NOT NULL,
    detected_at    TIMESTAMPTZ DEFAULT NOW(),
    resolved_at    TIMESTAMPTZ,
    notified       BOOLEAN DEFAULT FALSE,

    CONSTRAINT chk_sla_violation_type
        CHECK (violation_type IN ('pending_timeout', 'execution_timeout'))
);

CREATE INDEX idx_sla_violations_request    ON request_sla_violations(request_id);
-- Unresolved violations for monitoring queries.
CREATE INDEX idx_sla_violations_unresolved ON request_sla_violations(violation_type, detected_at DESC) WHERE resolved_at IS NULL;

COMMENT ON TABLE  request_sla_violations IS
    'Records SLA breaches for monitoring, alerting, and reporting.';
COMMENT ON COLUMN request_sla_violations.violation_type IS
    'pending_timeout = request sat in pending too long; execution_timeout = execution took too long.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS request_sla_violations     CASCADE;
DROP TABLE IF EXISTS request_sla_config         CASCADE;
DROP TABLE IF EXISTS request_rate_limits        CASCADE;
DROP TABLE IF EXISTS request_notifications      CASCADE;
DROP TABLE IF EXISTS request_executions         CASCADE;
DROP TABLE IF EXISTS request_approvals          CASCADE;
DROP TABLE IF EXISTS request_required_approvers CASCADE;
DROP TABLE IF EXISTS request_type_schemas       CASCADE;
DROP TABLE IF EXISTS requests                   CASCADE;

DROP TYPE IF EXISTS delivery_channel_enum   CASCADE;
DROP TYPE IF EXISTS notification_type_enum  CASCADE;
DROP TYPE IF EXISTS execution_status_enum   CASCADE;
DROP TYPE IF EXISTS approval_action_enum    CASCADE;
DROP TYPE IF EXISTS request_status_enum     CASCADE;

-- +goose StatementEnd
