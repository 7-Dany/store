//go:build integration_test

package rbac_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/db"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── TestMain ─────────────────────────────────────────────────────────────────

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	authsharedtest.RunTestMain(m, &testPool, 20)
}

// ── Fake Querier (unit tests) ─────────────────────────────────────────────────

type fakeQuerier struct {
	db.Querier // embedded — panics on unexpected calls
	row        db.CheckUserAccessRow
	err        error
}

func (f *fakeQuerier) CheckUserAccess(_ context.Context, _ db.CheckUserAccessParams) (db.CheckUserAccessRow, error) {
	return f.row, f.err
}

// ── Fake ApprovalSubmitter ────────────────────────────────────────────────────

type fakeSubmitter struct {
	requestID     string
	err           error
	called        bool
	gotUserID     string
	gotPermission string
	gotRequest    *http.Request
}

func (f *fakeSubmitter) SubmitPermissionApproval(_ context.Context, userID, permission string, r *http.Request) (string, error) {
	f.called = true
	f.gotUserID = userID
	f.gotPermission = permission
	f.gotRequest = r
	return f.requestID, f.err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// nextHandler records whether it was called.
func nextHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

// authedRequest returns a request with userID injected into context (simulates token.Auth).
func authedRequest(userID string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := token.InjectUserIDForTest(r.Context(), userID)
	return r.WithContext(ctx)
}

// toPgtypeUUID is a local helper for tests.
func toPgtypeUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// ── Integration seed helpers ──────────────────────────────────────────────────

// seedRole creates a non-owner, non-system test role and returns its UUID.
func seedRole(t *testing.T, q db.Querier) uuid.UUID {
	t.Helper()
	role, err := q.CreateRole(context.Background(), db.CreateRoleParams{
		Name:        "test-role-" + uuid.NewString()[:8],
		Description: pgtype.Text{},
	})
	require.NoError(t, err)
	return role.ID
}

// seedPermission fetches the first active permission from the DB.
// Requires seeds to have been applied.
func seedPermission(t *testing.T, q db.Querier) uuid.UUID {
	t.Helper()
	perms, err := q.GetPermissions(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, perms, "no permissions seeded — run sql/seeds before integration tests")
	return perms[0].ID
}

// seedPermissionByName returns the UUID of a named permission.
func seedPermissionByName(t *testing.T, q db.Querier, canonical string) uuid.UUID {
	t.Helper()
	row, err := q.GetPermissionByCanonicalName(context.Background(), pgtype.Text{String: canonical, Valid: true})
	require.NoError(t, err)
	return row.ID
}

// assignRole assigns roleID to userID with a permanent grant.
func assignRole(t *testing.T, q db.Querier, userID, roleID uuid.UUID) {
	t.Helper()
	_, err := q.AssignUserRole(context.Background(), db.AssignUserRoleParams{
		UserID:        toPgtypeUUID(userID),
		RoleID:        toPgtypeUUID(roleID),
		GrantedBy:     toPgtypeUUID(userID), // self-grant for tests
		GrantedReason: "integration test",
		ExpiresAt:     pgtype.Timestamptz{}, // NULL = permanent
	})
	require.NoError(t, err)
}

// addRolePermission adds permID to roleID with the given access_type and scope.
func addRolePermission(t *testing.T, q db.Querier, roleID, permID, grantedBy uuid.UUID, accessType db.PermissionAccessType, scope db.PermissionScope, conditions []byte) {
	t.Helper()
	if conditions == nil {
		conditions = []byte("{}")
	}
	_, err := q.AddRolePermission(context.Background(), db.AddRolePermissionParams{
		RoleID:        toPgtypeUUID(roleID),
		PermissionID:  toPgtypeUUID(permID),
		GrantedBy:     toPgtypeUUID(grantedBy),
		GrantedReason: "integration test",
		AccessType:    accessType,
		Scope:         scope,
		Conditions:    conditions,
	})
	require.NoError(t, err)
}

// createUserWithPermission creates a user, creates a role, assigns perm to role,
// assigns role to user, and returns (userIDStr, permCanonical).
func createUserWithRolePerm(t *testing.T, pool *pgxpool.Pool, q db.Querier, canonical string, accessType db.PermissionAccessType, scope db.PermissionScope, cond []byte) (string, uuid.UUID) {
	t.Helper()
	u := authsharedtest.CreateUser(t, pool, q, authsharedtest.NewEmail(t))
	userID, err := uuid.Parse(u.UserID)
	require.NoError(t, err)

	permID := seedPermissionByName(t, q, canonical)
	roleID := seedRole(t, q)
	addRolePermission(t, q, roleID, permID, userID, accessType, scope, cond)
	assignRole(t, q, userID, roleID)
	return u.UserID, permID
}

// ── T-R01 (unit) — Require passes for owner regardless of permission ──────────

func TestRequire_OwnerBypassesPermissionCheck(t *testing.T) {
	fq := &fakeQuerier{
		row: db.CheckUserAccessRow{
			IsOwner:            true,
			IsExplicitlyDenied: false,
			HasPermission:      pgtype.Bool{Bool: false, Valid: true},
			AccessType:         "direct",
			Scope:              "all",
			Conditions:         []byte("{}"),
		},
	}
	checker := rbac.NewChecker(fq)

	var nextCalled bool
	r := authedRequest(uuid.New().String())
	w := httptest.NewRecorder()

	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "owner should receive 200")
	assert.True(t, nextCalled, "next must be called for owner")

	result := rbac.AccessResultFromContext(r.Context())
	// Note: result is on the *modified* context inside the middleware; we assert via next handler below.
	_ = result
}

