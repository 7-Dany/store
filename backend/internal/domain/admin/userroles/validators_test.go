package userroles_test

import (
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/admin/userroles"
	"github.com/stretchr/testify/require"
)

func TestValidateAssignRole(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      userroles.AssignRoleInput
		wantErr error
	}{
		{
			name:    "role_id empty",
			in:      userroles.AssignRoleInput{GrantedReason: "r"},
			wantErr: userroles.ErrRoleIDEmpty,
		},
		{
			name:    "role_id whitespace",
			in:      userroles.AssignRoleInput{RoleID: " ", GrantedReason: "r"},
			wantErr: userroles.ErrRoleIDEmpty,
		},
		{
			name:    "reason empty",
			in:      userroles.AssignRoleInput{RoleID: "x"},
			wantErr: userroles.ErrGrantedReasonEmpty,
		},
		{
			name:    "reason whitespace",
			in:      userroles.AssignRoleInput{RoleID: "x", GrantedReason: " "},
			wantErr: userroles.ErrGrantedReasonEmpty,
		},
		{
			name:    "valid",
			in:      userroles.AssignRoleInput{RoleID: "x", GrantedReason: "r"},
			wantErr: nil,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := userroles.ValidateAssignRole(tc.in)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}
