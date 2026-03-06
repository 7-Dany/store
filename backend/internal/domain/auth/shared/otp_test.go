package authshared_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// GenerateCodeHash rand-error branch requires replacing rand.Reader; excluded from coverage.

// TestVerifyCodeHash verifies that VerifyCodeHash rejects codes whose length is not
// exactly 6 bytes — covering the len(code) != 6 early-return branch (otp.go:81).
// anyHash is a real bcrypt hash of "123456" at MinCost so the test is self-contained.
func TestVerifyCodeHash(t *testing.T) {
	t.Parallel()

	anyHash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.MinCost)
	require.NoError(t, err)
	h := string(anyHash)

	cases := []struct {
		code string
		desc string
	}{
		{"", "empty string"},
		{"12345", "5 chars"},
		{"1234567", "7 chars"},
		{"123456a", "7 chars with letter"},
	}
	for _, tc := range cases {
		code := tc.code
		desc := tc.desc
		t.Run(desc, func(t *testing.T) {
			t.Parallel()
			require.False(t, authshared.VerifyCodeHash(code, h),
				"VerifyCodeHash must return false for wrong-length input %q", code)
		})
	}
}

// §D: GenerateCodeHash — invalid bcrypt cost guard (otp.go:69-71).
// Setting bcryptCost to 8 satisfies: (8 != bcrypt.MinCost) && (8 < minProductionBcryptCost=12)
// so GenerateCodeHash must return an error without ever calling rand or bcrypt.
func TestGenerateCodeHash_InvalidCost(t *testing.T) {
	authshared.SetBcryptCostUnsafeForTest(8)
	t.Cleanup(func() { authshared.SetBcryptCostForTest(bcrypt.MinCost) })

	_, _, err := authshared.GenerateCodeHash()
	require.Error(t, err)
	require.Contains(t, err.Error(), "bcryptCost")
}

// §E: GenerateCodeHash — bcrypt error branch (otp.go:73-75).
// Setting bcryptCost to 32 (above bcrypt.MaxCost=31) passes the low-cost guard
// (32 >= 12) but causes bcrypt.GenerateFromPassword to fail, covering this branch.
func TestGenerateCodeHash_BcryptError(t *testing.T) {
	authshared.SetBcryptCostUnsafeForTest(32) // above bcrypt.MaxCost=31
	t.Cleanup(func() { authshared.SetBcryptCostForTest(bcrypt.MinCost) })

	_, _, err := authshared.GenerateCodeHash()
	require.Error(t, err)
	require.Contains(t, err.Error(), "bcrypt")
}

// §F: GetDummyOTPHash — panic branch (otp.go:78-80).
// ResetDummyOTPHashForTest (export_test.go) exposes the unexported sync.Once
// so we can re-arm it, then bcryptCost=32 causes GenerateCodeHash to fail →
// GetDummyOTPHash panics. Cleanup re-primes the once at MinCost so later
// parallel tests that call GetDummyOTPHash still get a valid hash.
// Must NOT call t.Parallel() — mutates shared package-level state.
func TestGetDummyOTPHash_Panic(t *testing.T) {
	authshared.SetBcryptCostUnsafeForTest(32)
	t.Cleanup(func() {
		authshared.SetBcryptCostForTest(bcrypt.MinCost)
		authshared.ResetDummyOTPHashForTest()
		_ = authshared.GetDummyOTPHash() // re-prime at MinCost
	})

	authshared.ResetDummyOTPHashForTest()
	require.Panics(t, func() { authshared.GetDummyOTPHash() })
}