// TestRequire_OwnerBypassesPermissionCheck_ContextInjected validates the injected AccessResult.
func TestRequire_OwnerAccessResultInjected(t *testing.T) {
	fq := &fakeQuerier{
		row: db.CheckUserAccessRow{
			IsOwner:            true,
			IsExplicitlyDenied: false,
			HasPermission:      pgtype.Bool{Bool: false, Valid: true},
			AccessType:         "direct",
			Scope:              "all",
			Conditions:         []byte("{}"),
		},
	}
	checker := rbac.NewChecker(fq)

	var capturedCtx context.Context
	captureNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	r := authedRequest(uuid.New().String())
	w := httptest.NewRecorder()
	checker.Require(rbac.PermRBACRead)(captureNext).ServeHTTP(w, r)

	result := rbac.AccessResultFromContext(capturedCtx)
	require.NotNil(t, result)
	assert.True(t, result.IsOwner)
	assert.Equal(t, "all", result.Scope)
}

// ── T-R08 (unit) — Require returns 401 when no userID in context ─────────────

func TestRequire_NoUserID_Returns401(t *testing.T) {
	checker := rbac.NewChecker(&fakeQuerier{})

	var nextCalled bool
	r := httptest.NewRequest(http.MethodGet, "/", nil) // no user ID injected
	w := httptest.NewRecorder()

	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, nextCalled)
	assert.Contains(t, w.Body.String(), "authentication_required")
}

// ── T-R09 (unit) — Require uses test-injected permissions; no DB hit ──────────

func TestRequire_TestInjectedPermission_NoDB(t *testing.T) {
	fq := &fakeQuerier{
		err: nil,
		// row intentionally left zero — if CheckUserAccess is called the test will detect it
	}
	checkCalled := false
	fq2 := &trackingQuerier{fq, &checkCalled}

	checker := rbac.NewChecker(fq2)

	var nextCalled bool
	r := authedRequest(uuid.New().String())
	r = r.WithContext(rbac.InjectPermissionsForTest(r.Context(), rbac.PermRBACRead))
	w := httptest.NewRecorder()

	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, nextCalled)
	assert.False(t, checkCalled, "CheckUserAccess must NOT be called when test hook is active")
}

// trackingQuerier wraps a Querier and records whether CheckUserAccess was called.
type trackingQuerier struct {
	db.Querier
	called *bool
}

