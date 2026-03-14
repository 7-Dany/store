//go:build integration_test

package owner_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/rbac/owner"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	// Lower bcrypt cost for the entire test binary (unit + integration tests).
	// RunTestMain calls m.Run() internally, so this must come first.
	owner.SetTransferTokenBcryptCostForTest(bcrypt.MinCost)
	rbacsharedtest.RunTestMain(m, &testPool, 20)
}

// ── param helpers ─────────────────────────────────────────────────────────────

// textP converts a plain string to pgtype.Text for test query params.
func textP(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

// uuidP converts a uuid.UUID to pgtype.UUID for test query params.
func uuidP(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte(u), Valid: true} }

// ── txStores / withProxy ──────────────────────────────────────────────────────

// txStores returns an owner.Store and a raw *db.Queries, both bound to the
// same rolled-back test transaction. The test is skipped when no pool is configured.
func txStores(t *testing.T) (*owner.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	tx, q := rbacsharedtest.MustBeginTx(t, testPool)
	_ = tx
	return owner.NewStore(testPool).WithQuerier(q), db.New(tx)
}

// withProxy returns an owner.Store whose querier is a QuerierProxy that has
// been mutated by mutate, together with the proxy itself. The test is skipped
// when no pool is configured.
func withProxy(t *testing.T, mutate func(*rbacsharedtest.QuerierProxy)) (*owner.Store, *rbacsharedtest.QuerierProxy) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := rbacsharedtest.MustBeginTx(t, testPool)
	proxy := rbacsharedtest.NewQuerierProxy(q)
	mutate(proxy)
	return owner.NewStore(testPool).WithQuerier(proxy), proxy
}

// seedUser inserts a fully-active, email-verified user and returns its UUID.
func seedUser(t *testing.T, q *db.Queries, email string) uuid.UUID {
	t.Helper()
	pw := rbacsharedtest.MustHashPassword(t, "pw")
	id, err := q.CreateVerifiedActiveUserForTest(context.Background(), db.CreateVerifiedActiveUserForTestParams{
		Email:        textP(email),
		PasswordHash: textP(pw),
	})
	require.NoError(t, err)
	return id
}

// ── CountActiveOwners ─────────────────────────────────────────────────────────

func TestCountActiveOwners_Zero_Integration(t *testing.T) {
	t.Parallel()
	s, _ := txStores(t)
	count, err := s.CountActiveOwners(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 0, count)
}

func TestCountActiveOwners_One_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	userID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)

	_, err = s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{UserID: [16]byte(userID), RoleID: roleID})
	require.NoError(t, err)

	count, err := s.CountActiveOwners(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

// ── GetOwnerRoleID ────────────────────────────────────────────────────────────

func TestGetOwnerRoleID_ReturnsNonZeroUUID_Integration(t *testing.T) {
	t.Parallel()
	s, _ := txStores(t)
	id, err := s.GetOwnerRoleID(context.Background())
	require.NoError(t, err)
	require.NotEqual(t, [16]byte{}, id, "owner role ID must be non-zero")
}

func TestGetOwnerRoleID_Fail_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) { p.FailGetOwnerRoleID = true })
	_, err := s.GetOwnerRoleID(context.Background())
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── GetActiveUserByID ─────────────────────────────────────────────────────────

func TestGetActiveUserByID_UserNotFound_Integration(t *testing.T) {
	t.Parallel()
	s, _ := txStores(t)
	_, err := s.GetActiveUserByID(context.Background(), rbacsharedtest.RandomUUID())
	require.ErrorIs(t, err, rbacshared.ErrUserNotFound)
}

func TestGetActiveUserByID_Success_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	userID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	user, err := s.GetActiveUserByID(ctx, [16]byte(userID))
	require.NoError(t, err)
	require.True(t, user.IsActive)
	require.True(t, user.EmailVerified)
}

func TestGetActiveUserByID_Fail_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) { p.FailGetActiveUserByID = true })
	_, err := s.GetActiveUserByID(context.Background(), rbacsharedtest.RandomUUID())
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── AssignOwnerTx ─────────────────────────────────────────────────────────────

