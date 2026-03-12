package password

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// changePasswordMaxAttempts is the per-user threshold for consecutive wrong
// old-password submissions on POST /change-password. Once reached the service
// returns ErrTooManyAttempts and the handler instructs the client to use the
// forgot-password flow instead.
//
// The IP rate limiter (5 req / 15 min, key prefix cpw:ip:) is the outer
// defence; this per-user counter is the inner guard against a single attacker
// who has a valid JWT but is brute-forcing the old password.
const changePasswordMaxAttempts int16 = 5

// Storer is the data-access contract that Service depends on.
// *Store satisfies this interface; tests may supply a fake implementation.
type Storer interface {
	GetUserForPasswordReset(ctx context.Context, email string) (GetUserForPasswordResetResult, error)
	// GetPasswordResetTokenCreatedAt returns the created_at of the most recent
	// active (used_at IS NULL) password reset token for email.
	// Returns authshared.ErrTokenNotFound when no active token exists.
	GetPasswordResetTokenCreatedAt(ctx context.Context, email string) (time.Time, error)
	RequestPasswordResetTx(ctx context.Context, in RequestPasswordResetStoreInput) error
	// ConsumeAndUpdatePasswordTx atomically validates the OTP, consumes the reset
	// token, checks for same-password reuse, updates the password hash, revokes all
	// active refresh tokens, ends all sessions, and writes both the
	// password_reset_confirmed and password_changed audit rows — in one transaction.
	// in.NewHash must be pre-computed by the caller outside this call so that the
	// slow bcrypt generation (~300 ms) does not hold the transaction open.
	// Returns the affected user's ID on success.
	ConsumeAndUpdatePasswordTx(ctx context.Context, in ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error)
	IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error
	// GetPasswordResetTokenForVerify returns the active token for email without
	// locking it. Returns authshared.ErrTokenNotFound when no active token exists.
	GetPasswordResetTokenForVerify(ctx context.Context, email string) (authshared.VerificationToken, error)

	// GetUserPasswordHash returns the current password hash for the user.
	// Returns authshared.ErrUserNotFound on no-rows.
	GetUserPasswordHash(ctx context.Context, userID [16]byte) (CurrentCredentials, error)
	// UpdatePasswordHashTx updates the user's password hash, revokes all active
	// refresh tokens, ends all sessions, and writes a password_changed audit row —
	// all in one transaction.
	UpdatePasswordHashTx(ctx context.Context, userID [16]byte, newHash, ipAddress, userAgent string) error
	// IncrementChangePasswordFailuresTx writes a password_change_failed audit row,
	// increments the per-user failed-attempt counter, and returns the new count.
	// The service compares the returned count against changePasswordMaxAttempts to
	// decide whether to return ErrTooManyAttempts (redirect to forgot-password) or
	// the standard ErrInvalidCredentials.
	//
	// NOTE: counter persistence requires a DB migration:
	//   ALTER TABLE users ADD COLUMN failed_change_password_attempts int2 NOT NULL DEFAULT 0;
	// Until that migration is applied the real Store returns a stub count and the
	// IP rate limiter (5 req/15 min, cpw:ip:) is the primary per-user defence.
	IncrementChangePasswordFailuresTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) (int16, error)
	// ResetChangePasswordFailuresTx resets the per-user failed-attempt counter to
	// zero after a successful password change so the user gets a clean slate.
	//
	// NOTE: no-op until the DB migration above is applied.
	ResetChangePasswordFailuresTx(ctx context.Context, userID [16]byte) error
}

// Service holds the business logic for the password reset flow.
// It has no knowledge of HTTP, pgtype, pgxpool, or JWT signing.
type Service struct {
	store    Storer
	tokenTTL time.Duration
}

// NewService constructs a Service with the given store and OTP token TTL.
// tokenTTL is sourced from config.Config.OTPTokenTTL at startup.
func NewService(store Storer, tokenTTL time.Duration) *Service {
	return &Service{store: store, tokenTTL: tokenTTL}
}

// ── RequestPasswordReset ──────────────────────────────────────────────────────