func (t *trackingQuerier) CheckUserAccess(ctx context.Context, p db.CheckUserAccessParams) (db.CheckUserAccessRow, error) {
	*t.called = true
	return t.Querier.CheckUserAccess(ctx, p)
}

// Also verify that a test-injected set that does NOT contain the permission returns 403.
func TestRequire_TestInjectedPermission_AbsentPermission_Returns403(t *testing.T) {
	checker := rbac.NewChecker(&fakeQuerier{})

	var nextCalled bool
	r := authedRequest(uuid.New().String())
	// Inject a different permission — PermRBACManage — but require PermRBACRead.
	r = r.WithContext(rbac.InjectPermissionsForTest(r.Context(), rbac.PermRBACManage))
	w := httptest.NewRecorder()

	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, nextCalled)
}

// ── T-R10 (unit) — Require returns 500 and fails closed on DB error ──────────

func TestRequire_DBError_Returns500_FailsClosed(t *testing.T) {
	fq := &fakeQuerier{err: assert.AnError}
	checker := rbac.NewChecker(fq)

	var nextCalled bool
	r := authedRequest(uuid.New().String())
	w := httptest.NewRecorder()

	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.False(t, nextCalled, "next must NOT be called when DB error occurs")
	assert.Contains(t, w.Body.String(), "internal_error")
}

// ── T-RAG01 (unit) — access_type="request" → submitter called; 202 returned ──

func TestApprovalGate_RequestType_SubmitterCalled_202(t *testing.T) {
	const perm = rbac.PermJobQueueConfigure
	reqID := uuid.NewString()
	sub := &fakeSubmitter{requestID: reqID}

	fq := &fakeQuerier{
		row: db.CheckUserAccessRow{
			IsOwner:            false,
			IsExplicitlyDenied: false,
			HasPermission:      pgtype.Bool{Bool: true, Valid: true},
			AccessType:         "request",
			Scope:              "all",
			Conditions:         []byte("{}"),
		},
	}
	checker := rbac.NewChecker(fq)

	var nextCalled bool
	userID := uuid.New().String()
	r := authedRequest(userID)
	w := httptest.NewRecorder()

	chain := checker.Require(perm)(checker.ApprovalGate(sub)(nextHandler(&nextCalled)))
	chain.ServeHTTP(w, r)

	assert.Equal(t, http.StatusAccepted, w.Code, "approval path must return 202")
	assert.False(t, nextCalled, "guarded handler must NOT be called on approval path")
	assert.True(t, sub.called, "submitter must be called")
	assert.Equal(t, userID, sub.gotUserID)
	assert.Equal(t, perm, sub.gotPermission, "submitter must receive the canonical permission name")
	assert.NotNil(t, sub.gotRequest, "submitter must receive the *http.Request")

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "approval_required", body["code"])
	assert.Equal(t, reqID, body["request_id"])
}

// ── T-RAG02 (unit) — access_type="direct" → submitter NOT called; next called ─

func TestApprovalGate_DirectType_NextCalled_SubmitterSkipped(t *testing.T) {
	const perm = rbac.PermRBACRead
	sub := &fakeSubmitter{requestID: "should-not-be-used"}

	fq := &fakeQuerier{
		row: db.CheckUserAccessRow{
			IsOwner:            false,
			IsExplicitlyDenied: false,
			HasPermission:      pgtype.Bool{Bool: true, Valid: true},
			AccessType:         "direct",
			Scope:              "all",
			Conditions:         []byte("{}"),
		},
	}
	checker := rbac.NewChecker(fq)

	var nextCalled bool
	r := authedRequest(uuid.New().String())
	w := httptest.NewRecorder()

	chain := checker.Require(perm)(checker.ApprovalGate(sub)(nextHandler(&nextCalled)))
	chain.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, nextCalled)
	assert.False(t, sub.called, "submitter must NOT be called for direct access")
}

// ── T-RAG03 (unit) — nil submitter + access_type="request" → 503 ─────────────

