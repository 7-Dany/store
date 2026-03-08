//go:build integration_test

// Package email_test contains store-layer integration tests for the
// three-step email-change flow.
//
// Coverage:
//
//	GetCurrentUserEmail:
//	  Happy path — returns the user's current email.
//	  Unknown user ID → profileshared.ErrUserNotFound.
//	  FailGetUserProfile → ErrProxy.
//
//	CheckEmailAvailableForChange:
//	  No conflicting row → available = true.
//	  Email registered to another active user → available = false.
//	  FailCheckEmailAvailableForChange → ErrProxy.
//
//	GetLatestEmailChangeVerifyTokenCreatedAt:
//	  No active token → authshared.ErrTokenNotFound.
//	  Active token exists → returns non-zero created_at time.
//	  FailGetLatestEmailChangeVerifyTokenCreatedAt → ErrProxy.
//
//	RequestEmailChangeTx (T-01, T-11, T-12):
//	  T-01/T-11: Happy path — verify token written to DB, audit row written.
//	  T-12: FailInvalidateUserEmailChangeVerifyTokens → ErrProxy.
//	        FailCreateEmailChangeVerifyToken → ErrProxy.
//	        FailInsertAuditLog → ErrProxy.
//	        Audit row NOT written when token creation fails.
//
//	VerifyCurrentEmailTx (T-13, T-23, T-24):
//	  T-13/T-23: Happy path — verify token consumed, confirm token created,
//	             NewEmail correct, audit row written.
//	  No verify token → authshared.ErrTokenNotFound.
//	  ConsumeEmailChangeTokenZero → authshared.ErrTokenAlreadyUsed.
//	  T-24: FailGetEmailChangeVerifyToken → ErrProxy.
//	        FailConsumeEmailChangeToken → ErrProxy.
//	        FailInvalidateUserEmailChangeConfirmTokens → ErrProxy.
//	        FailCreateEmailChangeConfirmToken → ErrProxy.
//	        FailInsertAuditLog (2nd call) → ErrProxy.
//
//	ConfirmEmailChangeTx (T-25, T-40, T-41, T-42):
//	  T-25/T-40: Happy path — user email column updated, audit row written.
//	  T-41: No confirm token → authshared.ErrTokenNotFound.
//	        ConsumeEmailChangeTokenZero → authshared.ErrTokenAlreadyUsed.
//	        SetUserEmailZero → authshared.ErrUserNotFound.
//	        23505 on SetUserEmail → ErrEmailTaken.
//	  T-42: FailGetEmailChangeConfirmToken → ErrProxy.
//	        FailConsumeEmailChangeToken → ErrProxy.
//	        FailGetUserForEmailChangeTx → ErrProxy.
//	        FailSetUserEmail → ErrProxy.
//	        FailRevokeAllUserRefreshTokens → ErrProxy.
//	        FailEndAllUserSessions → ErrProxy.
//	        FailInsertAuditLog → ErrProxy.
//	        Audit row NOT written when SetUserEmail fails.
//
// Run with:
//
//	go test -tags integration_test ./internal/domain/profile/email/... -v
package email_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/profile/email"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
)

// ── Package-level constants ───────────────────────────────────────────────────

const (
	testTTL      = 720.0 // 12 minutes in seconds
	testIP       = "127.0.0.1"
	testUA       = "go-test/1.0"
	testNewEmail = "newemail@example.com"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// txStores begins a rolled-back test transaction and returns a *email.Store and
// the raw *db.Queries both bound to that transaction.
func txStores(t *testing.T) (*email.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return email.NewStore(testPool).WithQuerier(q), q
}

// withProxy wraps q in proxy and returns a *email.Store that uses the proxy.
func withProxy(q db.Querier, proxy *authsharedtest.QuerierProxy) *email.Store {
	proxy.Querier = q
	return email.NewStore(testPool).WithQuerier(proxy)
}

// alwaysPass is a checkFn that unconditionally approves any token.
// Used in store integration tests to bypass application-layer OTP checks.
var alwaysPass = func(authshared.VerificationToken) error { return nil }

// reqInput builds a RequestEmailChangeTxInput for uid with a valid bcrypt code hash.
func reqInput(t *testing.T, uid [16]byte, currentEmail, newEmail string) email.RequestEmailChangeTxInput {
	t.Helper()
	return email.RequestEmailChangeTxInput{
		UserID:       uid,
		CurrentEmail: currentEmail,
		NewEmail:     newEmail,
		CodeHash:     authsharedtest.MustOTPHash(t),
		TTLSeconds:   testTTL,
		IPAddress:    testIP,
		UserAgent:    testUA,
	}
}

// verifyInput builds a VerifyCurrentEmailTxInput for uid with a valid bcrypt code hash.
func verifyInput(t *testing.T, uid [16]byte) email.VerifyCurrentEmailTxInput {
	t.Helper()
	return email.VerifyCurrentEmailTxInput{
		UserID:           uid,
		NewEmailCodeHash: authsharedtest.MustOTPHash(t),
		TTLSeconds:       testTTL,
		IPAddress:        testIP,
		UserAgent:        testUA,
	}
}

// confirmInput builds a ConfirmEmailChangeTxInput for uid.
func confirmInput(uid [16]byte) email.ConfirmEmailChangeTxInput {
	return email.ConfirmEmailChangeTxInput{
		UserID:    uid,
		IPAddress: testIP,
		UserAgent: testUA,
	}
}

// setupVerifyToken creates a user with currentEmail, runs RequestEmailChangeTx
// targeting newEmail, and returns the uid. s must be bound to q's transaction.
func setupVerifyToken(t *testing.T, s *email.Store, q *db.Queries, currentEmail, newEmail string) [16]byte {
	t.Helper()
	uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, currentEmail))
	err := s.RequestEmailChangeTx(context.Background(), reqInput(t, uid, currentEmail, newEmail))
	require.NoError(t, err, "setupVerifyToken: RequestEmailChangeTx failed")
	return uid
}