// RequestPasswordReset issues a password-reset OTP for the given email address.
//
// Anti-enumeration: unknown email, unverified, locked, and inactive accounts
// all return a zero-value result with nil error. The caller must respond with a
// uniform 202 body regardless of which path was taken.
//
// Timing invariant: authshared.GetDummyOTPHash is called on the unknown-email
// path so that the no-rows response latency matches the happy path, which calls
// authshared.GenerateCodeHash (bcrypt at otpBcryptCost). An attacker measuring
// response times cannot distinguish registered from unknown emails.
func (s *Service) RequestPasswordReset(ctx context.Context, in ForgotPasswordInput) (authshared.OTPIssuanceResult, error) {
	slog.DebugContext(ctx, "password.RequestPasswordReset: start", "email", in.Email, "ip", in.IPAddress)

	// 1. Look up the account. Unknown email → silent no-op (anti-enumeration).
	user, err := s.store.GetUserForPasswordReset(ctx, in.Email)
	if err != nil {
		if errors.Is(err, authshared.ErrUserNotFound) {
			// Timing invariant: always call GetDummyOTPHash on the no-rows path to
			// match the bcrypt cost of authshared.GenerateCodeHash on the happy path
			// (Conventions §7, ADR-006).
			_ = authshared.GetDummyOTPHash()
			slog.DebugContext(ctx, "password.RequestPasswordReset: suppressed — email not found", "email", in.Email)
			return authshared.OTPIssuanceResult{}, nil
		}
		return authshared.OTPIssuanceResult{}, fmt.Errorf("password.RequestPasswordReset: get user: %w", err)
	}

	// 2. Unverified, locked, or inactive → silent no-op (anti-enumeration).
	if !user.EmailVerified || user.IsLocked || !user.IsActive {
		slog.DebugContext(ctx, "password.RequestPasswordReset: suppressed — account not eligible",
			"email", in.Email,
			"email_verified", user.EmailVerified,
			"is_locked", user.IsLocked,
			"is_active", user.IsActive,
		)
		return authshared.OTPIssuanceResult{}, nil
	}

	// 2b. Cooldown guard: if a token was issued within the last 60 seconds,
	// suppress silently to reduce token-flooding attacks (anti-enumeration).
	const resetCooldown = 60 * time.Second
	issuedAt, cooldownErr := s.store.GetPasswordResetTokenCreatedAt(ctx, in.Email)
	if cooldownErr == nil && time.Since(issuedAt) < resetCooldown {
		slog.DebugContext(ctx, "password.RequestPasswordReset: suppressed — cooldown active",
			"email", in.Email,
			"issued_at", issuedAt,
			"elapsed", time.Since(issuedAt).Round(time.Second),
			"cooldown", resetCooldown,
		)
		return authshared.OTPIssuanceResult{}, nil
	}
	if cooldownErr != nil && !errors.Is(cooldownErr, authshared.ErrTokenNotFound) {
		return authshared.OTPIssuanceResult{},
			fmt.Errorf("password.RequestPasswordReset: cooldown check: %w", cooldownErr)
	}

	// 3. Generate OTP.
	slog.DebugContext(ctx, "password.RequestPasswordReset: generating OTP", "email", in.Email)
	raw, codeHash, err := authshared.GenerateCodeHash()
	if err != nil {
		return authshared.OTPIssuanceResult{}, fmt.Errorf("password.RequestPasswordReset: generate code hash: %w", err)
	}

	// 4. Persist token + audit row.
	slog.DebugContext(ctx, "password.RequestPasswordReset: persisting token", "email", in.Email)
	if err := s.store.RequestPasswordResetTx(ctx, RequestPasswordResetStoreInput{
		UserID:    user.ID,
		Email:     in.Email,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
		CodeHash:  codeHash,
		TTL:       s.tokenTTL,
	}); err != nil {
		if errors.Is(err, authshared.ErrResetTokenCooldown) {
			// Cooldown: a valid token already exists; return silently (anti-enumeration).
			slog.DebugContext(ctx, "password.RequestPasswordReset: suppressed — active token already exists (DB cooldown)", "email", in.Email)
			return authshared.OTPIssuanceResult{}, nil
		}
		return authshared.OTPIssuanceResult{},
			fmt.Errorf("password.RequestPasswordReset: request password reset tx: %w", err)
	}

	slog.InfoContext(ctx, "password.RequestPasswordReset: OTP issued", "email", in.Email)
	return authshared.NewOTPIssuanceResult(user.ID, in.Email, raw), nil
}

// ── UpdatePasswordHash ────────────────────────────────────────────────────────────