func TestApprovalGate_NilSubmitter_Returns503(t *testing.T) {
	const perm = rbac.PermJobQueueConfigure

	fq := &fakeQuerier{
		row: db.CheckUserAccessRow{
			IsOwner:            false,
			IsExplicitlyDenied: false,
			HasPermission:      pgtype.Bool{Bool: true, Valid: true},
			AccessType:         "request",
			Scope:              "all",
			Conditions:         []byte("{}"),
		},
	}
	checker := rbac.NewChecker(fq)

	var nextCalled bool
	r := authedRequest(uuid.New().String())
	w := httptest.NewRecorder()

	chain := checker.Require(perm)(checker.ApprovalGate(nil)(nextHandler(&nextCalled)))
	chain.ServeHTTP(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.False(t, nextCalled)
	assert.Contains(t, w.Body.String(), "approval_unavailable")
}

// ── T-RAG04 (unit) — submitter errors + access_type="request" → 500 ──────────

func TestApprovalGate_SubmitterError_Returns500(t *testing.T) {
	const perm = rbac.PermJobQueueConfigure
	sub := &fakeSubmitter{err: assert.AnError}

	fq := &fakeQuerier{
		row: db.CheckUserAccessRow{
			IsOwner:            false,
			IsExplicitlyDenied: false,
			HasPermission:      pgtype.Bool{Bool: true, Valid: true},
			AccessType:         "request",
			Scope:              "all",
			Conditions:         []byte("{}"),
		},
	}
	checker := rbac.NewChecker(fq)

	var nextCalled bool
	r := authedRequest(uuid.New().String())
	w := httptest.NewRecorder()

	chain := checker.Require(perm)(checker.ApprovalGate(sub)(nextHandler(&nextCalled)))
	chain.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.False(t, nextCalled)
	assert.Contains(t, w.Body.String(), "internal_error")
}

// ── T-R02 (integration) — Require passes and injects scope for "direct" ───────

func TestRequire_Direct_PassesAndInjectsScope(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	userIDStr, _ := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeDirect, db.PermissionScopeAll, nil)

	checker := rbac.NewChecker(q)

	var capturedCtx context.Context
	captureNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	r := authedRequest(userIDStr)
	w := httptest.NewRecorder()
	checker.Require(rbac.PermRBACRead)(captureNext).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	result := rbac.AccessResultFromContext(capturedCtx)
	require.NotNil(t, result)
	assert.Equal(t, "all", rbac.ScopeFromContext(capturedCtx))
}

// ── T-R03 (integration) — Require passes and injects scope+conditions for "conditional" ─

func TestRequire_Conditional_InjectsScopeAndConditions(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	cond := []byte(`{"min_tier": 2}`)
	userIDStr, _ := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeConditional, db.PermissionScopeOwn, cond)

	checker := rbac.NewChecker(q)

	var capturedCtx context.Context
	captureNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	r := authedRequest(userIDStr)
	w := httptest.NewRecorder()
	checker.Require(rbac.PermRBACRead)(captureNext).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	result := rbac.AccessResultFromContext(capturedCtx)
	require.NotNil(t, result)
	assert.Equal(t, "conditional", result.AccessType)
	rawCond := rbac.ConditionsFromContext(capturedCtx)
	assert.NotEqual(t, `{}`, string(rawCond), "conditions must be non-empty for conditional grant")
}

// ── T-R04 (integration) — Require injects AccessResult and calls next for "request" ─

func TestRequire_Request_InjectsAccessResultCallsNext(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	userIDStr, _ := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeRequest, db.PermissionScopeAll, nil)

	checker := rbac.NewChecker(q)

	var nextCalled bool
	var capturedCtx context.Context
	captureNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		capturedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	r := authedRequest(userIDStr)
	w := httptest.NewRecorder()
	// Require only — no ApprovalGate; next should be called.
	checker.Require(rbac.PermRBACRead)(captureNext).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, nextCalled, "next must be called by Require even for access_type='request'")
	result := rbac.AccessResultFromContext(capturedCtx)
	require.NotNil(t, result)
	assert.Equal(t, "request", result.AccessType, "AccessType must be 'request'")
}

