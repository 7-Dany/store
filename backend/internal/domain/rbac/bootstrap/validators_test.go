package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
)

func TestValidateBootstrapRequest(t *testing.T) {
	t.Parallel()

	t.Run("empty bootstrap_secret returns ErrBootstrapSecretEmpty", func(t *testing.T) {
		t.Parallel()
		err := bootstrap.ValidateBootstrapRequestForTest("")
		require.ErrorIs(t, err, bootstrap.ErrBootstrapSecretEmpty)
	})

	t.Run("whitespace-only bootstrap_secret returns ErrBootstrapSecretEmpty", func(t *testing.T) {
		t.Parallel()
		err := bootstrap.ValidateBootstrapRequestForTest("   ")
		require.ErrorIs(t, err, bootstrap.ErrBootstrapSecretEmpty)
	})

	t.Run("non-empty secret returns nil", func(t *testing.T) {
		t.Parallel()
		err := bootstrap.ValidateBootstrapRequestForTest("any-non-empty-value")
		require.NoError(t, err)
	})
}