// UpdatePasswordHash verifies the caller's current password and replaces it
// with a new one, revoking all active sessions on success.
//
// Timing invariant: CheckPassword always runs, even if the user is not found.
// The dummy password hash is used on the no-rows path.
func (s *Service) UpdatePasswordHash(ctx context.Context, in ChangePasswordInput) error {
	uid, err := authshared.ParseUserID("password.UpdatePasswordHash", in.UserID)
	if err != nil {
		return err
	}

	creds, lookupErr := s.store.GetUserPasswordHash(ctx, uid)

	// Timing invariant: always run CheckPassword, even on no-rows, to prevent
	// timing-based email enumeration (§3.7).
	var pwHash string
	if lookupErr == nil {
		pwHash = creds.PasswordHash
	} else {
		pwHash = authshared.GetDummyPasswordHash() // constant-time placeholder
	}
	pwErr := authshared.CheckPassword(pwHash, in.OldPassword)

	if errors.Is(lookupErr, authshared.ErrUserNotFound) {
		return authshared.ErrUserNotFound
	}
	if lookupErr != nil {
		return fmt.Errorf("password.UpdatePasswordHash: get password hash: %w", lookupErr)
	}

	if pwErr != nil {
		if !errors.Is(pwErr, authshared.ErrInvalidCredentials) {
			return fmt.Errorf("password.UpdatePasswordHash: password check: %w", pwErr)
		}
		// Security: WithoutCancel so a client disconnect cannot abort the failed-attempt record.
		count, incrErr := s.store.IncrementChangePasswordFailuresTx(
			context.WithoutCancel(ctx), uid, in.IPAddress, in.UserAgent,
		)
		if incrErr != nil {
			slog.ErrorContext(ctx, "password.UpdatePasswordHash: increment change password failures", "error", incrErr)
		}
		if count >= changePasswordMaxAttempts {
			return authshared.ErrTooManyAttempts
		}
		return authshared.ErrInvalidCredentials
	}

	// Same-password reuse check: reject if the new password matches the current one.
	// Changing to the same password would revoke all sessions for no security benefit.
	if authshared.CheckPassword(pwHash, in.NewPassword) == nil {
		return ErrSamePassword
	}

	if valErr := authshared.ValidatePassword(in.NewPassword); valErr != nil {
		return valErr
	}

	newHash, err := authshared.HashPassword(in.NewPassword)
	if err != nil {
		// Unreachable in practice: ValidatePassword enforces ≤72-byte passwords,
		// so bcrypt.GenerateFromPassword never returns an error here.
		return fmt.Errorf("password.UpdatePasswordHash: hash password: %w", err)
	}

	// Security: WithoutCancel so a client disconnect cannot abort the revocation.
	if err := s.store.UpdatePasswordHashTx(
		context.WithoutCancel(ctx),
		uid,
		newHash,
		in.IPAddress,
		in.UserAgent,
	); err != nil {
		return fmt.Errorf("password.UpdatePasswordHash: update password: %w", err)
	}

	// Reset the per-user attempt counter so the user starts fresh on next change.
	// Best-effort: a reset failure must not prevent the 200 response.
	if resetErr := s.store.ResetChangePasswordFailuresTx(
		context.WithoutCancel(ctx), uid,
	); resetErr != nil {
		slog.ErrorContext(ctx, "password.UpdatePasswordHash: reset change password failures", "error", resetErr)
	}
	return nil
}

// ── VerifyResetCode ────────────────────────────────────────────────────────────────────────────────────

