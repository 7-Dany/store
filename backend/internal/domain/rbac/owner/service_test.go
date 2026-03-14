package owner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/rbac/owner"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/stretchr/testify/require"
)

// ── shared test UUIDs ────────────────────────────────────────────────────────

const (
	ownerUUID  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	targetUUID = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
)

var (
	ownerID  = rbacsharedtest.MustUUID(ownerUUID)
	targetID = rbacsharedtest.MustUUID(targetUUID)
)

// ── AssignOwner ───────────────────────────────────────────────────────────────

func TestService_AssignOwner_OwnerAlreadyExists(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		CountActiveOwnersFn: func(_ context.Context) (int64, error) { return 1, nil },
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.ErrorIs(t, err, owner.ErrOwnerAlreadyExists)
}

func TestService_AssignOwner_CountError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		CountActiveOwnersFn: func(_ context.Context) (int64, error) { return 0, dbErr },
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.ErrorIs(t, err, dbErr)
}

func TestService_AssignOwner_GetOwnerRoleIDError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		GetOwnerRoleIDFn: func(_ context.Context) ([16]byte, error) { return [16]byte{}, dbErr },
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.ErrorIs(t, err, dbErr)
}

func TestService_AssignOwner_GetActiveUserByIDError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (owner.AssignOwnerUser, error) {
			return owner.AssignOwnerUser{}, dbErr
		},
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.ErrorIs(t, err, dbErr)
}

func TestService_AssignOwner_UserNotActive(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (owner.AssignOwnerUser, error) {
			return owner.AssignOwnerUser{IsActive: false, EmailVerified: true}, nil
		},
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.ErrorIs(t, err, owner.ErrUserNotActive)
}

// TestService_AssignOwner_UserNotActiveAndNotVerified verifies that the IsActive
// guard fires before the EmailVerified guard when both flags are false.
func TestService_AssignOwner_UserNotActiveAndNotVerified(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (owner.AssignOwnerUser, error) {
			return owner.AssignOwnerUser{IsActive: false, EmailVerified: false}, nil
		},
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	// IsActive guard fires first — must be ErrUserNotActive, not ErrUserNotVerified.
	require.ErrorIs(t, err, owner.ErrUserNotActive)
}

func TestService_AssignOwner_UserNotVerified(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (owner.AssignOwnerUser, error) {
			return owner.AssignOwnerUser{IsActive: true, EmailVerified: false}, nil
		},
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.ErrorIs(t, err, owner.ErrUserNotVerified)
}

func TestService_AssignOwner_TxError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("tx failed")
	store := &rbacsharedtest.OwnerFakeStorer{
		AssignOwnerTxFn: func(_ context.Context, _ owner.AssignOwnerTxInput) (owner.AssignOwnerResult, error) {
			return owner.AssignOwnerResult{}, dbErr
		},
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.ErrorIs(t, err, dbErr)
}

func TestService_AssignOwner_Success(t *testing.T) {
	t.Parallel()
	want := owner.AssignOwnerResult{UserID: ownerUUID, RoleName: "owner", GrantedAt: time.Now()}
	store := &rbacsharedtest.OwnerFakeStorer{
		AssignOwnerTxFn: func(_ context.Context, _ owner.AssignOwnerTxInput) (owner.AssignOwnerResult, error) {
			return want, nil
		},
	}
	got, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.NoError(t, err)
	require.Equal(t, want.UserID, got.UserID)
	require.Equal(t, "owner", got.RoleName)
}

// ── InitiateTransfer ──────────────────────────────────────────────────────────

func TestService_InitiateTransfer_CannotTransferToSelf(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID,
		TargetUserID:  ownerID, // same as acting owner
	})
	require.ErrorIs(t, err, owner.ErrCannotTransferToSelf)
}

func TestService_InitiateTransfer_HasPendingError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		HasPendingTransferTokenFn: func(_ context.Context) (bool, error) { return false, dbErr },
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, dbErr)
}

