package login

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// Storer is the data-access contract for the login service.
type Storer interface {
	LoginTx(ctx context.Context, in LoginTxInput) (LoggedInSession, error)
	GetUserForLogin(ctx context.Context, identifier string) (LoginUser, error)
	IncrementLoginFailuresTx(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
	ResetLoginFailuresTx(ctx context.Context, userID [16]byte) error
	WriteLoginFailedAuditTx(ctx context.Context, userID [16]byte, reason, ipAddress, userAgent string) error
}

// Service holds pure business logic for the login feature.
// It has no knowledge of HTTP, pgtype, pgxpool, or JWT signing.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// Login authenticates a user by email/username + password and, on success,
// returns raw session metadata for the handler to sign into JWTs.
//
// Timing invariant: CheckPassword is called REGARDLESS of whether the user was
// found. On no-rows, comparison runs against GetDummyPasswordHash() so that
// unknown-identifier and wrong-password responses have indistinguishable latency.
//
// Guard order (applied after the password check passes):
//  1. login_locked_until in the future → LoginLockedError    (HTTP 429 + Retry-After)
//  2. admin_locked                     → ErrAdminLocked      (HTTP 423 "admin_locked")
//  3. is_locked                        → ErrAccountLocked    (HTTP 423 "account_locked")
//  4. !email_verified                  → ErrEmailNotVerified (HTTP 403)
//  5. !is_active                       → ErrAccountInactive  (HTTP 403)
//
// Security trade-off: the login_locked_until guard (step 1) runs AFTER the
// password check passes. A user whose account is time-locked can confirm their
// password is correct by observing a 429 (LoginLockedError) rather than a 401
// (ErrInvalidCredentials). This is intentional (Option A): the lockout window
// itself limits exploitation, and the simpler guard order is easier to audit.
// See login.md §2.2 for the full analysis.
func (s *Service) Login(ctx context.Context, in LoginInput) (LoggedInSession, error) {
	slog.DebugContext(ctx, "login.Login: start", "identifier", in.Identifier, "ip", in.IPAddress)

	// 1. Look up the user. On no-rows, fall through to a dummy bcrypt compare
	// below so the timing is indistinguishable from a wrong-password attempt.
	user, lookupErr := s.store.GetUserForLogin(ctx, in.Identifier)

	// 2. Password check — always runs regardless of lookupErr.
	var pwHash string
	if lookupErr == nil {
		pwHash = user.PasswordHash
	} else {
		// Timing invariant: use the dummy hash on the no-rows path so that
		// unknown-identifier and wrong-password responses have indistinguishable
		// latency (§3.7).
		pwHash = authshared.GetDummyPasswordHash()
	}
	pwErr := authshared.CheckPassword(pwHash, in.Password)

	// 3. Surface lookup failure as ErrInvalidCredentials (anti-enumeration).
	// We waited until after CheckPassword so timing is already equalised.
	if errors.Is(lookupErr, authshared.ErrUserNotFound) {
		return LoggedInSession{}, authshared.ErrInvalidCredentials
	}
	if lookupErr != nil {
		return LoggedInSession{}, fmt.Errorf("login.Login: get user: %w", lookupErr)
	}

	// 4. Wrong password → increment failure counter, return ErrInvalidCredentials.
	if pwErr != nil {
		slog.DebugContext(ctx, "login.Login: wrong password", "user_id", uuid.UUID(user.ID).String())
		if !errors.Is(pwErr, authshared.ErrInvalidCredentials) {
			// Malformed hash is a data-integrity alert, not a user error.
			return LoggedInSession{}, fmt.Errorf("login.Login: password check: %w", pwErr)
		}
		// Security: context.WithoutCancel prevents a client-timed disconnect from
		// aborting the counter increment and granting unlimited wrong-password attempts.
		if incErr := s.store.IncrementLoginFailuresTx(
			context.WithoutCancel(ctx),
			user.ID,
			in.IPAddress,
			in.UserAgent,
		); incErr != nil {
			slog.ErrorContext(ctx, "login.Login: IncrementLoginFailuresTx", "error", incErr)
		}
		return LoggedInSession{}, authshared.ErrInvalidCredentials
	}

	// 5. Time-based lockout check (post-auth, pre-guard).
	// Checked before is_locked so login_locked_until errors surface as 429
	// with a Retry-After header rather than 423 (permanent admin lock).
	if user.LoginLockedUntil != nil && user.LoginLockedUntil.After(time.Now()) {
		retryAfter := time.Until(*user.LoginLockedUntil)
		slog.DebugContext(ctx, "login.Login: guard rejected — time_locked",
			"user_id", uuid.UUID(user.ID).String(),
			"retry_after", retryAfter.Round(time.Second).String(),
		)
		return LoggedInSession{}, &authshared.LoginLockedError{RetryAfter: retryAfter}
	}

	// 6. Guard checks. Each failure writes a login_failed audit row so that
	// credential-stuffing and brute-force patterns remain detectable.
	switch {
	case user.AdminLocked:
		// Admin-imposed lock checked before OTP lock: the unlock OTP flow cannot
		// clear admin_locked, so surfacing this first gives the clearest guidance.
		slog.DebugContext(ctx, "login.Login: guard rejected — admin_locked", "user_id", uuid.UUID(user.ID).String())
		if auditErr := s.store.WriteLoginFailedAuditTx(
			context.WithoutCancel(ctx),
			user.ID,
			"admin_locked",
			in.IPAddress,
			in.UserAgent,
		); auditErr != nil {
			slog.ErrorContext(ctx, "login.Login: write login_failed audit", "error", auditErr)
		}
		return LoggedInSession{}, authshared.ErrAdminLocked
	case user.IsLocked:
		slog.DebugContext(ctx, "login.Login: guard rejected — account_locked", "user_id", uuid.UUID(user.ID).String())
		// Security: WithoutCancel so audit write survives a client disconnect.
		if auditErr := s.store.WriteLoginFailedAuditTx(
			context.WithoutCancel(ctx),
			user.ID,
			"account_locked",
			in.IPAddress,
			in.UserAgent,
		); auditErr != nil {
			slog.ErrorContext(ctx, "login.Login: write login_failed audit", "error", auditErr)
		}
		return LoggedInSession{}, authshared.ErrAccountLocked
	case !user.EmailVerified:
		slog.DebugContext(ctx, "login.Login: guard rejected — email_not_verified", "user_id", uuid.UUID(user.ID).String())
		if auditErr := s.store.WriteLoginFailedAuditTx(
			context.WithoutCancel(ctx), user.ID, "email_not_verified",
			in.IPAddress, in.UserAgent,
		); auditErr != nil {
			slog.ErrorContext(ctx, "login.Login: write login_failed audit", "error", auditErr)
		}
		return LoggedInSession{}, authshared.ErrEmailNotVerified
	case !user.IsActive:
		slog.DebugContext(ctx, "login.Login: guard rejected — account_inactive", "user_id", uuid.UUID(user.ID).String())
		if auditErr := s.store.WriteLoginFailedAuditTx(
			context.WithoutCancel(ctx), user.ID, "account_inactive",
			in.IPAddress, in.UserAgent,
		); auditErr != nil {
			slog.ErrorContext(ctx, "login.Login: write login_failed audit", "error", auditErr)
		}
		return LoggedInSession{}, authshared.ErrAccountInactive
	}

	// 7. Persist session + refresh token + audit log in a single transaction.
	session, err := s.store.LoginTx(ctx, LoginTxInput{
		UserID:    user.ID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
	})
	if err != nil {
		return LoggedInSession{}, fmt.Errorf("login.Login: login tx: %w", err)
	}

	// 8. Clear the failure counter now that login succeeded.
	// Security: WithoutCancel so a client disconnect cannot skip the reset.
	if resetErr := s.store.ResetLoginFailuresTx(context.WithoutCancel(ctx), user.ID); resetErr != nil {
		slog.ErrorContext(ctx, "login.Login: ResetLoginFailuresTx", "error", resetErr)
	}

	slog.InfoContext(ctx, "login.Login: success",
		"user_id", uuid.UUID(user.ID).String(),
		"session_id", uuid.UUID(session.SessionID).String(),
	)

	// 9. Propagate pending-deletion timestamp so the handler can include
	// scheduled_deletion_at in the login response (D-04).
	if user.DeletedAt != nil {
		t := user.DeletedAt.Add(30 * 24 * time.Hour)
		session.ScheduledDeletionAt = &t
	}

	return session, nil
}