// ── T-R05 (integration) — Require returns 403 for "denied" ───────────────────

func TestRequire_Denied_Returns403(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	userIDStr, _ := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeDenied, db.PermissionScopeOwn, nil)

	checker := rbac.NewChecker(q)

	var nextCalled bool
	r := authedRequest(userIDStr)
	w := httptest.NewRecorder()
	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, nextCalled)
}

// ── T-R05b (integration) — denied role + direct user_permission → 403 (IsExplicitlyDenied fires) ─

// TestRequire_DeniedRolePlusDirect_Returns403 verifies that a 'denied' role grant takes
// absolute priority over a competing 'direct' user_permissions grant for the same permission.
// This is the F-2 regression case: IsExplicitlyDenied must be checked before HasPermission.
func TestRequire_DeniedRolePlusDirect_Returns403(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	tx, q := authsharedtest.MustBeginTx(t, testPool)

	// Create a user with a 'denied' role grant for PermRBACRead.
	userIDStr, permID := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeDenied, db.PermissionScopeOwn, nil)
	userID, err := uuid.Parse(userIDStr)
	require.NoError(t, err)

	// Bypass fn_prevent_privilege_escalation so we can insert a direct grant
	// without the granter needing a role that holds the permission.
	_, err = tx.Exec(context.Background(), "SET LOCAL rbac.skip_escalation_check = '1'")
	require.NoError(t, err)

	// Bypass expiry lead-time check so we can set a near-future expires_at in tests.
	_, err = tx.Exec(context.Background(), "SET LOCAL rbac.min_temp_grant_lead = '1 second'")
	require.NoError(t, err)

	// Also insert a 'direct' user_permissions grant for the same permission.
	// This is the competing grant that the bug allowed to bypass the denied role.
	_, err = q.GrantUserPermission(context.Background(), db.GrantUserPermissionParams{
		UserID:        toPgtypeUUID(userID),
		PermissionID:  toPgtypeUUID(permID),
		GrantedBy:     toPgtypeUUID(userID),
		GrantedReason: "integration test - competing direct grant",
		ExpiresAt:     pgtype.Timestamptz{Time: time.Now().Add(30 * time.Minute), Valid: true},
		Scope:         db.PermissionScopeAll,
		Conditions:    []byte("{}"),
	})
	require.NoError(t, err)

	checker := rbac.NewChecker(q)

	var nextCalled bool
	r := authedRequest(userIDStr)
	w := httptest.NewRecorder()
	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	// Denied role must win over the direct grant — 403, next NOT called.
	assert.Equal(t, http.StatusForbidden, w.Code,
		"denied role grant must override a competing direct user_permissions grant")
	assert.False(t, nextCalled, "next must NOT be called when IsExplicitlyDenied is true")
}

// ── T-R06 (integration) — Require returns 403 when user has no role ──────────

func TestRequire_NoRole_Returns403(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	u := authsharedtest.CreateUser(t, testPool, q, authsharedtest.NewEmail(t))
	checker := rbac.NewChecker(q)

	var nextCalled bool
	r := authedRequest(u.UserID)
	w := httptest.NewRecorder()
	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, nextCalled)
}

// ── T-R07 (integration) — Require returns 403 when direct grant is expired ───