// VerifyResetCode validates the password-reset OTP without consuming it.
// Returns the email address bound to the token on success so the handler can
// issue a short-lived grant token scoped to that address.
//
// Timing invariant: GetDummyOTPHash is called on the ErrTokenNotFound path to
// match the latency of VerifyCodeHash on the happy path (ADR-006).
//
// Attempt increment: a wrong code with remaining budget calls IncrementAttemptsTx
// via context.WithoutCancel (ADR-004, ADR-005).
func (s *Service) VerifyResetCode(ctx context.Context, in VerifyResetCodeInput) (string, error) {
	// 1. Fetch the active token (no FOR UPDATE — read-only check).
	tok, err := s.store.GetPasswordResetTokenForVerify(ctx, in.Email)
	if err != nil {
		if errors.Is(err, authshared.ErrTokenNotFound) {
			// Timing invariant: always run a dummy hash compare on the no-rows path
			// to match the latency of VerifyCodeHash on the happy path (ADR-006).
			_ = authshared.GetDummyOTPHash()
			return "", authshared.ErrTokenNotFound
		}
		return "", fmt.Errorf("password.VerifyResetCode: get token: %w", err)
	}

	// 2. Check expiry, attempt budget, and code hash.
	checkErr := authshared.CheckOTPToken(tok, in.Code, time.Now())

	// 3. Wrong code with remaining budget: increment attempt counter and return.
	if errors.Is(checkErr, authshared.ErrInvalidCode) && tok.Attempts < tok.MaxAttempts {
		// Security: detach from the request context so a client disconnect cannot
		// abort the counter increment and grant unlimited OTP retries (ADR-004).
		if incErr := s.store.IncrementAttemptsTx(
			context.WithoutCancel(ctx),
			authshared.IncrementInput{
				TokenID:      tok.ID,
				UserID:       tok.UserID,
				Attempts:     tok.Attempts,
				MaxAttempts:  tok.MaxAttempts,
				IPAddress:    in.IPAddress,
				UserAgent:    in.UserAgent,
				AttemptEvent: audit.EventPasswordResetAttemptFailed,
			},
		); incErr != nil {
			slog.ErrorContext(ctx, "password.VerifyResetCode: increment attempts", "error", incErr)
		}
		return "", authshared.ErrInvalidCode
	}

	// 4. Any other CheckOTPToken error (ErrTokenExpired, ErrTooManyAttempts): return as-is.
	if checkErr != nil {
		return "", checkErr
	}

	// 5. Success: return the email so the handler can issue a grant token.
	return in.Email, nil
}

// ── ConsumePasswordResetToken ────────────────────────────────────────────────────────────────────────────

// ConsumePasswordResetToken validates the password-reset OTP and, on success,
// updates the password hash and revokes all active sessions atomically.
// Returns the affected user's ID as a [16]byte on success (zero value on error),
// so the handler can immediately invalidate outstanding access tokens via the blocklist.
//
// Anti-enumeration, attempt-increment, and dummy-hash latency equalisation are
// handled by authshared.ConsumeOTPToken (ADR-005, ADR-006).
//
// Atomicity: token consumption and the password hash update execute in a single
// transaction inside ConsumeAndUpdatePasswordTx. There is no partial-failure
// window — either both commit or neither does. The new password hash is
// pre-computed here (outside the TX) so the slow bcrypt generation (~300 ms)
// does not hold a DB lock; the TX itself only holds two bcrypt compares
// (~200 ms: OTP code + same-password check) plus the DB writes.
//
// Note: account-lock state is not re-checked here; the handler has no
// locked-account branch for this endpoint.
func (s *Service) ConsumePasswordResetToken(ctx context.Context, in ResetPasswordInput) ([16]byte, error) {
	// 1. Validate password strength before any DB work.
	if err := authshared.ValidatePassword(in.NewPassword); err != nil {
		return [16]byte{}, err
	}

	// 2. Pre-compute the bcrypt hash outside any transaction.
	// bcrypt at cost ≥12 takes ~300 ms; keeping it here means the single combined
	// transaction only holds two bcrypt compares plus DB writes — no long-lived lock.
	// Unreachable: ValidatePassword (step 1) rejects empty and too-short passwords
	// before HashPassword is called. A validated password cannot cause HashPassword
	// to fail on any supported platform (crypto/rand failure requires OS-level fault).
	newHash, err := authshared.HashPassword(in.NewPassword)
	if err != nil {
		return [16]byte{}, fmt.Errorf("password.ConsumePasswordResetToken: hash password: %w", err)
	}

	var userID [16]byte
	err = authshared.ConsumeOTPToken(
		ctx,
		in.Code,
		func(checkFn func(authshared.VerificationToken) error) error {
			var storeErr error
			userID, storeErr = s.store.ConsumeAndUpdatePasswordTx(ctx, ConsumeAndUpdateInput{
				Email:       in.Email,
				NewPassword: in.NewPassword,
				NewHash:     newHash,
				IPAddress:   in.IPAddress,
				UserAgent:   in.UserAgent,
			}, checkFn)
			return storeErr
		},
		func(_ authshared.VerificationToken) error {
			// userID is captured inside ConsumeAndUpdatePasswordTx; nothing to do here.
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
				AttemptEvent: audit.EventPasswordResetAttemptFailed,
			})
		},
	)
	return userID, err
}
