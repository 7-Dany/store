package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// testSecret is the bootstrap secret used in all unit tests.
const testSecret = "unit-test-bootstrap-secret"

// jsonReq creates a POST /bootstrap request with the given raw JSON body.
// The request has NO user ID in its context — use authedReq for authenticated paths.
func jsonReq(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// authedReq creates a POST /bootstrap request with userID injected into the
// context (simulating a passed JWTAuth middleware) and the given body.
func authedReq(t *testing.T, userID, body string) *http.Request {
	t.Helper()
	req := jsonReq(t, body)
	req = req.WithContext(token.InjectUserIDForTest(req.Context(), userID))
	return req
}

// secretBody returns the JSON body for a valid bootstrap request.
func secretBody(secret string) string {
	return `{"bootstrap_secret":"` + secret + `"}`
}

// jsonBody encodes v as JSON into a buffer for use as a request body.
func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

// decodeBody unmarshals the response body into a map for assertions.
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

// errFn returns a BootstrapFakeServicer whose Bootstrap always returns err.
func errFn(err error) *rbacsharedtest.BootstrapFakeServicer {
	return &rbacsharedtest.BootstrapFakeServicer{
		BootstrapFn: func(_ context.Context, _ bootstrap.BootstrapInput) (bootstrap.BootstrapResult, error) {
			return bootstrap.BootstrapResult{}, err
		},
	}
}

// newHandler constructs a Handler with testSecret pre-wired.
func newHandler(svc bootstrap.Servicer) *bootstrap.Handler {
	return bootstrap.NewHandler(svc, testSecret)
}

// createVerifiedUser inserts an active, email-verified user inside tx and
// returns its UUID string. Skips if testPool is nil.
func createVerifiedUser(t *testing.T, tx pgx.Tx) string {
	t.Helper()
	email := rbacsharedtest.NewEmail(t)
	cq := db.New(tx)
	id, err := cq.CreateVerifiedUserWithUsername(context.Background(), db.CreateVerifiedUserWithUsernameParams{
		Email:        pgtype.Text{String: email, Valid: true},
		Username:     pgtype.Text{Valid: false},
		PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "S3cure!Pass"), Valid: true},
	})
	require.NoError(t, err)
	return id.String()
}

// createUnverifiedUser inserts an active, email-unverified user and returns
// its UUID string.
func createUnverifiedUser(t *testing.T, tx pgx.Tx) string {
	t.Helper()
	email := rbacsharedtest.NewEmail(t)
	cq := db.New(tx)
	id, err := cq.CreateActiveUnverifiedUserForTest(context.Background(), db.CreateActiveUnverifiedUserForTestParams{
		Email:        pgtype.Text{String: email, Valid: true},
		PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "S3cure!Pass"), Valid: true},
	})
	require.NoError(t, err)
	return id.String()
}

// seedOwner assigns the owner role to userIDStr within q.
func seedOwner(t *testing.T, q db.Querier, userIDStr string) {
	t.Helper()
	userID := rbacsharedtest.MustUUID(userIDStr)
	ownerRoleID, err := q.GetOwnerRoleID(context.Background())
	require.NoError(t, err)
	_, err = q.AssignUserRole(context.Background(), db.AssignUserRoleParams{
		UserID:        pgtype.UUID{Bytes: userID, Valid: true},
		RoleID:        pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
		GrantedBy:     pgtype.UUID{Bytes: userID, Valid: true},
		GrantedReason: "test seed",
		ExpiresAt:     pgtype.Timestamptz{Valid: false},
	})
	require.NoError(t, err)
}

// ── Unit tests (no build tag, BootstrapFakeServicer) ─────────────────────────