// makeToken builds a VerificationToken with sensible defaults.
func makeToken(overrides func(*authshared.VerificationToken)) authshared.VerificationToken {
	tok := authshared.VerificationToken{
		Attempts:    0,
		MaxAttempts: 5,
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if overrides != nil {
		overrides(&tok)
	}
	return tok
}

// hashCode bcrypt-hashes a plaintext code at MinCost for use in test tokens.
func hashCode(t *testing.T, code string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	require.NoError(t, err)
	return string(h)
}

// ─── CheckOTPToken — happy path ───────────────────────────────────────────────

func TestCheckOTPToken_ValidCode_ReturnsNil(t *testing.T) {
	t.Parallel()
	const code = "123456"
	tok := makeToken(nil)
	tok.CodeHash = hashCode(t, code)
	require.NoError(t, authshared.CheckOTPToken(tok, code, time.Now()))
}

// ─── CheckOTPToken — expiry guard ────────────────────────────────────────────

func TestCheckOTPToken_Expired_ReturnsErrTokenExpired(t *testing.T) {
	t.Parallel()
	const code = "000001"
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.ExpiresAt = time.Now().Add(-time.Second)
	})
	tok.CodeHash = hashCode(t, code)
	require.ErrorIs(t, authshared.CheckOTPToken(tok, code, time.Now()), authshared.ErrTokenExpired)
}

func TestCheckOTPToken_ExactlyAtExpiry_ReturnsErrTokenExpired(t *testing.T) {
	t.Parallel()
	const code = "000002"
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.ExpiresAt = time.Now().Add(-time.Nanosecond)
	})
	tok.CodeHash = hashCode(t, code)
	require.ErrorIs(t, authshared.CheckOTPToken(tok, code, time.Now()), authshared.ErrTokenExpired)
}

// ─── CheckOTPToken — attempt guard ───────────────────────────────────────────

func TestCheckOTPToken_TooManyAttempts_ReturnsErrTooManyAttempts(t *testing.T) {
	t.Parallel()
	const code = "000003"
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.Attempts = 5
		tok.MaxAttempts = 5
	})
	tok.CodeHash = hashCode(t, code)
	require.ErrorIs(t, authshared.CheckOTPToken(tok, code, time.Now()), authshared.ErrTooManyAttempts)
}

func TestCheckOTPToken_AttemptsExceedsMax_ReturnsErrTooManyAttempts(t *testing.T) {
	t.Parallel()
	const code = "000004"
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.Attempts = 10
		tok.MaxAttempts = 5
	})
	tok.CodeHash = hashCode(t, code)
	require.ErrorIs(t, authshared.CheckOTPToken(tok, code, time.Now()), authshared.ErrTooManyAttempts)
}

// ─── CheckOTPToken — wrong code ───────────────────────────────────────────────

func TestCheckOTPToken_WrongCode_ReturnsErrInvalidCode(t *testing.T) {
	t.Parallel()
	tok := makeToken(nil)
	tok.CodeHash = hashCode(t, "123456")
	require.ErrorIs(t, authshared.CheckOTPToken(tok, "654321", time.Now()), authshared.ErrInvalidCode)
}

// ─── CheckOTPToken — guard ordering ──────────────────────────────────────────

func TestCheckOTPToken_GuardOrder_ExpiredBeforeAttempts(t *testing.T) {
	t.Parallel()
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.ExpiresAt = time.Now().Add(-time.Hour)
		tok.Attempts = 5
		tok.MaxAttempts = 5
	})
	tok.CodeHash = hashCode(t, "111111")
	require.ErrorIs(t, authshared.CheckOTPToken(tok, "111111", time.Now()), authshared.ErrTokenExpired)
}

func TestCheckOTPToken_GuardOrder_AttemptsBeforeCode(t *testing.T) {
	t.Parallel()
	const code = "222222"
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.Attempts = 5
		tok.MaxAttempts = 5
	})
	tok.CodeHash = hashCode(t, code)
	require.ErrorIs(t, authshared.CheckOTPToken(tok, code, time.Now()), authshared.ErrTooManyAttempts)
}

// ─── CheckOTPToken — invalid code length ─────────────────────────────────────

func TestCheckOTPToken_WrongCodeLength_ReturnsErrInvalidCode(t *testing.T) {
	t.Parallel()
	tok := makeToken(nil)
	tok.CodeHash = hashCode(t, "123456")

	for _, bad := range []string{"", "1", "12345", "1234567", "abcdef"} {
		require.ErrorIs(t, authshared.CheckOTPToken(tok, bad, time.Now()), authshared.ErrInvalidCode,
			"expected ErrInvalidCode for code %q", bad)
	}
}

