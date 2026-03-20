package unlock

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/7-Dany/store/backend/internal/audit"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

var log = telemetry.New("unlock")

// Storer is the data-access contract that Service depends on.
// *Store satisfies this interface; tests may supply a fake implementation.
type Storer interface {
	GetUserForUnlock(ctx context.Context, email string) (UnlockUser, error)
	GetUnlockToken(ctx context.Context, email string) (authshared.VerificationToken, error)
	RequestUnlockTx(ctx context.Context, in RequestUnlockStoreInput) error
	ConsumeUnlockTokenTx(ctx context.Context, email string, checkFn func(authshared.VerificationToken) error) error
	UnlockAccountTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
	IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error
}

// Service holds the business logic for the unlock flow.
// It has no knowledge of HTTP, pgtype, pgxpool, or JWT signing.
type Service struct {
	store    Storer
	tokenTTL time.Duration
}

// NewService constructs a Service with the given store and OTP token TTL.
// Pass 15*time.Minute for the production default.
func NewService(store Storer, tokenTTL time.Duration) *Service {
	return &Service{store: store, tokenTTL: tokenTTL}
}

// RequestUnlock issues an account-unlock OTP for the given email address.
//
// Anti-enumeration: the following four paths all return a zero-value result
// with nil error. The caller must respond with a uniform 202 body regardless
// of which path was taken:
//  1. Unknown email.
//  2. Email address not verified.
//  3. Account not locked.
//  4. Active token already exists (cooldown).
//
// The caller must inspect OTPIssuanceResult.RawCode: an empty string means
// the request was silently suppressed and no email should be sent.
//
// Timing invariant: GetDummyOTPHash is called on both the unknown-email path
// (step 1) and the not-locked path (step 3) to equalise response latency
// with the happy path, which calls GenerateCodeHash (bcrypt at cost 12).
func (s *Service) RequestUnlock(ctx context.Context, in RequestUnlockInput) (authshared.OTPIssuanceResult, error) {
	log.Debug(ctx, "RequestUnlock: start", "email", in.Email, "ip", in.IPAddress)

	// 1. Look up the account. Unknown email → silent no-op (anti-enumeration).
	user, err := s.store.GetUserForUnlock(ctx, in.Email)
	if err != nil {
		if errors.Is(err, authshared.ErrUserNotFound) {
			// Timing invariant: GetDummyOTPHash is called on the unknown-email path to
			// equalize response latency with the happy path, which calls GenerateCodeHash
			// (bcrypt at cost 12).
			_ = authshared.GetDummyOTPHash()
			log.Debug(ctx, "RequestUnlock: suppressed — email not found", "email", in.Email)
			return authshared.OTPIssuanceResult{}, nil
		}
		return authshared.OTPIssuanceResult{}, telemetry.Service("RequestUnlock.get_user", err)
	}

	// 2. Guard: unverified accounts cannot self-unlock — they have not proven
	// ownership of the email address (anti-enumeration: same silent no-op).
	if !user.EmailVerified {
		_ = authshared.GetDummyOTPHash()
		log.Debug(ctx, "RequestUnlock: suppressed — email not verified", "email", in.Email)
		return authshared.OTPIssuanceResult{}, nil
	}

	// 3. Admin-locked accounts cannot self-unlock — only admin action (RBAC) can
	// clear admin_locked. The user-facing OTP flow must never clear this field.
	// Anti-enumeration: same silent no-op as the other suppression paths.
	if user.AdminLocked {
		_ = authshared.GetDummyOTPHash()
		log.Debug(ctx, "RequestUnlock: suppressed — admin locked (not eligible for self-unlock)", "email", in.Email)
		return authshared.OTPIssuanceResult{}, nil
	}

	// 4. Account not locked → silent no-op (anti-enumeration).
	timeLocked := user.LoginLockedUntil != nil && user.LoginLockedUntil.After(time.Now())
	if !user.IsLocked && !timeLocked {
		// Timing invariant: GetDummyOTPHash equalises response latency with the
		// locked path, which calls GenerateCodeHash (bcrypt at cost 12).
		// Without this, a caller who knows the email is registered can distinguish
		// "not locked" (fast) from "locked" (slow) by timing the response.
		_ = authshared.GetDummyOTPHash()
		log.Debug(ctx, "RequestUnlock: suppressed — account not locked", "email", in.Email)
		return authshared.OTPIssuanceResult{}, nil
	}

	// 5. Active token guard — if an unconsumed, non-expired token already exists,
	// return silently (anti-enumeration). This also prevents concurrent callers
	// from flooding the inbox with multiple simultaneous OTP emails.
	_, err = s.store.GetUnlockToken(ctx, in.Email)
	if err == nil {
		log.Debug(ctx, "RequestUnlock: suppressed — active token already exists", "email", in.Email)
		return authshared.OTPIssuanceResult{}, nil
	}
	if !errors.Is(err, authshared.ErrTokenNotFound) {
		return authshared.OTPIssuanceResult{}, telemetry.Service("RequestUnlock.check_active_token", err)
	}
	// err == ErrTokenNotFound → no active token, safe to issue one.

	// 6. Generate OTP.
	raw, codeHash, err := authshared.GenerateCodeHash()
	if err != nil {
		return authshared.OTPIssuanceResult{}, telemetry.Service("RequestUnlock.generate_code", err)
	}

	// 7. Persist token + audit row.
	if err := s.store.RequestUnlockTx(ctx, RequestUnlockStoreInput{
		UserID:    user.ID,
		Email:     in.Email,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
		CodeHash:  codeHash,
		TTL:       s.tokenTTL,
	}); err != nil {
		return authshared.OTPIssuanceResult{}, telemetry.Service("RequestUnlock.unlock_tx", err)
	}

	log.Info(ctx, "RequestUnlock: OTP issued",
		"user_id", uuid.UUID(user.ID).String(),
		"email", in.Email,
	)
	return authshared.NewOTPIssuanceResult(user.ID, in.Email, raw), nil
}

// ConsumeUnlockToken validates the unlock OTP and, on success, atomically
// clears the lock and writes both audit rows inside the store transaction.
//
// Anti-enumeration, attempt-increment, and dummy-hash latency equalisation are
// handled by authshared.ConsumeOTPToken (ADR-005, ADR-006).
func (s *Service) ConsumeUnlockToken(ctx context.Context, in ConfirmUnlockInput) error {
	log.Debug(ctx, "ConsumeUnlockToken: start", "email", in.Email, "ip", in.IPAddress)
	return authshared.ConsumeOTPToken(
		ctx,
		in.Code,
		func(checkFn func(authshared.VerificationToken) error) error {
			// Token consumption validation happens inside the transaction.
			return s.store.ConsumeUnlockTokenTx(ctx, in.Email, checkFn)
		},
		func(captured authshared.VerificationToken) error {
			// Security: detach from the request context so a client disconnect
			// cannot abort the account unlock write after the token was consumed.
			return s.store.UnlockAccountTx(context.WithoutCancel(ctx), captured.UserID, in.IPAddress, in.UserAgent)
		},
		func(incCtx context.Context, tok authshared.VerificationToken) error {
			return s.store.IncrementAttemptsTx(incCtx, authshared.IncrementInput{
				TokenID:      tok.ID,
				UserID:       tok.UserID,
				Attempts:     tok.Attempts,
				MaxAttempts:  tok.MaxAttempts,
				IPAddress:    in.IPAddress,
				UserAgent:    in.UserAgent,
				AttemptEvent: audit.EventUnlockAttemptFailed,
			})
		},
	)
}