func TestBootstrapHandler(t *testing.T) {
	t.Parallel()

	const callerUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	// ─ Guard 1: JWT context ──────────────────────────────────────────────────────

	t.Run("no user ID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := newHandler(&rbacsharedtest.BootstrapFakeServicer{})
		w := httptest.NewRecorder()
		// jsonReq has no user ID injected — simulates missing/invalid JWT.
		h.Bootstrap(w, jsonReq(t, secretBody(testSecret)))
		require.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "unauthorized", decodeBody(t, w)["code"])
	})

	// ─ Body size / decode guards ─────────────────────────────────────────────────

	t.Run("body exceeds MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		h := newHandler(&rbacsharedtest.BootstrapFakeServicer{})
		body := strings.Repeat("x", respond.MaxBodyBytes+1)
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, body))
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("malformed JSON returns 400", func(t *testing.T) {
		t.Parallel()
		h := newHandler(&rbacsharedtest.BootstrapFakeServicer{})
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, `{not-valid-json`))
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "bad_request", decodeBody(t, w)["code"])
	})

	// ─ Guard 2: bootstrap_secret validation and compare ──────────────────────────

	t.Run("empty bootstrap_secret returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		h := newHandler(&rbacsharedtest.BootstrapFakeServicer{})
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, `{"bootstrap_secret":""}`))
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("whitespace-only bootstrap_secret returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		h := newHandler(&rbacsharedtest.BootstrapFakeServicer{})
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, `{"bootstrap_secret":"   "}`))
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("wrong bootstrap_secret returns 403 forbidden", func(t *testing.T) {
		t.Parallel()
		h := newHandler(&rbacsharedtest.BootstrapFakeServicer{})
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, secretBody("wrong-secret")))
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "forbidden", decodeBody(t, w)["code"])
	})

	// ─ Guard 3: service-layer errors ─────────────────────────────────────────────

	t.Run("service ErrOwnerAlreadyExists returns 409", func(t *testing.T) {
		t.Parallel()
		h := newHandler(errFn(rbac.ErrOwnerAlreadyExists))
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, secretBody(testSecret)))
		require.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "owner_already_exists", decodeBody(t, w)["code"])
	})

	t.Run("service ErrUserNotFound returns 404", func(t *testing.T) {
		t.Parallel()
		// Caller is authenticated; ErrUserNotFound means account deleted mid-flight.
		h := newHandler(errFn(rbacshared.ErrUserNotFound))
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, secretBody(testSecret)))
		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "user_not_found", decodeBody(t, w)["code"])
	})

	t.Run("service ErrUserNotActive returns 422", func(t *testing.T) {
		t.Parallel()
		h := newHandler(errFn(bootstrap.ErrUserNotActive))
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, secretBody(testSecret)))
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "user_not_active", decodeBody(t, w)["code"])
	})

	t.Run("service ErrUserNotVerified returns 422", func(t *testing.T) {
		t.Parallel()
		h := newHandler(errFn(bootstrap.ErrUserNotVerified))
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, secretBody(testSecret)))
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "email_not_verified", decodeBody(t, w)["code"])
	})

	t.Run("service unexpected error returns 500", func(t *testing.T) {
		t.Parallel()
		h := newHandler(errFn(errors.New("unexpected db error")))
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, secretBody(testSecret)))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal_error", decodeBody(t, w)["code"])
	})

	// ─ Success ───────────────────────────────────────────────────────────────────

	t.Run("correct secret and valid JWT returns 201 with role data", func(t *testing.T) {
		t.Parallel()
		grantedAt := time.Now().Truncate(time.Second)
		svc := &rbacsharedtest.BootstrapFakeServicer{
			BootstrapFn: func(_ context.Context, _ bootstrap.BootstrapInput) (bootstrap.BootstrapResult, error) {
				return bootstrap.BootstrapResult{
					UserID:    callerUUID,
					RoleName:  "owner",
					GrantedAt: grantedAt,
				}, nil
			},
		}
		h := newHandler(svc)
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, secretBody(testSecret)))

		require.Equal(t, http.StatusCreated, w.Code)
		body := decodeBody(t, w)
		assert.Equal(t, callerUUID, body["user_id"])
		assert.Equal(t, "owner", body["role_name"])
		assert.NotEmpty(t, body["granted_at"])
	})

	t.Run("service receives user ID from JWT context not from body", func(t *testing.T) {
		t.Parallel()
		var capturedInput bootstrap.BootstrapInput
		svc := &rbacsharedtest.BootstrapFakeServicer{
			BootstrapFn: func(_ context.Context, in bootstrap.BootstrapInput) (bootstrap.BootstrapResult, error) {
				capturedInput = in
				return bootstrap.BootstrapResult{}, nil
			},
		}
		h := newHandler(svc)
		w := httptest.NewRecorder()
		h.Bootstrap(w, authedReq(t, callerUUID, secretBody(testSecret)))

		require.Equal(t, http.StatusCreated, w.Code)
		parsedUUID := rbacsharedtest.MustUUID(callerUUID)
		assert.Equal(t, [16]byte(parsedUUID), capturedInput.UserID)
	})
}

