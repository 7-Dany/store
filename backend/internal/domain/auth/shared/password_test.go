package authshared_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/stretchr/testify/require"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// ─── HashPassword ─────────────────────────────────────────────────────────────

func TestHashPassword_HappyPath(t *testing.T) {
	t.Parallel()
	hash, err := authshared.HashPassword("Valid!P@ss1")
	require.NoError(t, err)
	require.NotEmpty(t, hash)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("Valid!P@ss1")))
}

func TestHashPassword_EmptyPlaintext(t *testing.T) {
	t.Parallel()
	_, err := authshared.HashPassword("")
	require.Error(t, err)
}

// §A: HashPassword — invalid bcryptCost branch (password.go lines 22-24).
// Cost 5 is not bcrypt.MinCost (4) and is below the minimum production cost (12),
// so the guard fires and returns an error containing "below minimum".
func TestHashPassword_InvalidCost(t *testing.T) {
	authshared.SetBcryptCostUnsafeForTest(5)
	t.Cleanup(func() { authshared.SetBcryptCostForTest(bcrypt.MinCost) })

	_, err := authshared.HashPassword("somepassword")
	require.Error(t, err)
	require.Contains(t, err.Error(), "below minimum")
}

// §B: HashPassword — bcrypt.GenerateFromPassword error branch (password.go:26-28).
// Setting bcryptCost to 32 (above bcrypt.MaxCost=31) passes the < 12 guard
// (32 >= 12) but causes bcrypt.GenerateFromPassword to return an invalid-cost
// error, covering the otherwise unreachable error path.
func TestHashPassword_BcryptError(t *testing.T) {
	authshared.SetBcryptCostUnsafeForTest(32) // above bcrypt.MaxCost=31
	t.Cleanup(func() { authshared.SetBcryptCostForTest(bcrypt.MinCost) })

	_, err := authshared.HashPassword("somepassword")
	require.Error(t, err)
	require.Contains(t, err.Error(), "bcrypt")
}

func TestHashPassword_ProducesUniqueHashes(t *testing.T) {
	t.Parallel()
	// bcrypt includes a random salt; two calls with the same input must differ.
	h1, err := authshared.HashPassword("Same!P@ss1")
	require.NoError(t, err)
	h2, err := authshared.HashPassword("Same!P@ss1")
	require.NoError(t, err)
	require.NotEqual(t, h1, h2)
}

// ─── CheckPassword ────────────────────────────────────────────────────────────

func TestCheckPassword_CorrectPassword(t *testing.T) {
	t.Parallel()
	hash, err := authshared.HashPassword("Correct!P@ss1")
	require.NoError(t, err)
	require.NoError(t, authshared.CheckPassword(hash, "Correct!P@ss1"))
}

func TestCheckPassword_WrongPassword_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()
	hash, err := authshared.HashPassword("Correct!P@ss1")
	require.NoError(t, err)
	require.ErrorIs(t, authshared.CheckPassword(hash, "Wrong!P@ss1"), authshared.ErrInvalidCredentials)
}

func TestCheckPassword_MalformedHash_ReturnsWrappedError_NotErrInvalidCredentials(t *testing.T) {
	t.Parallel()
	err := authshared.CheckPassword("not-a-bcrypt-hash", "anypassword")
	require.Error(t, err)
	require.NotErrorIs(t, err, authshared.ErrInvalidCredentials)
}

// §C: CheckPassword — ErrHashTooShort branch (password.go lines 56-57).
// A hash shorter than bcrypt's minimum (59 bytes) causes bcrypt to return
// ErrHashTooShort, which CheckPassword wraps and returns.
func TestCheckPassword_ErrHashTooShort(t *testing.T) {
	t.Parallel()
	err := authshared.CheckPassword("$2a$04$short", "anypassword")
	require.Error(t, err)
	require.NotErrorIs(t, err, authshared.ErrInvalidCredentials)
	// The wrapped error must be traceable to bcrypt.ErrHashTooShort or
	// the message must mention "malformed hash".
	if !errors.Is(err, bcrypt.ErrHashTooShort) {
		require.Contains(t, err.Error(), "malformed hash")
	}
}

// ─── SetBcryptCostForTest ─────────────────────────────────────────────────────

func TestSetBcryptCostForTest_PanicsOnNonMinCost(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() { authshared.SetBcryptCostForTest(bcrypt.DefaultCost) })
	require.Panics(t, func() { authshared.SetBcryptCostForTest(0) })
	require.Panics(t, func() { authshared.SetBcryptCostForTest(bcrypt.MinCost + 1) })
}

func TestSetBcryptCostForTest_AcceptsMinCost(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() { authshared.SetBcryptCostForTest(bcrypt.MinCost) })
}

