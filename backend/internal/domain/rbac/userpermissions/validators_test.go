package userpermissions_test

import (
	"testing"
	"time"

	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userpermissions"
	"github.com/stretchr/testify/require"
)

// ── ValidateGrantPermission ───────────────────────────────────────────────────

func TestValidateGrantPermission_PermissionIDWhitespace(t *testing.T) {
	t.Parallel()
	err := userpermissions.ValidateGrantPermission(userpermissions.GrantPermissionInput{
		PermissionID:  "   ",
		GrantedReason: "test",
		ExpiresAt:     time.Now().Add(time.Hour),
	})
	require.ErrorIs(t, err, userpermissions.ErrPermissionIDEmpty)
}

func TestValidateGrantPermission_GrantedReasonWhitespace(t *testing.T) {
	t.Parallel()
	err := userpermissions.ValidateGrantPermission(userpermissions.GrantPermissionInput{
		PermissionID:  "some-id",
		GrantedReason: "   ",
		ExpiresAt:     time.Now().Add(time.Hour),
	})
	require.ErrorIs(t, err, userpermissions.ErrGrantedReasonEmpty)
}

func TestValidateGrantPermission_ExpiresAtInPast(t *testing.T) {
	t.Parallel()
	err := userpermissions.ValidateGrantPermission(userpermissions.GrantPermissionInput{
		PermissionID:  "some-id",
		GrantedReason: "test",
		ExpiresAt:     time.Now().Add(-time.Second),
	})
	require.ErrorIs(t, err, userpermissions.ErrExpiresAtInPast)
}

func TestValidateGrantPermission_Valid(t *testing.T) {
	t.Parallel()
	err := userpermissions.ValidateGrantPermission(userpermissions.GrantPermissionInput{
		PermissionID:  "some-id",
		GrantedReason: "test",
		ExpiresAt:     time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
}

// ── NormaliseScope ────────────────────────────────────────────────────────────

func TestNormaliseScope(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"", "own"},
		{"own", "own"},
		{"all", "all"},
		{"GLOBAL", "own"},
		{"ALL", "own"}, // case-sensitive; only exact "all" is preserved
		{"admin", "own"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("input="+tc.input, func(t *testing.T) {
			t.Parallel()
			got := userpermissions.NormaliseScope(tc.input)
			require.Equal(t, tc.want, got)
		})
	}
}

// ── ResolveScope ──────────────────────────────────────────────────────────────

func TestResolveScope(t *testing.T) {
	t.Parallel()
	cases := []struct {
		policy    string
		requested string
		wantScope string
		wantErr   error
	}{
		// none policy → only "own" allowed
		{"none", "own", "own", nil},
		{"none", "all", "", rbacshared.ErrScopeNotAllowed},
		// own policy → only "own" allowed
		{"own", "own", "own", nil},
		{"own", "all", "", rbacshared.ErrScopeNotAllowed},
		// all policy → only "all" allowed
		{"all", "all", "all", nil},
		{"all", "own", "", rbacshared.ErrScopeNotAllowed},
		// any policy → both valid
		{"any", "own", "own", nil},
		{"any", "all", "all", nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.policy+"/"+tc.requested, func(t *testing.T) {
			t.Parallel()
			got, err := userpermissions.ResolveScope(tc.policy, tc.requested)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantScope, got)
			}
		})
	}
}
