// Package rbac provides RBAC permission checking, middleware, and context helpers
// for the store API. The Checker performs DB-backed permission lookups and exposes
// chi-compatible middleware (Require and ApprovalGate) for use in domain routers.
package rbac

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ── Permission constants ──────────────────────────────────────────────────────

// Permission canonical names. Use these constants everywhere — never raw string literals.
const (
	PermRBACRead           = "rbac:read"
	PermRBACManage         = "rbac:manage"
	PermRBACGrantUserPerm  = "rbac:grant_user_permission"
	PermJobQueueRead       = "job_queue:read"
	PermJobQueueManage     = "job_queue:manage"
	PermJobQueueConfigure  = "job_queue:configure"
	PermUserRead           = "user:read"
	PermUserManage         = "user:manage"
	PermUserLock           = "user:lock"
	PermRequestRead        = "request:read"
	PermRequestManage      = "request:manage"
	PermRequestApprove     = "request:approve"
	PermProductManage      = "product:manage"

	// Bitcoin payment domain permissions.
	// Stage 0: not yet enforced — all authenticated users may access bitcoin endpoints.
	// Stage 2+: apply rbac.Require(rbac.PermBitcoinWatch) to POST /watch and GET /events.
	PermBitcoinWatch  = "bitcoin:watch"   // register addresses for SSE notification
	PermBitcoinStatus = "bitcoin:status"  // read ZMQ subscriber health (GET /status)
	PermBitcoinManage = "bitcoin:manage"  // admin: adjust watch limits, flush caches
)

// ── ConditionalEscalator ──────────────────────────────────────────────────────

// ConditionalEscalator is implemented by the requests domain.
// Domain handlers call it when a conditional permission's constraints are not met
// and the action should be queued for approval rather than rejected outright.
// Defining the interface here keeps platform/rbac free of any import dependency
// on the requests domain — dependency always flows toward platform, never away.
type ConditionalEscalator interface {
	// EscalateConditional submits an approval request on behalf of userID for the
	// given permission. The full *http.Request is passed so the implementation can
	// capture path params, query params, and body for later replay — the same
	// canonical shape used by ApprovalSubmitter.
	//
	// The implementation sets request_type = "permission_action" and stamps
	// "reason": "conditional_limit_exceeded" into request_data so the approver UI
	// can distinguish escalated conditional requests from native request-type actions.
	//
	// The caller (domain handler) is responsible for writing the 202 response.
	EscalateConditional(ctx context.Context, userID, permission string, r *http.Request) (requestID string, err error)
}

// ── ApprovalSubmitter ─────────────────────────────────────────────────────────

// ApprovalSubmitter is implemented by the requests domain.
// ApprovalGate calls it when a permission has access_type = "request".
// Defining the interface here keeps platform/rbac free of any import dependency
// on the requests domain — the requests domain depends on platform/rbac, not vice versa.
type ApprovalSubmitter interface {
	// SubmitPermissionApproval creates a pending approval request for the given
	// user and permission. The full *http.Request is passed so the implementation
	// can capture everything needed to replay the action once approved:
	// chi path params, query params, and the request body.
	//
	// Reading the body is safe here because ApprovalGate never calls next on the
	// approval path — the guarded handler is not invoked, so the body stream is
	// unconsumed. If the implementation reads the body it must also close it.
	//
	// The implementation is responsible for:
	//   — building request_data JSONB (see canonical shape below)
	//   — inserting a requests row (request_type = "permission_action")
	//   — populating request_required_approvers from permission_request_approvers
	//     for this permission
	// Returns the new request's UUID string on success.
	SubmitPermissionApproval(ctx context.Context, userID, permission string, r *http.Request) (requestID string, err error)
}

// ── Checker ───────────────────────────────────────────────────────────────────

// Checker performs RBAC permission checks against the database.
// All methods are safe for concurrent use from multiple goroutines.
// Construct once at server startup via NewChecker and store in app.Deps.
type Checker struct {
	q db.Querier
}

var log = telemetry.New("rbac")

// NewChecker constructs a Checker backed by the given Querier.
// Panics if q is nil — misconfiguration must be caught at startup.
func NewChecker(q db.Querier) *Checker {
	if q == nil {
		panic("rbac.NewChecker: querier must not be nil")
	}
	return &Checker{q: q}
}

// ── Unexported type helpers ───────────────────────────────────────────────────

// asBool extracts a bool from the any returned by COALESCE(…, FALSE) columns.
// pgx decodes SQL boolean as Go bool; nil means the column was NULL before COALESCE.
func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// asString extracts a string from the any returned by pgx for custom ENUM columns.
// pgx decodes custom ENUMs as plain string at runtime. Returns fallback when absent.
func asString(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}