func TestRequire_ExpiredDirectGrant_Returns403(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	tx, q := authsharedtest.MustBeginTx(t, testPool)

	u := authsharedtest.CreateUser(t, testPool, q, authsharedtest.NewEmail(t))
	userID, err := uuid.Parse(u.UserID)
	require.NoError(t, err)

	permID := seedPermissionByName(t, q, rbac.PermRBACRead)

	// Bypass fn_prevent_privilege_escalation — granter has no role in this test fixture.
	_, err = tx.Exec(context.Background(), "SET LOCAL rbac.skip_escalation_check = '1'")
	require.NoError(t, err)

	// Insert a direct grant with future expiry (trigger allows this).
	row, err := q.GrantUserPermission(context.Background(), db.GrantUserPermissionParams{
		UserID:        toPgtypeUUID(userID),
		PermissionID:  toPgtypeUUID(permID),
		GrantedBy:     toPgtypeUUID(userID),
		GrantedReason: "integration test - will be expired",
		ExpiresAt:     pgtype.Timestamptz{Time: time.Now().Add(10 * time.Minute), Valid: true},
		Scope:         db.PermissionScopeAll,
		Conditions:    []byte("{}"),
	})
	require.NoError(t, err)

	// Relax the expiry lead-time check so we can backdate expires_at.
	// fn_validate_user_permission_expiry fires on UPDATE too; setting min_lead
	// to a negative interval makes any past timestamp pass the check.
	_, err = tx.Exec(context.Background(), "SET LOCAL rbac.min_temp_grant_lead = '-2 hours'")
	require.NoError(t, err)

	// Expire it in-place — now allowed because min_lead is relaxed for this tx.
	_, err = tx.Exec(context.Background(),
		"UPDATE user_permissions SET expires_at = NOW() - interval '1 hour' WHERE id = $1",
		row.ID,
	)
	require.NoError(t, err)

	checker := rbac.NewChecker(q)

	var nextCalled bool
	r := authedRequest(u.UserID)
	w := httptest.NewRecorder()
	checker.Require(rbac.PermRBACRead)(nextHandler(&nextCalled)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, nextCalled)
}

// ── T-R11 (integration) — IsOwner returns true for owner-role user ────────────

func TestIsOwner_OwnerUser_ReturnsTrue(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	tx, q := authsharedtest.MustBeginTx(t, testPool)

	u := authsharedtest.CreateUser(t, testPool, q, authsharedtest.NewEmail(t))
	userID, err := uuid.Parse(u.UserID)
	require.NoError(t, err)

	ownerRoleID, err := q.GetOwnerRoleID(context.Background())
	require.NoError(t, err)

	// Bypass fn_prevent_owner_role_escalation — no owner exists yet in this test fixture.
	// 009_rbac_owner_trigger_bypass.sql adds the same rbac.skip_escalation_check hatch used
	// by fn_prevent_privilege_escalation, making the bootstrap case testable.
	_, err = tx.Exec(context.Background(), "SET LOCAL rbac.skip_escalation_check = '1'")
	require.NoError(t, err)

	_, err = tx.Exec(context.Background(),
		`INSERT INTO user_roles (user_id, role_id, granted_by, granted_reason)
		 VALUES ($1, $2, $3, 'integration test owner')
		 ON CONFLICT (user_id) DO UPDATE SET role_id = EXCLUDED.role_id`,
		userID, ownerRoleID, userID,
	)
	require.NoError(t, err)

	checker := rbac.NewChecker(q)
	got, err := checker.IsOwner(context.Background(), u.UserID)
	require.NoError(t, err)
	assert.True(t, got)
}

// ── T-R12 (integration) — IsOwner returns false for non-owner user ────────────

func TestIsOwner_NonOwnerUser_ReturnsFalse(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	u := authsharedtest.CreateUser(t, testPool, q, authsharedtest.NewEmail(t))
	checker := rbac.NewChecker(q)

	got, err := checker.IsOwner(context.Background(), u.UserID)
	require.NoError(t, err)
	assert.False(t, got)
}

// ── T-R13 (integration) — HasPermission returns true via role path ────────────

func TestHasPermission_ViaRole_ReturnsTrue(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	userIDStr, _ := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeDirect, db.PermissionScopeAll, nil)

	checker := rbac.NewChecker(q)
	got, err := checker.HasPermission(context.Background(), userIDStr, rbac.PermRBACRead)
	require.NoError(t, err)
	assert.True(t, got)
}