// ── Integration tests (require //go:build integration_test + TEST_DATABASE_URL) ─

// T-R18: No owner exists, valid active+verified user → HTTP 201 with correct body.
func TestBootstrap_Success(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured")
	}
	tx, q := rbacsharedtest.MustBeginTx(t, testPool)

	userID := createVerifiedUser(t, tx)

	store := bootstrap.NewStore(testPool).WithQuerier(q)
	h := bootstrap.NewHandler(bootstrap.NewService(store), testSecret)

	req := authedReq(t, userID, secretBody(testSecret))
	w := httptest.NewRecorder()
	h.Bootstrap(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, userID, body["user_id"])
	assert.Equal(t, "owner", body["role_name"])
	assert.NotEmpty(t, body["granted_at"])
}

// T-R19: Active owner already exists → HTTP 409 owner_already_exists.
func TestBootstrap_OwnerAlreadyExists(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured")
	}
	tx, q := rbacsharedtest.MustBeginTx(t, testPool)

	userID := createVerifiedUser(t, tx)
	seedOwner(t, q, userID)

	store := bootstrap.NewStore(testPool).WithQuerier(q)
	h := bootstrap.NewHandler(bootstrap.NewService(store), testSecret)

	req := authedReq(t, userID, secretBody(testSecret))
	w := httptest.NewRecorder()
	h.Bootstrap(w, req)

	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "owner_already_exists", decodeBody(t, w)["code"])
}

// T-R20: JWT context user ID does not exist in DB → HTTP 404 user_not_found.
func TestBootstrap_UserNotFound(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := rbacsharedtest.MustBeginTx(t, testPool)

	store := bootstrap.NewStore(testPool).WithQuerier(q)
	h := bootstrap.NewHandler(bootstrap.NewService(store), testSecret)

	// Inject a UUID that does not exist in the database.
	req := authedReq(t, "00000000-0000-0000-0000-000000000001", secretBody(testSecret))
	w := httptest.NewRecorder()
	h.Bootstrap(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_not_found", decodeBody(t, w)["code"])
}

// T-R21: User exists but email_verified = FALSE → HTTP 422 email_not_verified.
func TestBootstrap_EmailNotVerified(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured")
	}
	tx, q := rbacsharedtest.MustBeginTx(t, testPool)

	userID := createUnverifiedUser(t, tx)

	store := bootstrap.NewStore(testPool).WithQuerier(q)
	h := bootstrap.NewHandler(bootstrap.NewService(store), testSecret)

	req := authedReq(t, userID, secretBody(testSecret))
	w := httptest.NewRecorder()
	h.Bootstrap(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "email_not_verified", decodeBody(t, w)["code"])
}

// T-R22: Rate limiter is wired — 4th request from the same IP must yield 429.
// deps.JWTAuth is set to a passthrough so all requests clear auth and the
// rate limiter is the guard under test.
func TestBootstrap_RateLimitIsRegistered(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured")
	}
	ctx := context.Background()

	kv := kvstore.NewInMemoryStore(5 * time.Minute)
	deps := &app.Deps{
		Pool:    testPool,
		KVStore: kv,
		// Passthrough middleware: injects a fake user ID so JWTAuth guard passes
		// and the rate limiter is the guard under test.
		JWTAuth: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = r.WithContext(token.InjectUserIDForTest(r.Context(), "00000000-0000-0000-0000-000000000001"))
				next.ServeHTTP(w, r)
			})
		},
	}

	r := chi.NewRouter()
	bootstrap.Routes(ctx, r, deps)

	var lastCode int
	for range 4 {
		body := jsonBody(t, map[string]string{"bootstrap_secret": testSecret})
		req := httptest.NewRequest(http.MethodPost, "/bootstrap", body)
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		lastCode = w.Code
	}

	assert.Equal(t, http.StatusTooManyRequests, lastCode)
}