func TestService_InitiateTransfer_AlreadyPending(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		HasPendingTransferTokenFn: func(_ context.Context) (bool, error) { return true, nil },
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, owner.ErrTransferAlreadyPending)
}

func TestService_InitiateTransfer_GetTargetError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{}, dbErr
		},
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, dbErr)
}

// TestService_InitiateTransfer_TargetIsOwnerBeforeActiveCheck verifies that
// the IsOwner guard fires before the IsActive guard when both flags are set.
func TestService_InitiateTransfer_TargetIsOwnerBeforeActiveCheck(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			// IsOwner=true, IsActive=false — IsOwner guard must fire first.
			return owner.TransferTargetUser{IsOwner: true, IsActive: false}, nil
		},
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, owner.ErrUserIsAlreadyOwner)
}

func TestService_InitiateTransfer_TargetIsAlreadyOwner(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{IsActive: true, EmailVerified: true, IsOwner: true}, nil
		},
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, owner.ErrUserIsAlreadyOwner)
}

func TestService_InitiateTransfer_TargetNotActive(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{IsActive: false, EmailVerified: true}, nil
		},
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, owner.ErrUserNotActive)
}

func TestService_InitiateTransfer_TargetNotVerified(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{IsActive: true, EmailVerified: false}, nil
		},
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, owner.ErrUserNotVerified)
}

func TestService_InitiateTransfer_InsertTokenError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("insert failed")
	store := &rbacsharedtest.OwnerFakeStorer{
		InsertTransferTokenFn: func(_ context.Context, _ [16]byte, _, _, _ string) ([16]byte, time.Time, error) {
			return [16]byte{}, time.Time{}, dbErr
		},
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, dbErr)
}

// TestService_InitiateTransfer_WriteAuditLogError_NonFatal verifies that a
// failure in WriteInitiateAuditLog does not abort the response; the token is
// already committed so the service must return a non-nil result and nil error.
func TestService_InitiateTransfer_WriteAuditLogError_NonFatal(t *testing.T) {
	t.Parallel()
	auditErr := errors.New("audit write failed")
	tokenID := rbacsharedtest.RandomUUID()
	expiry := time.Now().Add(48 * time.Hour)
	store := &rbacsharedtest.OwnerFakeStorer{
		InsertTransferTokenFn: func(_ context.Context, _ [16]byte, _, _, _ string) ([16]byte, time.Time, error) {
			return tokenID, expiry, nil
		},
		WriteInitiateAuditLogFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
			return auditErr
		},
	}
	result, rawToken, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.NoError(t, err, "audit write failure must be non-fatal")
	require.NotEmpty(t, rawToken)
	require.Equal(t, targetUUID, result.TargetUserID)
}

func TestService_InitiateTransfer_Success(t *testing.T) {
	t.Parallel()
	tokenID := rbacsharedtest.RandomUUID()
	expiry := time.Now().Add(48 * time.Hour)
	store := &rbacsharedtest.OwnerFakeStorer{
		InsertTransferTokenFn: func(_ context.Context, _ [16]byte, _, _, _ string) ([16]byte, time.Time, error) {
			return tokenID, expiry, nil
		},
	}
	result, rawToken, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID,
		TargetUserID:  targetID,
		IPAddress:     "127.0.0.1",
		UserAgent:     "test-agent",
	})
	require.NoError(t, err)
	require.NotEmpty(t, rawToken, "raw token must be returned to the caller for emailing")
	require.Equal(t, expiry.UTC().Truncate(time.Second), result.ExpiresAt.UTC().Truncate(time.Second))
	require.Equal(t, targetUUID, result.TargetUserID)
}

// ── AcceptTransfer ────────────────────────────────────────────────────────────

func TestService_AcceptTransfer_GetPendingTokenError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{}, dbErr
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{
		RawToken: "sometoken",
	})
	require.ErrorIs(t, err, dbErr)
}

func TestService_AcceptTransfer_InvalidToken(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash("correct-token"),
				InitiatedBy: ownerUUID,
			}, nil
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{
		RawToken: "wrong-token",
	})
	require.ErrorIs(t, err, owner.ErrTransferTokenInvalid)
}