// ── T-R14 (integration) — HasPermission returns true via direct grant ─────────

func TestHasPermission_ViaDirectGrant_ReturnsTrue(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	tx, q := authsharedtest.MustBeginTx(t, testPool)

	u := authsharedtest.CreateUser(t, testPool, q, authsharedtest.NewEmail(t))
	userID, err := uuid.Parse(u.UserID)
	require.NoError(t, err)

	permID := seedPermissionByName(t, q, rbac.PermRBACRead)

	// Bypass fn_prevent_privilege_escalation — granter has no role in this test fixture.
	_, err = tx.Exec(context.Background(), "SET LOCAL rbac.skip_escalation_check = '1'")
	require.NoError(t, err)

	// Insert a direct grant bypassing the privilege escalation trigger via the escape hatch.
	_, err = tx.Exec(context.Background(),
		`INSERT INTO user_permissions (user_id, permission_id, granted_by, granted_reason, expires_at, scope, conditions)
		 VALUES ($1, $2, $3, 'integration test', NOW() + interval '1 hour', 'all', '{}')`,
		userID, permID, userID,
	)
	require.NoError(t, err)

	checker := rbac.NewChecker(q)
	got, err := checker.HasPermission(context.Background(), u.UserID, rbac.PermRBACRead)
	require.NoError(t, err)
	assert.True(t, got)
}

// ── T-R15 (integration) — HasPermission returns false after role permission removed ─

func TestHasPermission_AfterRolePermRemoved_ReturnsFalse(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	userIDStr, permID := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeDirect, db.PermissionScopeAll, nil)
	userID, err := uuid.Parse(userIDStr)
	require.NoError(t, err)

	// Find the role and remove the permission.
	roleRow, err := q.GetUserRole(context.Background(), toPgtypeUUID(userID))
	require.NoError(t, err)

	_, err = q.RemoveRolePermission(context.Background(), db.RemoveRolePermissionParams{
		RoleID:       roleRow.RoleID,
		PermissionID: toPgtypeUUID(permID),
	})
	require.NoError(t, err)

	checker := rbac.NewChecker(q)
	got, err := checker.HasPermission(context.Background(), userIDStr, rbac.PermRBACRead)
	require.NoError(t, err)
	assert.False(t, got)
}

// ── T-R16 (integration) — ScopeFromContext returns seeded scope ───────────────

func TestRequire_ScopeFromContext_MatchesSeed(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	// Admin user — scope "all".
	adminIDStr, _ := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeDirect, db.PermissionScopeAll, nil)

	// Vendor user — scope "own".
	vendorIDStr, _ := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeDirect, db.PermissionScopeOwn, nil)

	checker := rbac.NewChecker(q)

	runAndCapture := func(userID string) string {
		var capturedCtx context.Context
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
			w.WriteHeader(http.StatusOK)
		})
		r := authedRequest(userID)
		w := httptest.NewRecorder()
		checker.Require(rbac.PermRBACRead)(h).ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		return rbac.ScopeFromContext(capturedCtx)
	}

	assert.Equal(t, "all", runAndCapture(adminIDStr))
	assert.Equal(t, "own", runAndCapture(vendorIDStr))
}

// ── T-R17 (integration) — ConditionsFromContext returns non-{} for conditional ─

func TestRequire_ConditionsFromContext_NonEmptyForConditional(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)

	cond := []byte(`{"region": "eu"}`)
	userIDStr, _ := createUserWithRolePerm(t, testPool, q, rbac.PermRBACRead,
		db.PermissionAccessTypeConditional, db.PermissionScopeOwn, cond)

	checker := rbac.NewChecker(q)

	var capturedCtx context.Context
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})
	r := authedRequest(userIDStr)
	w := httptest.NewRecorder()
	checker.Require(rbac.PermRBACRead)(h).ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	raw := rbac.ConditionsFromContext(capturedCtx)
	assert.NotEqual(t, "{}", string(raw), "conditions must be non-empty for conditional grant")

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, "eu", decoded["region"])
}