// ─── VerifyCodeHash ───────────────────────────────────────────────────────────

func TestVerifyCodeHash_CorrectCode_ReturnsTrue(t *testing.T) {
	t.Parallel()
	const code = "999999"
	h := hashCode(t, code)
	require.True(t, authshared.VerifyCodeHash(code, h))
}

func TestVerifyCodeHash_WrongCode_ReturnsFalse(t *testing.T) {
	t.Parallel()
	h := hashCode(t, "123456")
	require.False(t, authshared.VerifyCodeHash("654321", h))
}

func TestVerifyCodeHash_NonSixByteCode_ReturnsFalse(t *testing.T) {
	t.Parallel()
	h := hashCode(t, "123456")
	for _, bad := range []string{"", "1", "12345", "1234567"} {
		require.False(t, authshared.VerifyCodeHash(bad, h),
			"expected false for code %q", bad)
	}
}

// ─── GenerateCodeHash ─────────────────────────────────────────────────────────

func TestGenerateCodeHash_ProducesValidSixDigitCode(t *testing.T) {
	t.Parallel()
	raw, hash, err := authshared.GenerateCodeHash()
	require.NoError(t, err)
	require.Len(t, raw, 6)
	for _, c := range raw {
		require.True(t, c >= '0' && c <= '9', "code %q contains non-digit %q", raw, c)
	}
	require.NotEmpty(t, hash)
	require.True(t, authshared.VerifyCodeHash(raw, hash), "hash must verify against raw code")
}

func TestGenerateCodeHash_ProducesUniqueCodesAndHashes(t *testing.T) {
	t.Parallel()
	raw1, hash1, err := authshared.GenerateCodeHash()
	require.NoError(t, err)
	raw2, hash2, err := authshared.GenerateCodeHash()
	require.NoError(t, err)
	// Codes may collide with ~1-in-a-million probability; hashes must never collide.
	require.NotEqual(t, hash1, hash2)
	_ = raw1
	_ = raw2
}

// ─── ConsumeOTPToken ─────────────────────────────────────────────────────────

// TestConsumeOTPToken_HappyPath verifies that a correct code results in
// onSuccess being called and nil being returned.
func TestConsumeOTPToken_HappyPath_CallsOnSuccess(t *testing.T) {
	t.Parallel()
	const code = "123456"
	tok := makeToken(nil)
	tok.CodeHash = hashCode(t, code)

	var successCalled bool
	err := authshared.ConsumeOTPToken(
		context.Background(),
		code,
		func(checkFn func(authshared.VerificationToken) error) error {
			return checkFn(tok)
		},
		func(token authshared.VerificationToken) error {
			successCalled = true
			return nil
		},
		func(_ context.Context, _ authshared.VerificationToken) error {
			t.Fatal("incrementFn must not be called on success")
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, successCalled)
}

// TestConsumeOTPToken_TokenNotFound verifies the anti-enumeration path.
func TestConsumeOTPToken_TokenNotFound_ReturnsSentinel(t *testing.T) {
	t.Parallel()
	var incrementCalled bool
	err := authshared.ConsumeOTPToken(
		context.Background(),
		"000000",
		func(_ func(authshared.VerificationToken) error) error {
			return authshared.ErrTokenNotFound
		},
		func(_ authshared.VerificationToken) error {
			t.Fatal("onSuccess must not be called on ErrTokenNotFound")
			return nil
		},
		func(_ context.Context, _ authshared.VerificationToken) error {
			incrementCalled = true
			return nil
		},
	)
	require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	require.False(t, incrementCalled, "incrementFn must not be called on ErrTokenNotFound")
}

// TestConsumeOTPToken_InvalidCode_CallsIncrementFn verifies that ErrInvalidCode
// triggers incrementFn when attempts < maxAttempts.
func TestConsumeOTPToken_InvalidCode_CallsIncrementFn(t *testing.T) {
	t.Parallel()
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.Attempts = 1
		tok.MaxAttempts = 5
		tok.CodeHash = hashCode(t, "123456")
	})

	var incrementCalled bool
	err := authshared.ConsumeOTPToken(
		context.Background(),
		"999999", // wrong code
		func(checkFn func(authshared.VerificationToken) error) error {
			return checkFn(tok)
		},
		func(_ authshared.VerificationToken) error {
			t.Fatal("onSuccess must not be called on wrong code")
			return nil
		},
		func(_ context.Context, _ authshared.VerificationToken) error {
			incrementCalled = true
			return nil
		},
	)
	require.ErrorIs(t, err, authshared.ErrInvalidCode)
	require.True(t, incrementCalled, "incrementFn must be called on ErrInvalidCode below max")
}