// setupConfirmToken runs the first two steps of the email-change flow and returns
// the uid with a live confirm token in the DB.
func setupConfirmToken(t *testing.T, s *email.Store, q *db.Queries, currentEmail, newEmail string) [16]byte {
	t.Helper()
	uid := setupVerifyToken(t, s, q, currentEmail, newEmail)
	_, err := s.VerifyCurrentEmailTx(context.Background(), verifyInput(t, uid), alwaysPass)
	require.NoError(t, err, "setupConfirmToken: VerifyCurrentEmailTx failed")
	return uid
}

// countAudit returns the number of audit rows for uid with the given eventType.
func countAudit(t *testing.T, q *db.Queries, uid [16]byte, eventType string) int64 {
	t.Helper()
	n, err := q.CountAuditEventsByUser(context.Background(), db.CountAuditEventsByUserParams{
		UserID:    authsharedtest.ToPgtypeUUID(uid),
		EventType: eventType,
	})
	require.NoError(t, err)
	return int64(n)
}

// ── TestGetCurrentUserEmail_Integration ───────────────────────────────────────

func TestGetCurrentUserEmail_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("happy path — returns the user's current email", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, userEmail))

		got, err := s.GetCurrentUserEmail(ctx, uid)
		require.NoError(t, err)
		require.Equal(t, userEmail, got)
	})

	t.Run("unknown user ID returns profileshared.ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		_, err := s.GetCurrentUserEmail(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	t.Run("FailGetUserProfile returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserProfile = true
		_, err := withProxy(q, proxy).GetCurrentUserEmail(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestCheckEmailAvailableForChange_Integration ──────────────────────────────

func TestCheckEmailAvailableForChange_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("no conflicting row returns available = true", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		available, err := s.CheckEmailAvailableForChange(ctx, "ghost@example.com", authsharedtest.RandomUUID())
		require.NoError(t, err)
		require.True(t, available, "email not in DB must be available")
	})

	t.Run("email owned by another active user returns available = false", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		takenEmail := authsharedtest.NewEmail(t)
		ownerUID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, takenEmail))
		callerUID := authsharedtest.RandomUUID() // different user checking availability

		available, err := s.CheckEmailAvailableForChange(ctx, takenEmail, callerUID)
		require.NoError(t, err)
		require.False(t, available,
			"email already owned by another user must not be available; owner=%v", ownerUID)
	})

	t.Run("same user's own email is available (no self-conflict)", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, userEmail))

		available, err := s.CheckEmailAvailableForChange(ctx, userEmail, uid)
		require.NoError(t, err)
		require.True(t, available,
			"user's own current email must not count as a conflict")
	})

	t.Run("FailCheckEmailAvailableForChange returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCheckEmailAvailableForChange = true
		_, err := withProxy(q, proxy).CheckEmailAvailableForChange(ctx, "any@example.com", authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestGetLatestEmailChangeVerifyTokenCreatedAt_Integration ──────────────────

func TestGetLatestEmailChangeVerifyTokenCreatedAt_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("no active token returns authshared.ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		_, err := s.GetLatestEmailChangeVerifyTokenCreatedAt(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("active token exists — returns a non-zero created_at time", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupVerifyToken(t, s, q, userEmail, testNewEmail)

		createdAt, err := s.GetLatestEmailChangeVerifyTokenCreatedAt(ctx, uid)
		require.NoError(t, err)
		require.False(t, createdAt.IsZero(), "created_at must be a real DB timestamp")
	})

	t.Run("FailGetLatestEmailChangeVerifyTokenCreatedAt returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetLatestEmailChangeVerifyTokenCreatedAt = true
		_, err := withProxy(q, proxy).GetLatestEmailChangeVerifyTokenCreatedAt(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestRequestEmailChangeTx_Integration ─────────────────────────────────────

func TestRequestEmailChangeTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// ── T-01 / T-11: happy path ───────────────────────────────────────────────

	t.Run("T-01: happy path — verify token written and created_at becomes queryable", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, userEmail))

		err := s.RequestEmailChangeTx(ctx, reqInput(t, uid, userEmail, testNewEmail))
		require.NoError(t, err)

		// Verify token exists by querying its created_at.
		createdAt, err := s.GetLatestEmailChangeVerifyTokenCreatedAt(ctx, uid)
		require.NoError(t, err)
		require.False(t, createdAt.IsZero(), "verify token must be in DB after RequestEmailChangeTx")
	})

	t.Run("T-11: audit row with event_type=email_change_requested is written on success", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, userEmail))

		require.NoError(t, s.RequestEmailChangeTx(ctx, reqInput(t, uid, userEmail, testNewEmail)))

		require.EqualValues(t, 1,
			countAudit(t, q, uid, string(audit.EventEmailChangeRequested)),
			"exactly one email_change_requested audit row must be written")
	})

	// ── T-12: proxy failure paths ─────────────────────────────────────────────

	t.Run("T-12: FailInvalidateUserEmailChangeVerifyTokens returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInvalidateUserEmailChangeVerifyTokens = true
		uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		err := withProxy(q, proxy).RequestEmailChangeTx(ctx,
			reqInput(t, uid, authsharedtest.NewEmail(t), testNewEmail))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-12: FailCreateEmailChangeVerifyToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCreateEmailChangeVerifyToken = true
		uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		err := withProxy(q, proxy).RequestEmailChangeTx(ctx,
			reqInput(t, uid, authsharedtest.NewEmail(t), testNewEmail))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-12: FailInsertAuditLog returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		err := withProxy(q, proxy).RequestEmailChangeTx(ctx,
			reqInput(t, uid, authsharedtest.NewEmail(t), testNewEmail))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("audit row NOT written when CreateEmailChangeVerifyToken proxy fails", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCreateEmailChangeVerifyToken = true
		uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		_ = withProxy(q, proxy).RequestEmailChangeTx(ctx,
			reqInput(t, uid, authsharedtest.NewEmail(t), testNewEmail))

		require.EqualValues(t, 0,
			countAudit(t, q, uid, string(audit.EventEmailChangeRequested)),
			"audit row must NOT be written when token creation fails")
	})
}