func TestAssignOwnerTx_Success_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	userID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)

	result, err := s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{
		UserID: [16]byte(userID), RoleID: roleID, IPAddress: "127.0.0.1", UserAgent: "test",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.UserID)
	require.Equal(t, "owner", result.RoleName)
	require.False(t, result.GrantedAt.IsZero())

	// Verify user_roles row.
	roleName, err := q.GetUserRoleNameForTest(ctx, uuidP(userID))
	require.NoError(t, err)
	require.Equal(t, "owner", roleName)

	// Verify audit row.
	count, err := q.GetAuditLogEventCountForTest(ctx, db.GetAuditLogEventCountForTestParams{
		UserID: uuidP(userID), EventType: "owner_assigned",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestAssignOwnerTx_FailAssignUserRole_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) { p.FailAssignUserRole = true })
	ctx := context.Background()
	roleID, _ := owner.NewStore(testPool).GetOwnerRoleID(ctx)
	_, err := s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{
		UserID: rbacsharedtest.RandomUUID(), RoleID: roleID,
	})
	require.Error(t, err)
}

func TestAssignOwnerTx_FailInsertAuditLog_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	userID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	_, baseQ := rbacsharedtest.MustBeginTx(t, testPool)
	proxy := rbacsharedtest.NewQuerierProxy(baseQ)
	proxy.FailInsertAuditLog = true
	s2 := owner.NewStore(testPool).WithQuerier(proxy)

	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)

	_, err = s2.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{
		UserID: [16]byte(userID), RoleID: roleID,
	})
	require.Error(t, err)

	// user_roles row must not exist (rolled back).
	_, roleErr := q.GetUserRoleNameForTest(ctx, uuidP(userID))
	require.Error(t, roleErr, "user_roles row must not exist after rollback")
}

// ── GetTransferTargetUser ─────────────────────────────────────────────────────

func TestGetTransferTargetUser_UserNotFound_Integration(t *testing.T) {
	t.Parallel()
	s, _ := txStores(t)
	_, err := s.GetTransferTargetUser(context.Background(), rbacsharedtest.RandomUUID())
	require.ErrorIs(t, err, rbacshared.ErrUserNotFound)
}

func TestGetTransferTargetUser_NotOwner_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	email := rbacsharedtest.NewEmail(t)
	userID := seedUser(t, q, email)

	target, err := s.GetTransferTargetUser(ctx, [16]byte(userID))
	require.NoError(t, err)
	require.False(t, target.IsOwner)
	require.True(t, target.IsActive)
	require.True(t, target.EmailVerified)
	require.Equal(t, email, target.Email)
}

func TestGetTransferTargetUser_IsOwner_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	userID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)
	_, err = s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{UserID: [16]byte(userID), RoleID: roleID})
	require.NoError(t, err)

	target, err := s.GetTransferTargetUser(ctx, [16]byte(userID))
	require.NoError(t, err)
	require.True(t, target.IsOwner)
}

func TestGetTransferTargetUser_FailGetActiveUserByID_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) { p.FailGetActiveUserByID = true })
	_, err := s.GetTransferTargetUser(context.Background(), rbacsharedtest.RandomUUID())
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

func TestGetTransferTargetUser_FailCheckUserAccess_Integration(t *testing.T) {
	t.Parallel()
	_, q := txStores(t)
	ctx := context.Background()

	userID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	// Proxy must be backed by the same transaction so the seeded user is visible.
	proxy := rbacsharedtest.NewQuerierProxy(q)
	proxy.FailCheckUserAccess = true
	s2 := owner.NewStore(testPool).WithQuerier(proxy)

	_, err := s2.GetTransferTargetUser(ctx, [16]byte(userID))
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── HasPendingTransferToken ───────────────────────────────────────────────────

func TestHasPendingTransferToken_NoToken_Integration(t *testing.T) {
	t.Parallel()
	s, _ := txStores(t)
	has, err := s.HasPendingTransferToken(context.Background())
	require.NoError(t, err)
	require.False(t, has)
}

func TestHasPendingTransferToken_ActiveToken_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	email := rbacsharedtest.NewEmail(t)
	userID := seedUser(t, q, email)

	_, _, err := s.InsertTransferToken(ctx, [16]byte(userID), email,
		rbacsharedtest.MustTokenHash("tok"), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.NoError(t, err)

	has, err := s.HasPendingTransferToken(ctx)
	require.NoError(t, err)
	require.True(t, has)
}

func TestHasPendingTransferToken_Fail_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) {
		p.FailGetPendingOwnershipTransferToken = true
	})
	_, err := s.HasPendingTransferToken(context.Background())
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── InsertTransferToken ───────────────────────────────────────────────────────

