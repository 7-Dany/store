package userlock_test

import (
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/admin/userlock"
	"github.com/stretchr/testify/require"
)

func TestValidateLockUser_ReasonEmpty(t *testing.T) {
	t.Parallel()
	err := userlock.ValidateLockUser(userlock.LockUserInput{Reason: ""})
	require.ErrorIs(t, err, userlock.ErrReasonRequired)
}

func TestValidateLockUser_ReasonWhitespace(t *testing.T) {
	t.Parallel()
	err := userlock.ValidateLockUser(userlock.LockUserInput{Reason: "   "})
	require.ErrorIs(t, err, userlock.ErrReasonRequired)
}

func TestValidateLockUser_Valid(t *testing.T) {
	t.Parallel()
	err := userlock.ValidateLockUser(userlock.LockUserInput{Reason: "spam"})
	require.NoError(t, err)
}