func TestSetBcryptCostForTest_ReducesHashingTime(t *testing.T) {
	// SetBcryptCostForTest is called in TestMain with bcrypt.MinCost, so by the
	// time individual tests run the cost is already low. Verify this means a
	// HashPassword call completes well under 100 ms — any cost >= 12 would take
	// several hundred milliseconds and would cause this assertion to fail.
	t.Parallel()
	start := time.Now()
	_, err := authshared.HashPassword("Check!Speed1")
	require.NoError(t, err)
	require.Less(t, time.Since(start), 100*time.Millisecond,
		"HashPassword must be fast when bcryptCost == MinCost; "+
			"if this fails, SetBcryptCostForTest was not called before this test")
}

// ─── CheckPassword — catch-all error path ───────────────────────────────────

func TestCheckPassword_MalformedHashWithValidLength_ReturnsCatchAllError(t *testing.T) {
	// A string that is long enough to pass ErrHashTooShort (>=60 bytes) but has
	// an invalid bcrypt cost (99 > bcrypt.MaxCost=31) so it falls through to the
	// catch-all fmt.Errorf path, not ErrHashTooShort or ErrMismatchedHashAndPassword.
	t.Parallel()
	// "$2a$99$" (7 bytes) + 53 x's = 60 bytes total.
	hash := "$2a$99$" + strings.Repeat("x", 53)
	err := authshared.CheckPassword(hash, "anyPassword")
	require.Error(t, err)
	require.NotErrorIs(t, err, authshared.ErrInvalidCredentials)
}

// ─── GetDummyPasswordHash ─────────────────────────────────────────────────────

func TestGetDummyPasswordHash_IsValidBcryptHash(t *testing.T) {
	t.Parallel()
	h := authshared.GetDummyPasswordHash()
	require.NotEmpty(t, h)
	cost, err := bcrypt.Cost([]byte(h))
	require.NoError(t, err)
	require.GreaterOrEqual(t, cost, bcrypt.MinCost)
}

func TestGetDummyPasswordHash_ReturnsSameValueOnConcurrentCalls(t *testing.T) {
	t.Parallel()

	const goroutines = 50
	results := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx] = authshared.GetDummyPasswordHash()
		}(i)
	}
	wg.Wait()

	first := results[0]
	require.NotEmpty(t, first)
	for _, r := range results[1:] {
		require.Equal(t, first, r)
	}
}

// ─── GetDummyPasswordHash — panic path ─────────────────────────────────────────

// TestGetDummyPasswordHash_Panic exercises the panic branch (password.go line
// 57-59) inside GetDummyPasswordHash's sync.Once. Setting bcryptCost to 32
// (above bcrypt.MaxCost=31) passes the "< minProductionBcryptCost" guard (32 >=
// 12) but causes bcrypt.GenerateFromPassword to return an error, which triggers
// the panic. The test must NOT call t.Parallel() because it mutates shared
// package-level state (bcryptCost and dummyPasswordHashOnce).
func TestGetDummyPasswordHash_Panic(t *testing.T) {
	// Arrange: force an invalid cost and reset the Once so the initialisation
	// re-runs on the next GetDummyPasswordHash call.
	authshared.SetBcryptCostUnsafeForTest(32) // 32 > bcrypt.MaxCost(31) → GenerateFromPassword fails
	authshared.ResetDummyPasswordHashForTest()

	t.Cleanup(func() {
		// Restore safe state so subsequent tests are not affected.
		authshared.SetBcryptCostForTest(bcrypt.MinCost)
		authshared.ResetDummyPasswordHashForTest()
		_ = authshared.GetDummyPasswordHash() // re-prime at MinCost
	})

	require.Panics(t, func() { authshared.GetDummyPasswordHash() })
}

// ─── GetDummyOTPHash ──────────────────────────────────────────────────────────

func TestGetDummyOTPHash_IsValidBcryptHash(t *testing.T) {
	t.Parallel()
	h := authshared.GetDummyOTPHash()
	require.NotEmpty(t, h)
	cost, err := bcrypt.Cost([]byte(h))
	require.NoError(t, err)
	require.GreaterOrEqual(t, cost, bcrypt.MinCost)
}

func TestGetDummyOTPHash_ReturnsSameValueOnConcurrentCalls(t *testing.T) {
	t.Parallel()

	const goroutines = 50
	results := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx] = authshared.GetDummyOTPHash()
		}(i)
	}
	wg.Wait()

	first := results[0]
	require.NotEmpty(t, first)
	for _, r := range results[1:] {
		require.Equal(t, first, r)
	}
}