// asBytes extracts a []byte from the any returned by pgx for JSONB columns.
// pgx v5 may decode JSONB into []byte, string, or map[string]interface{} depending
// on the wire protocol and driver configuration. All three are handled:
//   - []byte  — binary protocol or direct byte scan
//   - string  — text protocol
//   - other   — JSON re-encode (covers map[string]interface{} and similar)
//
// Returns fallback when v is nil or cannot be marshalled.
func asBytes(v any, fallback []byte) []byte {
	switch t := v.(type) {
	case []byte:
		return t
	case string:
		return []byte(t)
	default:
		if v != nil {
			if b, err := json.Marshal(v); err == nil {
				return b
			}
		}
	}
	return fallback
}

// parseUUID converts a string userID to pgtype.UUID.
// Returns pgtype.UUID{} (Valid=false) on failure — CheckUserAccess returns no rows,
// yielding is_owner=false, has_permission=false.
func parseUUID(s string) pgtype.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

// ── Methods ───────────────────────────────────────────────────────────────────

// IsOwner reports whether userID holds the active owner role.
// Returns (false, nil) for any non-owner user, unknown user IDs, and parse failures.
func (c *Checker) IsOwner(ctx context.Context, userID string) (bool, error) {
	uid := parseUUID(userID)
	if !uid.Valid {
		return false, nil
	}
	row, err := c.q.CheckUserAccess(ctx, db.CheckUserAccessParams{
		UserID:     uid,
		Permission: pgtype.Text{String: "", Valid: true},
	})
	if err != nil {
		return false, telemetry.RBAC("IsOwner.db_check", err)
	}
	return asBool(row.IsOwner), nil
}

// HasPermission reports whether userID holds the given canonical permission
// via role or direct grant, and is not explicitly denied.
// Returns (false, nil) for unknown user IDs and expired grants.
func (c *Checker) HasPermission(ctx context.Context, userID, permission string) (bool, error) {
	uid := parseUUID(userID)
	if !uid.Valid {
		return false, nil
	}
	row, err := c.q.CheckUserAccess(ctx, db.CheckUserAccessParams{
		UserID:     uid,
		Permission: pgtype.Text{String: permission, Valid: true},
	})
	if err != nil {
		return false, telemetry.RBAC("HasPermission.db_check", err)
	}
	if asBool(row.IsOwner) {
		return true, nil
	}
	return row.HasPermission.Bool && row.HasPermission.Valid && !row.IsExplicitlyDenied, nil
}

// Require returns chi-compatible middleware that enforces the named permission.
// It is intentionally minimal: its only job is to check permissions and inject
// the AccessResult into context. It does NOT handle approval submission —
// compose ApprovalGate after Require for routes that need that behaviour.
//
// Prerequisites:
//
//	token.Auth must run before Require — it injects the userID.
//
// Guard order (implement exactly in this sequence):
//  1. token.UserIDFromContext — empty/missing → 401 authentication_required
//  2. HasPermissionInContext  — test hook; short-circuits DB if a test set is present
//  3. c.q.CheckUserAccess    — DB error → slog.ErrorContext + 500; fails closed (D-R11)
//  4. asBool(row.IsOwner)    → inject AccessResult{IsOwner:true, Scope:"all"}; call next
//  5. row.IsExplicitlyDenied → 403 forbidden
//  6. !row.HasPermission     → 403 forbidden
//  7. switch asString(row.AccessType, "direct"):
//     "denied"                      → 403 forbidden
//     "request"                     → inject AccessResult{AccessType:"request"}; call next
//     "direct" | "conditional" | _  → inject full AccessResult; call next
//
// access_type = "request" does NOT produce a 202 here. Require injects the
// AccessResult and calls next — ApprovalGate (chained after) intercepts and returns 202.
//
// Require never reads r.Body — it only inspects the Authorization header via
// token.UserIDFromContext. The body stream is left intact for ApprovalGate's
// submitter to read if the approval path is taken.
func (c *Checker) Require(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Extract user ID from context — set by token.Auth.
			userID, ok := token.UserIDFromContext(r.Context())
			if !ok || userID == "" {
				respond.Error(w, http.StatusUnauthorized, "authentication_required",
					"authentication is required")
				return
			}

			// 2. Test hook — short-circuit DB when injected permission set is present.
			if allowed, found := HasPermissionInContext(r.Context(), permission); found {
				if !allowed {
					respond.Error(w, http.StatusForbidden, "forbidden",
						"insufficient permissions")
					return
				}
				ctx := injectAccessResult(r.Context(), &AccessResult{
					Permission:    permission,
					HasPermission: true,
					AccessType:    "direct",
					Scope:         "all",
					Conditions:    json.RawMessage("{}"),
				})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 3. DB permission check — fail closed on error.
			uid := parseUUID(userID)
			row, err := c.q.CheckUserAccess(r.Context(), db.CheckUserAccessParams{
				UserID:     uid,
				Permission: pgtype.Text{String: permission, Valid: true},
			})
			if err != nil {
				log.Error(r.Context(), "Require: db check failed", "error", err)
				respond.Error(w, http.StatusInternalServerError, "internal_error",
					"internal server error")
				return
			}

			// 4. Owner bypass — owner has unrestricted access to all permissions.
			if asBool(row.IsOwner) {
				ctx := injectAccessResult(r.Context(), &AccessResult{
					Permission:    permission,
					IsOwner:       true,
					HasPermission: true,
					AccessType:    "direct",
					Scope:         "all",
					Conditions:    json.RawMessage("{}"),
				})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 5. Explicit denial — denied role grant takes priority over all.
			if row.IsExplicitlyDenied {
				respond.Error(w, http.StatusForbidden, "forbidden",
					"insufficient permissions")
				return
			}

			// 6. No permission at all.
			if !row.HasPermission.Bool || !row.HasPermission.Valid {
				respond.Error(w, http.StatusForbidden, "forbidden",
					"insufficient permissions")
				return
			}

			// 7. Route by access_type.
			accessType := asString(row.AccessType, "direct")
			switch accessType {
			case "denied":
				respond.Error(w, http.StatusForbidden, "forbidden",
					"insufficient permissions")
				return
			default:
				// "direct", "conditional", "request" — inject full AccessResult and call next.
				// ApprovalGate (chained after) handles the "request" path.
				condBytes := asBytes(row.Conditions, []byte("{}"))
				ctx := injectAccessResult(r.Context(), &AccessResult{
					Permission:    permission,
					IsOwner:       false,
					HasPermission: true,
					AccessType:    accessType,
					Scope:         asString(row.Scope, "own"),
					Conditions:    json.RawMessage(condBytes),
				})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		})
	}
}