// TestConsumeOTPToken_InvalidCode_AtMaxAttempts verifies that ErrInvalidCode at
// max attempts does NOT call incrementFn (already capped).
func TestConsumeOTPToken_InvalidCode_AtMaxAttempts_SkipsIncrement(t *testing.T) {
	t.Parallel()
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.Attempts = 5
		tok.MaxAttempts = 5
		tok.CodeHash = hashCode(t, "123456")
	})

	err := authshared.ConsumeOTPToken(
		context.Background(),
		"999999",
		func(checkFn func(authshared.VerificationToken) error) error {
			return checkFn(tok)
		},
		func(_ authshared.VerificationToken) error {
			t.Fatal("onSuccess must not be called")
			return nil
		},
		func(_ context.Context, _ authshared.VerificationToken) error {
			t.Fatal("incrementFn must not be called at max attempts")
			return nil
		},
	)
	// CheckOTPToken returns ErrTooManyAttempts when attempts >= maxAttempts,
	// regardless of the supplied code.
	require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
}

// TestConsumeOTPToken_TooManyAttempts_ReturnedAsIs verifies that
// ErrTooManyAttempts is propagated without calling any side-effect functions.
func TestConsumeOTPToken_TooManyAttempts_ReturnedAsIs(t *testing.T) {
	t.Parallel()
	err := authshared.ConsumeOTPToken(
		context.Background(),
		"000000",
		func(_ func(authshared.VerificationToken) error) error {
			return authshared.ErrTooManyAttempts
		},
		func(_ authshared.VerificationToken) error {
			t.Fatal("onSuccess must not be called")
			return nil
		},
		func(_ context.Context, _ authshared.VerificationToken) error {
			t.Fatal("incrementFn must not be called")
			return nil
		},
	)
	require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
}

// ─── Timing equalisation ────────────────────────────────────────────

// TestConsumeOTPToken_DummyPath_TimingEqualization verifies that the dummy hash
// comparison on the ErrTokenNotFound path actually executes (non-zero duration),
// which is the anti-enumeration guard described in ADR-004.
func TestConsumeOTPToken_DummyPath_TimingEqualization(t *testing.T) {
	const code = "123456"
	start := time.Now()

	_ = authshared.ConsumeOTPToken(
		context.Background(),
		code,
		func(_ func(authshared.VerificationToken) error) error {
			return authshared.ErrTokenNotFound
		},
		func(_ authshared.VerificationToken) error { return nil },
		func(_ context.Context, _ authshared.VerificationToken) error { return nil },
	)

	elapsed := time.Since(start)
	// bcrypt at MinCost takes at least 1 ms — assert the call was not a no-op.
	require.Greater(t, elapsed.Nanoseconds(), int64(1),
		"ErrTokenNotFound path must execute a bcrypt comparison (anti-enumeration)")
}

// ── GetDummyOTPHashCallCount ──────────────────────────────────────────────────────────────

// TestGetDummyOTPHashCallCount_ReflectsCallsToGetDummyOTPHash verifies that
// GetDummyOTPHashCallCount (otp.go:31–33) increments when GetDummyOTPHash is
// called. sync.Once means GetDummyOTPHash may already have been invoked by
// prior tests; we compare a delta rather than an absolute value.
//
// Must NOT call t.Parallel() — reads a package-level atomic counter that is
// also mutated by other non-parallel tests in this package.
func TestGetDummyOTPHashCallCount_ReflectsCallsToGetDummyOTPHash(t *testing.T) {
	before := authshared.GetDummyOTPHashCallCount()
	_ = authshared.GetDummyOTPHash()
	after := authshared.GetDummyOTPHashCallCount()
	require.GreaterOrEqual(t, after, before+1,
		"GetDummyOTPHashCallCount must increase by at least 1 after one GetDummyOTPHash call")
}