func TestService_AcceptTransfer_InvalidInitiatorUUID(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: "not-a-uuid", // malformed
			}, nil
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{
		RawToken: rawToken,
	})
	require.Error(t, err)
	// Must not be a sentinel — this is an internal parsing error.
	require.False(t, errors.Is(err, owner.ErrTransferTokenInvalid))
}

func TestService_AcceptTransfer_ReCheckTargetError(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{}, dbErr
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{
		RawToken: rawToken,
	})
	require.ErrorIs(t, err, dbErr)
}

func TestService_AcceptTransfer_TargetNoLongerEligible(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			// Active but email no longer verified.
			return owner.TransferTargetUser{IsActive: true, EmailVerified: false}, nil
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{
		RawToken: rawToken,
	})
	require.ErrorIs(t, err, owner.ErrUserNotEligible)
}

// TestService_AcceptTransfer_TargetNotActiveAtAcceptTime verifies that
// IsActive=false triggers ErrUserNotEligible at accept time.
func TestService_AcceptTransfer_TargetNotActiveAtAcceptTime(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{IsActive: false, EmailVerified: true}, nil
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{RawToken: rawToken})
	require.ErrorIs(t, err, owner.ErrUserNotEligible)
}

// TestService_AcceptTransfer_TargetNotVerifiedAtAcceptTime verifies that
// EmailVerified=false triggers ErrUserNotEligible at accept time.
func TestService_AcceptTransfer_TargetNotVerifiedAtAcceptTime(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{IsActive: true, EmailVerified: false}, nil
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{RawToken: rawToken})
	require.ErrorIs(t, err, owner.ErrUserNotEligible)
}

// TestService_AcceptTransfer_TargetNeitherActiveNorVerifiedAtAcceptTime verifies
// that when both IsActive and EmailVerified are false, ErrUserNotEligible is returned.
func TestService_AcceptTransfer_TargetNeitherActiveNorVerifiedAtAcceptTime(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{IsActive: false, EmailVerified: false}, nil
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{RawToken: rawToken})
	require.ErrorIs(t, err, owner.ErrUserNotEligible)
}

func TestService_AcceptTransfer_GetOwnerRoleIDError(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		GetOwnerRoleIDFn: func(_ context.Context) ([16]byte, error) { return [16]byte{}, dbErr },
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{
		RawToken: rawToken,
	})
	require.ErrorIs(t, err, dbErr)
}

func TestService_AcceptTransfer_TxError(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	dbErr := errors.New("tx failed")
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		AcceptTransferTxFn: func(_ context.Context, _ owner.AcceptTransferTxInput) (time.Time, error) {
			return time.Time{}, dbErr
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{
		RawToken: rawToken,
	})
	require.ErrorIs(t, err, dbErr)
}

// TestService_AcceptTransfer_TxReturnsTransferTokenInvalid verifies that
// ErrTransferTokenInvalid propagates from AcceptTransferTx (token already consumed race).
func TestService_AcceptTransfer_TxReturnsTransferTokenInvalid(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		AcceptTransferTxFn: func(_ context.Context, _ owner.AcceptTransferTxInput) (time.Time, error) {
			return time.Time{}, owner.ErrTransferTokenInvalid
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{RawToken: rawToken})
	require.ErrorIs(t, err, owner.ErrTransferTokenInvalid)
}

// TestService_AcceptTransfer_TxReturnsInitiatorNotOwner verifies that
// ErrInitiatorNotOwner propagates from AcceptTransferTx.
func TestService_AcceptTransfer_TxReturnsInitiatorNotOwner(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		AcceptTransferTxFn: func(_ context.Context, _ owner.AcceptTransferTxInput) (time.Time, error) {
			return time.Time{}, owner.ErrInitiatorNotOwner
		},
	}
	_, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{RawToken: rawToken})
	require.ErrorIs(t, err, owner.ErrInitiatorNotOwner)
}