// ApprovalGate returns middleware that intercepts requests where the AccessResult
// injected by Require has AccessType == "request", calls the submitter to create
// an approval request, then returns 202 Accepted. The guarded handler is NOT called —
// it must not run until the approval is granted and the requests processor executes it.
//
// For all other access types (direct, conditional, owner) it is a no-op passthrough.
// Chain ApprovalGate immediately after Require on routes where approval may apply:
//
//	r.With(
//	    deps.JWTAuth,
//	    deps.RBAC.Require(rbac.PermJobQueueConfigure),
//	    deps.RBAC.ApprovalGate(deps.ApprovalSubmitter),
//	).Post("/queues/{kind}/pause", h.PauseKind)
//
// Routes without any 'request'-type permissions may omit ApprovalGate entirely.
//
// Behaviour:
//
//	AccessType != "request"  → call next unchanged (body untouched)
//	submitter is nil         → 503 approval_unavailable (safe until requests domain is wired)
//	submitter returns error  → slog.ErrorContext + 500 internal_error
//	submitter succeeds       → 202 {"code":"approval_required","request_id":"<uuid>","message":"..."}
//
// ApprovalGate passes the original *http.Request to SubmitPermissionApproval so the
// requests domain can capture path params, query params, and body for later replay.
// The body is safe to read because next is never called on the approval path —
// the guarded handler is not invoked and the body stream is unconsumed.
//
// ApprovalGate does not read or buffer the body itself — that is the submitter's
// responsibility. If the submitter reads the body it must also close it.
func (c *Checker) ApprovalGate(submitter ApprovalSubmitter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			result := AccessResultFromContext(r.Context())
			// If no access result or not a request-type permission, pass through.
			if result == nil || result.AccessType != "request" {
				next.ServeHTTP(w, r)
				return
			}

			// Submitter not yet wired — safe 503 until requests domain is ready.
			if submitter == nil {
				respond.Error(w, http.StatusServiceUnavailable, "approval_unavailable",
					"approval submission is not available yet")
				return
			}

			userID, _ := token.UserIDFromContext(r.Context())
			requestID, err := submitter.SubmitPermissionApproval(
				r.Context(), userID, result.Permission, r,
			)
			if err != nil {
				log.Error(r.Context(), "ApprovalGate: submit approval failed",
					"error", err)
				respond.Error(w, http.StatusInternalServerError, "internal_error",
					"internal server error")
				return
			}

			respond.JSON(w, http.StatusAccepted, map[string]any{
				"code":       "approval_required",
				"request_id": requestID,
				"message":    "this action requires approval — a request has been submitted",
			})
		})
	}
}