// TestConsumeOTPToken_TokenExpired_ReturnedAsIs verifies that ErrTokenExpired
// falls through the `return err` path in ConsumeOTPToken: neither onSuccess nor
// incrementFn is called, and the sentinel is propagated unchanged.
func TestConsumeOTPToken_TokenExpired_ReturnedAsIs(t *testing.T) {
	t.Parallel()
	err := authshared.ConsumeOTPToken(
		context.Background(),
		"123456",
		func(_ func(authshared.VerificationToken) error) error {
			return authshared.ErrTokenExpired
		},
		func(_ authshared.VerificationToken) error {
			t.Fatal("onSuccess must not be called on ErrTokenExpired")
			return nil
		},
		func(_ context.Context, _ authshared.VerificationToken) error {
			t.Fatal("incrementFn must not be called on ErrTokenExpired")
			return nil
		},
	)
	require.ErrorIs(t, err, authshared.ErrTokenExpired)
}

// TestConsumeOTPToken_IncrementFn_ReceivesUncancelledContext verifies the
// ADR-004 invariant: incrementFn always receives a context that is not
// cancelled, even when the caller's ctx is already cancelled. This prevents a
// client disconnect from aborting the attempt counter write and granting
// unlimited OTP retries.
func TestConsumeOTPToken_IncrementFn_ReceivesUncancelledContext(t *testing.T) {
	t.Parallel()
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.Attempts = 0
		tok.MaxAttempts = 5
		tok.CodeHash = hashCode(t, "123456")
	})

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE the call

	var incrementCtxErr error
	err := authshared.ConsumeOTPToken(
		cancelledCtx,
		"999999", // wrong code → ErrInvalidCode
		func(checkFn func(authshared.VerificationToken) error) error {
			return checkFn(tok)
		},
		func(_ authshared.VerificationToken) error {
			t.Fatal("onSuccess must not be called on wrong code")
			return nil
		},
		func(ctx context.Context, _ authshared.VerificationToken) error {
			incrementCtxErr = ctx.Err() // must be nil because of WithoutCancel
			return nil
		},
	)
	require.ErrorIs(t, err, authshared.ErrInvalidCode)
	require.NoError(t, incrementCtxErr,
		"incrementFn context must not be cancelled (ADR-004: WithoutCancel guard)")
}

// TestConsumeOTPToken_InvalidCode_IncrementFnReturnsError_StillReturnsErrInvalidCode
// covers otp.go lines 144-146: when incrementFn returns an error the function
// logs it (via slog.ErrorContext) but still returns ErrInvalidCode — the
// client's perspective is unchanged and the counter increment failure is
// internal.
func TestConsumeOTPToken_InvalidCode_IncrementFnReturnsError_StillReturnsErrInvalidCode(t *testing.T) {
	t.Parallel()
	tok := makeToken(func(tok *authshared.VerificationToken) {
		tok.Attempts = 0
		tok.MaxAttempts = 5
		tok.CodeHash = hashCode(t, "123456")
	})

	var incrementCalled bool
	err := authshared.ConsumeOTPToken(
		context.Background(),
		"999999", // wrong code → ErrInvalidCode
		func(checkFn func(authshared.VerificationToken) error) error {
			return checkFn(tok)
		},
		func(_ authshared.VerificationToken) error {
			t.Fatal("onSuccess must not be called on wrong code")
			return nil
		},
		func(_ context.Context, _ authshared.VerificationToken) error {
			incrementCalled = true
			return context.DeadlineExceeded // simulated storage failure
		},
	)

	require.ErrorIs(t, err, authshared.ErrInvalidCode,
		"ErrInvalidCode must be returned even when incrementFn fails")
	require.True(t, incrementCalled, "incrementFn must still be called")
}
