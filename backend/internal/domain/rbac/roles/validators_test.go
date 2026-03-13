package roles_test

import (
	"strings"
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── validateCreateRole ────────────────────────────────────────────────────────

func TestValidateCreateRole(t *testing.T) {
	t.Parallel()

	t.Run("empty name returns ErrNameEmpty", func(t *testing.T) {
		t.Parallel()
		err := callValidateCreateRole("")
		require.ErrorIs(t, err, roles.ErrNameEmpty)
	})

	t.Run("whitespace-only name returns ErrNameEmpty", func(t *testing.T) {
		t.Parallel()
		err := callValidateCreateRole("   ")
		require.ErrorIs(t, err, roles.ErrNameEmpty)
	})

	t.Run("name exactly 100 chars is valid", func(t *testing.T) {
		t.Parallel()
		err := callValidateCreateRole(strings.Repeat("a", 100))
		require.NoError(t, err)
	})

	t.Run("name exactly 101 chars returns ErrNameTooLong", func(t *testing.T) {
		t.Parallel()
		err := callValidateCreateRole(strings.Repeat("a", 101))
		require.ErrorIs(t, err, roles.ErrNameTooLong)
	})

	t.Run("valid name returns nil", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, callValidateCreateRole("admin"))
	})
}

// ── validateUpdateRole ────────────────────────────────────────────────────────

func TestValidateUpdateRole(t *testing.T) {
	t.Parallel()

	str := func(s string) *string { return &s }

	t.Run("both Name and Description nil returns ErrNoUpdateFields", func(t *testing.T) {
		t.Parallel()
		err := callValidateUpdateRole(nil, nil)
		require.ErrorIs(t, err, roles.ErrNoUpdateFields)
	})

	t.Run("Name pointer to empty string returns ErrNameEmpty", func(t *testing.T) {
		t.Parallel()
		err := callValidateUpdateRole(str(""), nil)
		require.ErrorIs(t, err, roles.ErrNameEmpty)
	})

	t.Run("Name pointer to whitespace-only string returns ErrNameEmpty", func(t *testing.T) {
		t.Parallel()
		err := callValidateUpdateRole(str("  "), nil)
		require.ErrorIs(t, err, roles.ErrNameEmpty)
	})

	t.Run("Name exactly 100 chars is valid", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, callValidateUpdateRole(str(strings.Repeat("a", 100)), nil))
	})

	t.Run("Name exactly 101 chars returns ErrNameTooLong", func(t *testing.T) {
		t.Parallel()
		err := callValidateUpdateRole(str(strings.Repeat("a", 101)), nil)
		require.ErrorIs(t, err, roles.ErrNameTooLong)
	})

	t.Run("Description-only update is valid", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, callValidateUpdateRole(nil, str("some desc")))
	})

	t.Run("both fields provided and valid returns nil", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, callValidateUpdateRole(str("ok"), str("desc")))
	})
}

// ── validateAddRolePermission ─────────────────────────────────────────────────

func TestValidateAddRolePermission(t *testing.T) {
	t.Parallel()

	valid := func() (string, string, string, string) {
		return testPermID, "integration test", "direct", "all"
	}

	t.Run("empty permission_id returns ErrPermissionIDEmpty", func(t *testing.T) {
		t.Parallel()
		_, reason, at, scope := valid()
		err := callValidateAddRolePermission("", reason, at, scope)
		require.ErrorIs(t, err, roles.ErrPermissionIDEmpty)
	})

	t.Run("whitespace-only permission_id returns ErrPermissionIDEmpty", func(t *testing.T) {
		t.Parallel()
		_, reason, at, scope := valid()
		err := callValidateAddRolePermission("  ", reason, at, scope)
		require.ErrorIs(t, err, roles.ErrPermissionIDEmpty)
	})

	t.Run("empty granted_reason returns ErrGrantedReasonEmpty", func(t *testing.T) {
		t.Parallel()
		permID, _, at, scope := valid()
		err := callValidateAddRolePermission(permID, "", at, scope)
		require.ErrorIs(t, err, roles.ErrGrantedReasonEmpty)
	})

	t.Run("whitespace-only granted_reason returns ErrGrantedReasonEmpty", func(t *testing.T) {
		t.Parallel()
		permID, _, at, scope := valid()
		err := callValidateAddRolePermission(permID, "   ", at, scope)
		require.ErrorIs(t, err, roles.ErrGrantedReasonEmpty)
	})

	for _, at := range []string{"direct", "conditional", "request", "denied"} {
		at := at
		t.Run("access_type "+at+" is valid", func(t *testing.T) {
			t.Parallel()
			permID, reason, _, scope := valid()
			require.NoError(t, callValidateAddRolePermission(permID, reason, at, scope))
		})
	}

	t.Run("unknown access_type returns ErrInvalidAccessType", func(t *testing.T) {
		t.Parallel()
		permID, reason, _, scope := valid()
		err := callValidateAddRolePermission(permID, reason, "admin", scope)
		require.ErrorIs(t, err, roles.ErrInvalidAccessType)
		assert.Equal(t, roles.ErrInvalidAccessType.Error(), err.Error())
	})

	for _, scope := range []string{"own", "all"} {
		scope := scope
		t.Run("scope "+scope+" is valid", func(t *testing.T) {
			t.Parallel()
			permID, reason, at, _ := valid()
			require.NoError(t, callValidateAddRolePermission(permID, reason, at, scope))
		})
	}

	t.Run("unknown scope returns ErrInvalidScope", func(t *testing.T) {
		t.Parallel()
		permID, reason, at, _ := valid()
		err := callValidateAddRolePermission(permID, reason, at, "global")
		require.ErrorIs(t, err, roles.ErrInvalidScope)
	})

	t.Run("all fields valid returns nil", func(t *testing.T) {
		t.Parallel()
		permID, reason, at, scope := valid()
		require.NoError(t, callValidateAddRolePermission(permID, reason, at, scope))
	})
}

// ── call helpers (exercise package-private functions through the export shim) ──

// callValidateCreateRole exercises validateCreateRole via the exported shim in
// export_test.go.
func callValidateCreateRole(name string) error {
	return roles.ExportValidateCreateRole(name)
}

func callValidateUpdateRole(name, desc *string) error {
	return roles.ExportValidateUpdateRole(name, desc)
}

func callValidateAddRolePermission(permID, reason, accessType, scope string) error {
	return roles.ExportValidateAddRolePermission(permID, reason, accessType, scope)
}
