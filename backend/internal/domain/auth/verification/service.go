package verification

import (
	"context"
	"errors"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
)

var log = telemetry.New("verification")

// resendCooldown is the minimum interval between resend requests for the same account.
const resendCooldown = 2 * time.Minute

// ── Storer interface ──────────────────────────────────────────────────────────

// Storer is the data-access contract that Service depends on.
type Storer interface {
	GetLatestTokenCreatedAt(ctx context.Context, userID [16]byte) (time.Time, error)
	GetUserForResend(ctx context.Context, email string) (ResendUser, error)
	IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error
	ResendVerificationTx(ctx context.Context, in ResendStoreInput, codeHash string) error
	VerifyEmailTx(ctx context.Context, email, ipAddress, userAgent string, checkFn func(authshared.VerificationToken) error) error
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service holds the business logic for email verification.
type Service struct {
	store    Storer
	tokenTTL time.Duration
}

// NewService constructs a Service with the given store and OTP token TTL.
// tokenTTL is sourced from config.Config.OTPValidMinutes at startup.
func NewService(store Storer, tokenTTL time.Duration) *Service {
	return &Service{store: store, tokenTTL: tokenTTL}
}

// VerifyEmail validates a one-time OTP against the stored token and, on
// success, marks the account as email-verified in the same transaction.
//
// Anti-enumeration, attempt-increment, and dummy-hash latency equalisation are
// handled by authshared.ConsumeOTPToken (ADR-005, ADR-006).
// VerifyEmailTx already marks the email as verified inside its transaction,
// so the onSuccess callback is a no-op.
func (s *Service) VerifyEmail(ctx context.Context, in VerifyEmailInput) error {
	log.Debug(ctx, "VerifyEmail: start", "email", in.Email, "ip", in.IPAddress)
	err := authshared.ConsumeOTPToken(
		ctx,
		in.Code,
		func(checkFn func(authshared.VerificationToken) error) error {
			return s.store.VerifyEmailTx(ctx, in.Email, in.IPAddress, in.UserAgent, checkFn)
		},
		func(_ authshared.VerificationToken) error {
			// VerifyEmailTx already marked the email as verified inside its
			// transaction -- nothing further to do on success.
			return nil
		},
		func(incCtx context.Context, tok authshared.VerificationToken) error {
			return s.store.IncrementAttemptsTx(incCtx, authshared.IncrementInput{
				TokenID:      tok.ID,
				UserID:       tok.UserID,
				Attempts:     tok.Attempts,
				MaxAttempts:  tok.MaxAttempts,
				IPAddress:    in.IPAddress,
				UserAgent:    in.UserAgent,
				AttemptEvent: audit.EventVerifyAttemptFailed,
			})
		},
	)
	if err == nil {
		log.Info(ctx, "VerifyEmail: success", "email", in.Email)
	}
	return err
}

// ResendVerification issues a fresh OTP for the given email address.
//
// Anti-enumeration: unknown email, already-verified accounts, and locked
// accounts all return a zero-value result with nil error. The caller must
// respond with a uniform 202 body regardless of which path was taken.
//
// The caller must inspect ResendResult.RawCode: an empty string means
// the request was silently suppressed and no email should be sent.
func (s *Service) ResendVerification(ctx context.Context, in ResendInput) (authshared.OTPIssuanceResult, error) {
	log.Debug(ctx, "ResendVerification: start", "email", in.Email, "ip", in.IPAddress)

	// 1. Look up the account. Unknown email -> silent no-op (anti-enumeration).
	user, err := s.store.GetUserForResend(ctx, in.Email)
	if err != nil {
		if errors.Is(err, authshared.ErrUserNotFound) {
			// Timing invariant: GetDummyOTPHash is called on the unknown-email path to
			// equalize response latency with the happy path, which calls GenerateCodeHash
			// (bcrypt at cost 12).
			_ = authshared.GetDummyOTPHash()
			log.Debug(ctx, "ResendVerification: suppressed — email not found", "email", in.Email)
			return authshared.OTPIssuanceResult{}, nil
		}
		return authshared.OTPIssuanceResult{}, telemetry.Service("ResendVerification.get_user", err)
	}

	// 2. Already verified or admin-locked -> silent no-op (anti-enumeration).
	if user.EmailVerified || user.IsLocked {
		log.Debug(ctx, "ResendVerification: suppressed — already verified or locked",
			"email", in.Email,
			"email_verified", user.EmailVerified,
			"is_locked", user.IsLocked,
		)
		return authshared.OTPIssuanceResult{}, nil
	}

	// 3. Cooldown: a fresh token was recently issued -- prevent flooding.
	createdAt, err := s.store.GetLatestTokenCreatedAt(ctx, user.ID)
	if err != nil {
		return authshared.OTPIssuanceResult{}, telemetry.Service("ResendVerification.get_latest_token", err)
	}
	if !createdAt.IsZero() && time.Since(createdAt) < resendCooldown {
		// Anti-enumeration: surfacing ErrResendTooSoon would allow an attacker to
		// distinguish known-recently-active addresses from all others within the
		// 2-minute window. Treat this path as a silent no-op identical to unknown email.
		log.Info(ctx, "ResendVerification: cooldown not elapsed, suppressing resend",
			"user_id", uuid.UUID(user.ID).String(),
			"email", in.Email,
		)
		return authshared.OTPIssuanceResult{}, nil
	}

	// 4. Generate fresh OTP.
	raw, codeHash, err := authshared.GenerateCodeHash()
	if err != nil {
		return authshared.OTPIssuanceResult{}, telemetry.Service("ResendVerification.generate_code", err)
	}

	// 5. Invalidate prior tokens, create new one, write audit row.
	if err := s.store.ResendVerificationTx(ctx, ResendStoreInput{
		UserID:    user.ID,
		Email:     in.Email,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
		TTL:       s.tokenTTL,
	}, codeHash); err != nil {
		return authshared.OTPIssuanceResult{}, telemetry.Service("ResendVerification.resend_tx", err)
	}

	log.Info(ctx, "ResendVerification: OTP issued",
		"user_id", uuid.UUID(user.ID).String(),
		"email", in.Email,
	)
	return authshared.NewOTPIssuanceResult(user.ID, in.Email, raw), nil
}