func TestInsertTransferToken_Success_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	email := rbacsharedtest.NewEmail(t)
	userID := seedUser(t, q, email)

	const initiatedBy = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	codeHash := rbacsharedtest.MustTokenHash("raw-token")
	tokenID, expiresAt, err := s.InsertTransferToken(ctx, [16]byte(userID), email, codeHash, initiatedBy)
	require.NoError(t, err)
	require.NotEqual(t, [16]byte{}, tokenID)
	require.True(t, expiresAt.After(time.Now()), "ExpiresAt must be in the future")

	// Verify via GetPendingTransferToken.
	info, err := s.GetPendingTransferToken(ctx)
	require.NoError(t, err)
	require.Equal(t, [16]byte(userID), info.NewOwnerID)
	require.Equal(t, initiatedBy, info.InitiatedBy)
	require.NotEmpty(t, info.CodeHash)
}

func TestInsertTransferToken_Fail_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) {
		p.FailInsertOwnershipTransferToken = true
	})
	_, _, err := s.InsertTransferToken(context.Background(), rbacsharedtest.RandomUUID(),
		"x@example.com", "hash", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── GetPendingTransferToken ───────────────────────────────────────────────────

func TestGetPendingTransferToken_NoToken_Integration(t *testing.T) {
	t.Parallel()
	s, _ := txStores(t)
	_, err := s.GetPendingTransferToken(context.Background())
	require.ErrorIs(t, err, owner.ErrTransferTokenInvalid)
}

func TestGetPendingTransferToken_Success_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	email := rbacsharedtest.NewEmail(t)
	userID := seedUser(t, q, email)

	const initiatedBy = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	codeHash := rbacsharedtest.MustTokenHash("raw-token")
	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(userID), email, codeHash, initiatedBy)
	require.NoError(t, err)

	info, err := s.GetPendingTransferToken(ctx)
	require.NoError(t, err)
	require.Equal(t, tokenID, info.TokenID)
	require.Equal(t, [16]byte(userID), info.NewOwnerID)
	require.Equal(t, initiatedBy, info.InitiatedBy)
	require.NotEmpty(t, info.CodeHash)
}

func TestGetPendingTransferToken_Fail_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) {
		p.FailGetPendingOwnershipTransferToken = true
	})
	_, err := s.GetPendingTransferToken(context.Background())
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── DeletePendingTransferToken ────────────────────────────────────────────────

func TestDeletePendingTransferToken_NoMatch_Integration(t *testing.T) {
	t.Parallel()
	s, _ := txStores(t)
	err := s.DeletePendingTransferToken(context.Background(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.ErrorIs(t, err, owner.ErrNoPendingTransfer)
}

func TestDeletePendingTransferToken_Success_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	email := rbacsharedtest.NewEmail(t)
	userID := seedUser(t, q, email)

	const initiatedBy = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	_, _, err := s.InsertTransferToken(ctx, [16]byte(userID), email,
		rbacsharedtest.MustTokenHash("tok"), initiatedBy)
	require.NoError(t, err)

	err = s.DeletePendingTransferToken(ctx, initiatedBy)
	require.NoError(t, err)

	_, err = s.GetPendingTransferToken(ctx)
	require.ErrorIs(t, err, owner.ErrTransferTokenInvalid)
}

func TestDeletePendingTransferToken_Fail_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) {
		p.FailDeletePendingOwnershipTransferToken = true
	})
	err := s.DeletePendingTransferToken(context.Background(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── WriteInitiateAuditLog ─────────────────────────────────────────────────────

func TestWriteInitiateAuditLog_WritesRow_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	userID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	const targetIDConst = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	err := s.WriteInitiateAuditLog(ctx, [16]byte(userID), targetIDConst, "127.0.0.1", "test")
	require.NoError(t, err)

	count, err := q.GetAuditLogEventCountForTest(ctx, db.GetAuditLogEventCountForTestParams{
		UserID: uuidP(userID), EventType: "owner_transfer_initiated",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

// ── WriteCancelAuditLog ───────────────────────────────────────────────────────

func TestWriteCancelAuditLog_WritesRow_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	userID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	err := s.WriteCancelAuditLog(ctx, [16]byte(userID), "127.0.0.1", "test")
	require.NoError(t, err)

	count, err := q.GetAuditLogEventCountForTest(ctx, db.GetAuditLogEventCountForTestParams{
		UserID: uuidP(userID), EventType: "owner_transfer_cancelled",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

// ── AcceptTransferTx ──────────────────────────────────────────────────────────

func TestAcceptTransferTx_FailSetSkipEscalationCheck_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) { p.FailSetSkipEscalationCheck = true })
	_, err := s.AcceptTransferTx(context.Background(), owner.AcceptTransferTxInput{})
	require.Error(t, err)
}

func TestAcceptTransferTx_FailConsumeToken_Integration(t *testing.T) {
	t.Parallel()
	s, _ := withProxy(t, func(p *rbacsharedtest.QuerierProxy) {
		p.FailConsumeOwnershipTransferToken = true
	})
	_, err := s.AcceptTransferTx(context.Background(), owner.AcceptTransferTxInput{
		TokenID: rbacsharedtest.RandomUUID(),
	})
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

func TestAcceptTransferTx_TokenAlreadyConsumed_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	email := rbacsharedtest.NewEmail(t)
	userID := seedUser(t, q, email)

	const initiatedBy = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(userID), email,
		rbacsharedtest.MustTokenHash("tok"), initiatedBy)
	require.NoError(t, err)

	// Delete the token so ConsumeOwnershipTransferToken returns 0 rows.
	err = s.DeletePendingTransferToken(ctx, initiatedBy)
	require.NoError(t, err)

	_, err = s.AcceptTransferTx(ctx, owner.AcceptTransferTxInput{TokenID: tokenID})
	require.ErrorIs(t, err, owner.ErrTransferTokenInvalid)
}

func TestAcceptTransferTx_FailCheckUserAccess_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	email := rbacsharedtest.NewEmail(t)
	userID := seedUser(t, q, email)

	const initiatedBy = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(userID), email,
		rbacsharedtest.MustTokenHash("tok"), initiatedBy)
	require.NoError(t, err)

	_, baseQ := rbacsharedtest.MustBeginTx(t, testPool)
	proxy := rbacsharedtest.NewQuerierProxy(baseQ)
	proxy.FailCheckUserAccess = true
	s2 := owner.NewStore(testPool).WithQuerier(proxy)

	_, err = s2.AcceptTransferTx(ctx, owner.AcceptTransferTxInput{TokenID: tokenID})
	require.Error(t, err)
}