func TestService_AcceptTransfer_Success(t *testing.T) {
	t.Parallel()
	const rawToken = "valid-token-abc"
	transferredAt := time.Now().UTC().Truncate(time.Second)
	store := &rbacsharedtest.OwnerFakeStorer{
		GetPendingTransferTokenFn: func(_ context.Context) (owner.PendingTransferInfo, error) {
			return owner.PendingTransferInfo{
				CodeHash:    rbacsharedtest.MustTokenHash(rawToken),
				InitiatedBy: ownerUUID,
				NewOwnerID:  targetID,
			}, nil
		},
		AcceptTransferTxFn: func(_ context.Context, _ owner.AcceptTransferTxInput) (time.Time, error) {
			return transferredAt, nil
		},
	}
	result, err := owner.NewService(store).AcceptTransfer(context.Background(), owner.AcceptInput{
		RawToken:  rawToken,
		IPAddress: "127.0.0.1",
		UserAgent: "test-agent",
	})
	require.NoError(t, err)
	require.Equal(t, targetUUID, result.NewOwnerID)
	require.Equal(t, ownerUUID, result.PreviousOwnerID)
	require.Equal(t, transferredAt, result.TransferredAt.UTC().Truncate(time.Second))
}

// ── CancelTransfer ────────────────────────────────────────────────────────────

func TestService_CancelTransfer_DeleteError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	store := &rbacsharedtest.OwnerFakeStorer{
		DeletePendingTransferTokenFn: func(_ context.Context, _ string) error { return dbErr },
	}
	err := owner.NewService(store).CancelTransfer(context.Background(), ownerID, "127.0.0.1", "agent")
	require.ErrorIs(t, err, dbErr)
}

func TestService_CancelTransfer_NoPendingTransfer(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		DeletePendingTransferTokenFn: func(_ context.Context, _ string) error {
			return owner.ErrNoPendingTransfer
		},
	}
	err := owner.NewService(store).CancelTransfer(context.Background(), ownerID, "127.0.0.1", "agent")
	require.ErrorIs(t, err, owner.ErrNoPendingTransfer)
}

// TestService_CancelTransfer_AuditLogError_NonFatal verifies that a failure in
// WriteCancelAuditLog does not abort the cancel; the delete already succeeded.
func TestService_CancelTransfer_AuditLogError_NonFatal(t *testing.T) {
	t.Parallel()
	auditErr := errors.New("audit write failed")
	store := &rbacsharedtest.OwnerFakeStorer{
		WriteCancelAuditLogFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return auditErr
		},
	}
	err := owner.NewService(store).CancelTransfer(context.Background(), ownerID, "127.0.0.1", "agent")
	require.NoError(t, err, "audit write failure must be non-fatal")
}

func TestService_CancelTransfer_Success(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{}
	err := owner.NewService(store).CancelTransfer(context.Background(), ownerID, "127.0.0.1", "agent")
	require.NoError(t, err)
}

// ── ErrUserNotFound propagation ───────────────────────────────────────────────

func TestService_AssignOwner_UserNotFound(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetActiveUserByIDFn: func(_ context.Context, _ [16]byte) (owner.AssignOwnerUser, error) {
			return owner.AssignOwnerUser{}, rbacshared.ErrUserNotFound
		},
	}
	_, err := owner.NewService(store).AssignOwner(context.Background(), owner.AssignOwnerInput{UserID: ownerID})
	require.ErrorIs(t, err, rbacshared.ErrUserNotFound)
}

func TestService_InitiateTransfer_TargetNotFound(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.OwnerFakeStorer{
		GetTransferTargetUserFn: func(_ context.Context, _ [16]byte) (owner.TransferTargetUser, error) {
			return owner.TransferTargetUser{}, rbacshared.ErrUserNotFound
		},
	}
	_, _, err := owner.NewService(store).InitiateTransfer(context.Background(), owner.InitiateInput{
		ActingOwnerID: ownerID, TargetUserID: targetID,
	})
	require.ErrorIs(t, err, rbacshared.ErrUserNotFound)
}