// ── TestVerifyCurrentEmailTx_Integration ─────────────────────────────────────

func TestVerifyCurrentEmailTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// ── T-13 / T-23: happy path ───────────────────────────────────────────────

	t.Run("T-13: happy path — verify token consumed, NewEmail matches input", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupVerifyToken(t, s, q, userEmail, testNewEmail)

		result, err := s.VerifyCurrentEmailTx(ctx, verifyInput(t, uid), alwaysPass)
		require.NoError(t, err)
		require.Equal(t, testNewEmail, result.NewEmail,
			"NewEmail must match the new_email stored in the verify token's metadata")
		require.NotZero(t, result.ConfirmTokenID, "ConfirmTokenID must be a non-zero UUID")
		require.False(t, result.ConfirmExpiresAt.IsZero(), "ConfirmExpiresAt must be set")
	})

	t.Run("T-23: audit row email_change_current_verified is written on success", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupVerifyToken(t, s, q, userEmail, testNewEmail)

		_, err := s.VerifyCurrentEmailTx(ctx, verifyInput(t, uid), alwaysPass)
		require.NoError(t, err)

		require.EqualValues(t, 1,
			countAudit(t, q, uid, string(audit.EventEmailChangeCurrentVerified)),
			"exactly one email_change_current_verified audit row must be written")
	})

	// ── Error paths ───────────────────────────────────────────────────────────

	t.Run("no active verify token returns authshared.ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		_, err := s.VerifyCurrentEmailTx(ctx, verifyInput(t, authsharedtest.RandomUUID()), alwaysPass)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("ConsumeEmailChangeTokenZero returns authshared.ErrTokenAlreadyUsed", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupVerifyToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.ConsumeEmailChangeTokenZero = true
		_, err := withProxy(q, proxy).VerifyCurrentEmailTx(ctx, verifyInput(t, uid), alwaysPass)
		require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
	})

	// ── T-24: proxy failure paths ─────────────────────────────────────────────

	t.Run("T-24: FailGetEmailChangeVerifyToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetEmailChangeVerifyToken = true
		_, err := withProxy(q, proxy).VerifyCurrentEmailTx(ctx, verifyInput(t, authsharedtest.RandomUUID()), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-24: FailConsumeEmailChangeToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupVerifyToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailConsumeEmailChangeToken = true
		_, err := withProxy(q, proxy).VerifyCurrentEmailTx(ctx, verifyInput(t, uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-24: FailInvalidateUserEmailChangeConfirmTokens returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupVerifyToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInvalidateUserEmailChangeConfirmTokens = true
		_, err := withProxy(q, proxy).VerifyCurrentEmailTx(ctx, verifyInput(t, uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-24: FailCreateEmailChangeConfirmToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupVerifyToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCreateEmailChangeConfirmToken = true
		_, err := withProxy(q, proxy).VerifyCurrentEmailTx(ctx, verifyInput(t, uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-24: FailInsertAuditLog (step 2's audit) returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupVerifyToken(t, s, q, userEmail, testNewEmail)

		// The verify step produces one InsertAuditLog call — fail it.
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		_, err := withProxy(q, proxy).VerifyCurrentEmailTx(ctx, verifyInput(t, uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestConfirmEmailChangeTx_Integration ─────────────────────────────────────

func TestConfirmEmailChangeTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// ── T-25 / T-40: happy path ───────────────────────────────────────────────

	t.Run("T-25: happy path — user email column updated to newEmail in DB", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		require.NoError(t, s.ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass))

		// Read back the user's email to confirm the column was updated.
		got, err := s.GetCurrentUserEmail(ctx, uid)
		require.NoError(t, err)
		require.Equal(t, testNewEmail, got,
			"user email must be updated to newEmail after ConfirmEmailChangeTx")
	})

	t.Run("T-40: audit row email_changed is written on success", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		require.NoError(t, s.ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass))

		require.EqualValues(t, 1,
			countAudit(t, q, uid, string(audit.EventEmailChanged)),
			"exactly one email_changed audit row must be written")
	})

	// ── T-41: flow sentinel paths ─────────────────────────────────────────────

	t.Run("T-41: no active confirm token returns authshared.ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		err := s.ConfirmEmailChangeTx(ctx, confirmInput(authsharedtest.RandomUUID()), alwaysPass)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("T-41: ConsumeEmailChangeTokenZero returns authshared.ErrTokenAlreadyUsed", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.ConsumeEmailChangeTokenZero = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
	})

	t.Run("T-41: SetUserEmailZero returns authshared.ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.SetUserEmailZero = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("T-41: 23505 on SetUserEmail returns ErrEmailTaken", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		// Step 1: create user1 (current) and run the full flow targeting conflictEmail.
		conflictEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, authsharedtest.NewEmail(t), conflictEmail)

		// Step 2: a second user claims conflictEmail before the confirm step runs.
		authsharedtest.CreateUserUUID(t, testPool, q, conflictEmail)

		// Step 3: ConfirmEmailChangeTx hits the unique constraint → ErrEmailTaken.
		err := s.ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, email.ErrEmailTaken)
	})

	// ── T-42: proxy failure paths ─────────────────────────────────────────────

	t.Run("T-42: FailGetEmailChangeConfirmToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetEmailChangeConfirmToken = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(authsharedtest.RandomUUID()), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-42: FailConsumeEmailChangeToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailConsumeEmailChangeToken = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-42: FailGetUserForEmailChangeTx returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserForEmailChangeTx = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-42: FailSetUserEmail returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailSetUserEmail = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-42: FailRevokeAllUserRefreshTokens returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailRevokeAllUserRefreshTokens = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-42: FailEndAllUserSessions returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailEndAllUserSessions = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("T-42: FailInsertAuditLog returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		err := withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("audit row NOT written when SetUserEmail proxy fails", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userEmail := authsharedtest.NewEmail(t)
		uid := setupConfirmToken(t, s, q, userEmail, testNewEmail)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailSetUserEmail = true
		_ = withProxy(q, proxy).ConfirmEmailChangeTx(ctx, confirmInput(uid), alwaysPass)

		require.EqualValues(t, 0,
			countAudit(t, q, uid, string(audit.EventEmailChanged)),
			"audit row must NOT be written when SetUserEmail fails")
	})
}
