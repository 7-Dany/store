package authshared

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"golang.org/x/crypto/bcrypt"
)

// ── package-level vars ──────────────────────────────────────────────────────

const minProductionBcryptCost = 12

var bcryptCost = 12

var (
	dummyOTPHashOnce      sync.Once
	dummyOTPHashValue     string
	dummyOTPHashCallCount atomic.Int64
)

// GetDummyOTPHashCallCount returns the number of times GetDummyOTPHash has
// been called since process start. Used in tests to assert timing invariants.
func GetDummyOTPHashCallCount() int64 {
	return dummyOTPHashCallCount.Load()
}

// VerifyCodeHash reports whether code matches a bcrypt hash produced by
// GenerateCodeHash. bcrypt.CompareHashAndPassword performs a constant-time
// comparison internally.
//
// The length guard is the first check: bcrypt silently truncates input at
// 72 bytes, so any code that is not exactly 6 bytes is rejected before the
// bcrypt call is ever made.
func VerifyCodeHash(code, stored string) bool {
	if len(code) != 6 {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(stored), []byte(code)) == nil
}

// CheckOTPToken validates an OTP attempt against a VerificationToken.
// It checks expiry, then attempt budget, then hash equality.
// Returns a domain sentinel error or nil on success.
//
// Guard ordering:
//  1. Expired     → ErrTokenExpired     (no point checking code for dead token)
//  2. Exhausted   → ErrTooManyAttempts  (do not reveal whether code matches)
//  3. Wrong code  → ErrInvalidCode
//
// The caller is responsible for incrementing the attempt counter via
// store.IncrementAttemptsTx after this function returns ErrInvalidCode,
// outside the token-lock transaction per ADR-005.
func CheckOTPToken(token VerificationToken, code string, now time.Time) error {
	if now.After(token.ExpiresAt) {
		return ErrTokenExpired
	}
	if token.Attempts >= token.MaxAttempts {
		return ErrTooManyAttempts
	}
	if !VerifyCodeHash(code, token.CodeHash) {
		return ErrInvalidCode
	}
	return nil
}

// GenerateCodeHash returns a cryptographically random 6-digit OTP and its
// bcrypt hash. Feature packages call this to produce OTPs.
func GenerateCodeHash() (raw, hash string, err error) {
	if bcryptCost != bcrypt.MinCost && bcryptCost < minProductionBcryptCost {
		return "", "", fmt.Errorf("authshared.GenerateCodeHash: bcryptCost %d is below minimum %d", bcryptCost, minProductionBcryptCost)
	}
	n, randErr := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if randErr != nil {
		return "", "", telemetry.Service("GenerateCodeHash.rand_otp", randErr)
	}
	raw = fmt.Sprintf("%06d", n.Int64())
	b, hashErr := bcrypt.GenerateFromPassword([]byte(raw), bcryptCost)
	if hashErr != nil {
		return "", "", telemetry.Service("GenerateCodeHash.bcrypt", hashErr)
	}
	return raw, string(b), nil
}

// GenerateCodeHashForTest hashes an arbitrary code string at the current
// package-level bcrypt cost (controlled by SetBcryptCostForTest). Use in
// tests that need a predictable hash for a known code without calling
// GenerateCodeHash (which generates a random code).
// Must only be called from test helpers; the function name is intentionally
// marked ForTest to signal this restriction.
func GenerateCodeHashForTest(code string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(code), bcryptCost)
	if err != nil {
		return "", telemetry.Service("GenerateCodeHashForTest.hash", err)
	}
	return string(h), nil
}

// GetDummyOTPHash returns a stable bcrypt hash of a fixed sentinel OTP code,
// computed exactly once on first call. Used on the no-rows path of OTP-consuming
// service methods to equalise response latency (anti-enumeration).
//
// Always call SetBcryptCostForTest in TestMain before the first invocation.
func GetDummyOTPHash() string {
	dummyOTPHashCallCount.Add(1)
	dummyOTPHashOnce.Do(func() {
		_, h, err := GenerateCodeHash()
		if err != nil {
			panic("authshared: cannot init dummyOTPHash: " + err.Error())
		}
		dummyOTPHashValue = h
	})
	return dummyOTPHashValue
}

// ── OTP consumption choreography ────────────────────────────────────────────────────────────

// ConsumeOTPToken runs the standard OTP-consume choreography shared by the
// password-reset, account-unlock, and email-verification flows.
//
// It calls consumeFn with a closure that captures the token and calls
// CheckOTPToken. Depending on the outcome:
//
//   - Success: calls onSuccess(capturedToken) and returns its error.
//   - ErrTokenNotFound: runs a dummy hash comparison (anti-enumeration) and
//     returns ErrTokenNotFound.
//   - ErrInvalidCode (only when Attempts < MaxAttempts): calls incrementFn to
//     record the failed attempt, then returns ErrInvalidCode.
//   - All other errors (ErrTooManyAttempts, ErrTokenExpired, ErrTokenAlreadyUsed,
//     etc.): returned as-is.
//
// incrementFn always receives context.WithoutCancel(ctx) so that a client
// disconnect cannot abort the counter write and grant unlimited OTP retries.
func ConsumeOTPToken(
	ctx context.Context,
	code string,
	consumeFn func(checkFn func(VerificationToken) error) error,
	onSuccess func(token VerificationToken) error,
	incrementFn func(ctx context.Context, token VerificationToken) error,
) error {
	var captured VerificationToken

	err := consumeFn(func(tok VerificationToken) error {
		captured = tok
		return CheckOTPToken(tok, code, time.Now())
	})

	if err == nil {
		return onSuccess(captured)
	}

	if errors.Is(err, ErrTokenNotFound) {
		_ = VerifyCodeHash(code, GetDummyOTPHash())
		return ErrTokenNotFound
	}

	if errors.Is(err, ErrInvalidCode) && captured.Attempts < captured.MaxAttempts {
		// Security: detach from the request context so a client-timed disconnect
		// cannot abort the counter increment and grant unlimited OTP retries (ADR-004).
		if incErr := incrementFn(context.WithoutCancel(ctx), captured); incErr != nil {
			log.Warn(ctx, "ConsumeOTPToken: incrementFn failed", "error", incErr)
		}
		return ErrInvalidCode
	}

	return err
}