func TestAcceptTransferTx_InitiatorNotOwner_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	email := rbacsharedtest.NewEmail(t)
	userID := seedUser(t, q, email)

	// PreviousOwnerID has no owner role.
	const initiatedBy = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(userID), email,
		rbacsharedtest.MustTokenHash("tok"), initiatedBy)
	require.NoError(t, err)

	prevOwnerID := rbacsharedtest.MustUUID(initiatedBy)

	_, err = s.AcceptTransferTx(ctx, owner.AcceptTransferTxInput{
		TokenID:         tokenID,
		NewOwnerID:      [16]byte(userID),
		PreviousOwnerID: prevOwnerID,
		ActingUserID:    [16]byte(userID),
	})
	require.ErrorIs(t, err, owner.ErrInitiatorNotOwner)
}

func TestAcceptTransferTx_FailAssignNewOwner_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	ownerID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	newOwnerID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)
	_, err = s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{UserID: [16]byte(ownerID), RoleID: roleID})
	require.NoError(t, err)

	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(newOwnerID),
		rbacsharedtest.NewEmail(t), rbacsharedtest.MustTokenHash("tok"),
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.NoError(t, err)

	_, baseQ := rbacsharedtest.MustBeginTx(t, testPool)
	proxy := rbacsharedtest.NewQuerierProxy(baseQ)
	proxy.FailAssignUserRole = true
	s2 := owner.NewStore(testPool).WithQuerier(proxy)

	_, err = s2.AcceptTransferTx(ctx, owner.AcceptTransferTxInput{
		TokenID:         tokenID,
		NewOwnerID:      [16]byte(newOwnerID),
		PreviousOwnerID: [16]byte(ownerID),
		RoleID:          roleID,
		ActingUserID:    [16]byte(newOwnerID),
	})
	require.Error(t, err)
}

func TestAcceptTransferTx_FailRemoveOldOwner_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	ownerID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	newOwnerID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)
	_, err = s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{UserID: [16]byte(ownerID), RoleID: roleID})
	require.NoError(t, err)

	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(newOwnerID),
		rbacsharedtest.NewEmail(t), rbacsharedtest.MustTokenHash("tok"),
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.NoError(t, err)

	_, baseQ := rbacsharedtest.MustBeginTx(t, testPool)
	proxy := rbacsharedtest.NewQuerierProxy(baseQ)
	proxy.FailRemoveUserRole = true
	s2 := owner.NewStore(testPool).WithQuerier(proxy)

	_, err = s2.AcceptTransferTx(ctx, owner.AcceptTransferTxInput{
		TokenID:         tokenID,
		NewOwnerID:      [16]byte(newOwnerID),
		PreviousOwnerID: [16]byte(ownerID),
		RoleID:          roleID,
		ActingUserID:    [16]byte(newOwnerID),
	})
	require.Error(t, err)
}

func TestAcceptTransferTx_FailAuditLog_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	ownerID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	newOwnerID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)
	_, err = s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{UserID: [16]byte(ownerID), RoleID: roleID})
	require.NoError(t, err)

	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(newOwnerID),
		rbacsharedtest.NewEmail(t), rbacsharedtest.MustTokenHash("tok"),
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.NoError(t, err)

	_, baseQ := rbacsharedtest.MustBeginTx(t, testPool)
	proxy := rbacsharedtest.NewQuerierProxy(baseQ)
	proxy.FailInsertAuditLog = true
	s2 := owner.NewStore(testPool).WithQuerier(proxy)

	_, err = s2.AcceptTransferTx(ctx, owner.AcceptTransferTxInput{
		TokenID:         tokenID,
		NewOwnerID:      [16]byte(newOwnerID),
		PreviousOwnerID: [16]byte(ownerID),
		RoleID:          roleID,
		ActingUserID:    [16]byte(newOwnerID),
	})
	require.Error(t, err)
}

func TestAcceptTransferTx_FailRevokeTokens_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	ownerID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	newOwnerID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)
	_, err = s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{UserID: [16]byte(ownerID), RoleID: roleID})
	require.NoError(t, err)

	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(newOwnerID),
		rbacsharedtest.NewEmail(t), rbacsharedtest.MustTokenHash("tok"),
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.NoError(t, err)

	_, baseQ := rbacsharedtest.MustBeginTx(t, testPool)
	proxy := rbacsharedtest.NewQuerierProxy(baseQ)
	proxy.FailRevokeAllUserRefreshTokens = true
	s2 := owner.NewStore(testPool).WithQuerier(proxy)

	_, err = s2.AcceptTransferTx(ctx, owner.AcceptTransferTxInput{
		TokenID:         tokenID,
		NewOwnerID:      [16]byte(newOwnerID),
		PreviousOwnerID: [16]byte(ownerID),
		RoleID:          roleID,
		ActingUserID:    [16]byte(newOwnerID),
	})
	require.Error(t, err)
}

func TestAcceptTransferTx_Success_Integration(t *testing.T) {
	t.Parallel()
	s, q := txStores(t)
	ctx := context.Background()

	ownerID := seedUser(t, q, rbacsharedtest.NewEmail(t))
	newOwnerID := seedUser(t, q, rbacsharedtest.NewEmail(t))

	roleID, err := s.GetOwnerRoleID(ctx)
	require.NoError(t, err)
	_, err = s.AssignOwnerTx(ctx, owner.AssignOwnerTxInput{UserID: [16]byte(ownerID), RoleID: roleID})
	require.NoError(t, err)

	tokenID, _, err := s.InsertTransferToken(ctx, [16]byte(newOwnerID),
		rbacsharedtest.NewEmail(t), rbacsharedtest.MustTokenHash("tok"),
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	require.NoError(t, err)

	transferredAt, err := s.AcceptTransferTx(ctx, owner.AcceptTransferTxInput{
		TokenID:         tokenID,
		NewOwnerID:      [16]byte(newOwnerID),
		PreviousOwnerID: [16]byte(ownerID),
		RoleID:          roleID,
		ActingUserID:    [16]byte(newOwnerID),
		IPAddress:       "127.0.0.1",
		UserAgent:       "test",
	})
	require.NoError(t, err)
	require.False(t, transferredAt.IsZero())
	require.WithinDuration(t, time.Now().UTC(), transferredAt, 5*time.Second)

	// New owner has the owner role.
	newRoleName, err := q.GetUserRoleNameForTest(ctx, uuidP(newOwnerID))
	require.NoError(t, err)
	require.Equal(t, "owner", newRoleName)

	// Previous owner has no role.
	_, err = q.GetUserRoleNameForTest(ctx, uuidP(ownerID))
	require.Error(t, err, "previous owner must have no role after transfer")

	// Audit row exists.
	count, err := q.GetAuditLogEventCountForTest(ctx, db.GetAuditLogEventCountForTestParams{
		UserID: uuidP(newOwnerID), EventType: "owner_transfer_accepted",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
