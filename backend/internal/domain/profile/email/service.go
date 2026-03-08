package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/7-Dany/store/backend/internal/audit"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
)

// Storer is the data-access contract for the email-change feature.
// *Store satisfies this interface; tests use EmailChangeFakeStorer from
// internal/domain/auth/shared/testutil.
type Storer interface {
	// GetCurrentUserEmail returns the current email address of userID.
	// Returns profileshared.ErrUserNotFound when the user row does not exist.
	GetCurrentUserEmail(ctx context.Context, userID [16]byte) (string, error)

	// CheckEmailAvailableForChange returns true when no active user other than
	// the caller holds newEmail. The result is a point-in-time read; the mutation
	// path enforces uniqueness via SetUserEmail (23505 on idx_users_email_active).
	CheckEmailAvailableForChange(ctx context.Context, newEmail string, userID [16]byte) (bool, error)

	// GetLatestEmailChangeVerifyTokenCreatedAt returns the created_at of the most
	// recent active email_change_verify token for the user. Returns
	// authshared.ErrTokenNotFound when no active token exists.
	GetLatestEmailChangeVerifyTokenCreatedAt(ctx context.Context, userID [16]byte) (time.Time, error)

	// RequestEmailChangeTx atomically invalidates any existing verify tokens,
	// creates a new one with new_email in its metadata, and writes an audit row.
	RequestEmailChangeTx(ctx context.Context, in RequestEmailChangeTxInput) error

	// VerifyCurrentEmailTx validates and consumes the email_change_verify token,
	// creates a new email_change_confirm token, and writes an audit row.
	VerifyCurrentEmailTx(ctx context.Context, in VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (VerifyCurrentEmailStoreResult, error)

	// IncrementAttemptsTx records a failed OTP attempt and optionally locks the
	// account. Always commits independently of the caller's context (ADR-003).
	IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error

	// ConfirmEmailChangeTx validates and consumes the email_change_confirm token,
	// swaps the user's email, revokes all refresh tokens and sessions, and writes
	// an audit row.
	ConfirmEmailChangeTx(ctx context.Context, in ConfirmEmailChangeTxInput, checkFn func(authshared.VerificationToken) error) error
}

// Service holds the business logic for the email-change flow.
// It has no knowledge of HTTP, pgtype, pgxpool, or JWT signing.
type Service struct {
	store          Storer
	kv             kvstore.Store
	blocklist      kvstore.TokenBlocklist // nil-safe; may be nil for in-memory backend
	tokenTTL       time.Duration          // OTP token lifetime (from config.OTPTokenTTL)
	accessTokenTTL time.Duration          // JWT access token lifetime (for blocklist TTL)
}

// NewService constructs a Service backed by the given dependencies.
// blocklist may be nil (in-memory KV backend does not implement TokenBlocklist).
// tokenTTL is sourced from deps.OTPTokenTTL; accessTokenTTL from
// deps.JWTConfig.AccessTokenExpiry.
func NewService(
	store Storer,
	kv kvstore.Store,
	blocklist kvstore.TokenBlocklist,
	tokenTTL time.Duration,
	accessTokenTTL time.Duration,
) *Service {
	return &Service{
		store:          store,
		kv:             kv,
		blocklist:      blocklist,
		tokenTTL:       tokenTTL,
		accessTokenTTL: accessTokenTTL,
	}
}

// RequestEmailChange implements step 1 of the email-change flow.
// It validates the requested new email, checks the cooldown, generates an OTP,
// and persists the verify token in a transaction. The caller is responsible for
// enqueuing the OTP email to result.CurrentEmail.
func (s *Service) RequestEmailChange(ctx context.Context, in EmailChangeRequestInput) (EmailChangeRequestResult, error) {
	// 1. Validate and normalise new email.
	normalised, err := NormaliseAndValidateNewEmail(in.NewEmail)
	if err != nil {
		return EmailChangeRequestResult{}, err
	}

	// 2. Fetch the user's current email.
	currentEmail, err := s.store.GetCurrentUserEmail(ctx, in.UserID)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return EmailChangeRequestResult{}, profileshared.ErrUserNotFound
		}
		return EmailChangeRequestResult{}, fmt.Errorf("email.RequestEmailChange: get current email: %w", err)
	}

	// 3. Reject no-op changes.
	if normalised == currentEmail {
		return EmailChangeRequestResult{}, ErrSameEmail
	}

	// 4. Point-in-time availability check (uniqueness re-enforced inside ConfirmEmailChangeTx).
	available, err := s.store.CheckEmailAvailableForChange(ctx, normalised, in.UserID)
	if err != nil {
		return EmailChangeRequestResult{}, fmt.Errorf("email.RequestEmailChange: check availability: %w", err)
	}
	if !available {
		return EmailChangeRequestResult{}, ErrEmailTaken
	}

	// 5. Cooldown check: reject if a verify token was issued less than 2 minutes ago.
	createdAt, err := s.store.GetLatestEmailChangeVerifyTokenCreatedAt(ctx, in.UserID)
	if err != nil && !errors.Is(err, authshared.ErrTokenNotFound) {
		return EmailChangeRequestResult{}, fmt.Errorf("email.RequestEmailChange: cooldown check: %w", err)
	}
	if err == nil && time.Since(createdAt) < 2*time.Minute {
		return EmailChangeRequestResult{}, ErrCooldownActive
	}

	// 6. Generate OTP.
	raw, codeHash, err := authshared.GenerateCodeHash()
	if err != nil {
		return EmailChangeRequestResult{}, fmt.Errorf("email.RequestEmailChange: generate code hash: %w", err)
	}

	// 7. Persist the verify token (context.WithoutCancel ensures the write commits
	// even if the client disconnects mid-request — T-09).
	if err := s.store.RequestEmailChangeTx(context.WithoutCancel(ctx), RequestEmailChangeTxInput{
		UserID:       in.UserID,
		CurrentEmail: currentEmail,
		NewEmail:     normalised,
		CodeHash:     codeHash,
		TTLSeconds:   s.tokenTTL.Seconds(),
		IPAddress:    in.IPAddress,
		UserAgent:    in.UserAgent,
	}); err != nil {
		if errors.Is(err, ErrCooldownActive) {
			return EmailChangeRequestResult{}, ErrCooldownActive
		}
		return EmailChangeRequestResult{}, fmt.Errorf("email.RequestEmailChange: request tx: %w", err)
	}

	// 8. Return plaintext OTP for the handler to enqueue.
	return EmailChangeRequestResult{CurrentEmail: currentEmail, RawCode: raw}, nil
}

// VerifyCurrentEmail implements step 2 of the email-change flow.
// It validates the OTP code, consumes the verify token, issues a grant token in
// the KV store, and returns the grant token plus the new-email OTP for delivery.
func (s *Service) VerifyCurrentEmail(ctx context.Context, in EmailChangeVerifyCurrentInput) (EmailChangeVerifyCurrentResult, error) {
	// 1. Validate code format.
	if err := ValidateOTPCode(in.Code); err != nil {
		return EmailChangeVerifyCurrentResult{}, err
	}

	// 2. Generate OTP for the new-email confirm token (created inside the TX).
	raw2, codeHash2, err := authshared.GenerateCodeHash()
	if err != nil {
		return EmailChangeVerifyCurrentResult{}, fmt.Errorf("email.VerifyCurrentEmail: generate confirm code hash: %w", err)
	}

	// 3. Build checkFn — runs inside the store TX under a row-level lock.
	// IMPORTANT: IncrementAttemptsTx must NOT be called from inside checkFn.
	// checkFn is invoked while the TX holds a SELECT FOR UPDATE lock on the token
	// row. IncrementAttemptsTx opens an independent TX (ADR-003) that issues an
	// UPDATE on the same row — it would wait forever for the lock that the outer
	// TX already holds, causing a deadlock. Instead we capture the token here and
	// call IncrementAttemptsTx after the TX has committed or rolled back (i.e.
	// after the lock is released).
	var capturedVerifyToken authshared.VerificationToken
	checkFn := func(token authshared.VerificationToken) error {
		capturedVerifyToken = token
		return authshared.CheckOTPToken(token, in.Code, time.Now())
	}

	// 4. Consume the verify token and create the confirm token in one TX.
	// context.WithoutCancel ensures the commit survives a client disconnect (T-21).
	result, err := s.store.VerifyCurrentEmailTx(context.WithoutCancel(ctx), VerifyCurrentEmailTxInput{
		UserID:           in.UserID,
		NewEmailCodeHash: codeHash2,
		TTLSeconds:       s.tokenTTL.Seconds(),
		IPAddress:        in.IPAddress,
		UserAgent:        in.UserAgent,
	}, checkFn)
	if err != nil {
		// The TX has now rolled back and released its row lock — safe to increment.
		if errors.Is(err, authshared.ErrInvalidCode) && capturedVerifyToken.Attempts < capturedVerifyToken.MaxAttempts {
			// ADR-003: counter commits independently; never block the response on this.
			if incErr := s.store.IncrementAttemptsTx(
				context.WithoutCancel(ctx),
				authshared.IncrementInput{
					TokenID:      capturedVerifyToken.ID,
					UserID:       capturedVerifyToken.UserID,
					Attempts:     capturedVerifyToken.Attempts,
					MaxAttempts:  capturedVerifyToken.MaxAttempts,
					IPAddress:    in.IPAddress,
					UserAgent:    in.UserAgent,
					AttemptEvent: audit.EventEmailChangeVerifyAttemptFailed,
				},
			); incErr != nil {
				slog.ErrorContext(ctx, "email.VerifyCurrentEmail: increment attempts", "error", incErr)
			}
		}
		// Pass through known sentinels; wrap everything else.
		if errors.Is(err, authshared.ErrTokenNotFound) ||
			errors.Is(err, authshared.ErrTokenExpired) ||
			errors.Is(err, authshared.ErrTooManyAttempts) ||
			errors.Is(err, authshared.ErrInvalidCode) ||
			errors.Is(err, authshared.ErrTokenAlreadyUsed) {
			return EmailChangeVerifyCurrentResult{}, err
		}
		return EmailChangeVerifyCurrentResult{}, fmt.Errorf("email.VerifyCurrentEmail: verify tx: %w", err)
	}

	// 5. Generate a random grant token (UUID string).
	grantToken := uuid.New().String()

	// 6. Store grant token → "userID:newEmail" with a 10-minute TTL.
	// Security: WithoutCancel so a client disconnect cannot abort the KV write
	// after the DB TX has already committed and consumed the verify token.
	// Without this, the verify token would be consumed with no grant token
	// issued, forcing the user to restart the entire email-change flow.
	if err := s.kv.Set(context.WithoutCancel(ctx), "echg:gt:"+grantToken,
		uuid.UUID(in.UserID).String()+":"+result.NewEmail,
		10*time.Minute,
	); err != nil {
		return EmailChangeVerifyCurrentResult{}, fmt.Errorf("email.VerifyCurrentEmail: set grant token: %w", err)
	}

	// 7. Return grant token and new-email OTP for the handler to deliver.
	return EmailChangeVerifyCurrentResult{
		GrantToken:      grantToken,
		ExpiresIn:       600,
		NewEmail:        result.NewEmail,
		NewEmailRawCode: raw2,
	}, nil
}

// ConfirmEmailChange implements step 3 of the email-change flow.
// It validates the grant token and OTP, swaps the user's email, revokes all
// sessions and blocklists the caller's access token.
func (s *Service) ConfirmEmailChange(ctx context.Context, in EmailChangeConfirmInput) (ConfirmEmailChangeResult, error) {
	// 1. Validate inputs.
	if err := ValidateGrantToken(in.GrantToken); err != nil {
		return ConfirmEmailChangeResult{}, err
	}
	if err := ValidateOTPCode(in.Code); err != nil {
		return ConfirmEmailChangeResult{}, err
	}

	// 2. Look up grant token from KV store.
	rawVal, err := s.kv.Get(ctx, "echg:gt:"+in.GrantToken)
	if err != nil {
		if errors.Is(err, kvstore.ErrNotFound) {
			return ConfirmEmailChangeResult{}, ErrGrantTokenInvalid
		}
		return ConfirmEmailChangeResult{}, ErrGrantTokenInvalid
	}

	// 3. Parse the stored value: "{userID}:{newEmail}".
	userIDStr, _, ok := strings.Cut(rawVal, ":")
	if !ok {
		return ConfirmEmailChangeResult{}, ErrGrantTokenInvalid
	}

	// 4. Verify the grant token belongs to this caller.
	parsedUID, err := uuid.Parse(userIDStr)
	if err != nil || [16]byte(parsedUID) != in.UserID {
		return ConfirmEmailChangeResult{}, ErrGrantTokenInvalid
	}

	// 5. Snapshot the current (old) email before the swap for the notification mailer.
	oldEmail, err := s.store.GetCurrentUserEmail(ctx, in.UserID)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return ConfirmEmailChangeResult{}, profileshared.ErrUserNotFound
		}
		return ConfirmEmailChangeResult{}, fmt.Errorf("email.ConfirmEmailChange: get old email: %w", err)
	}

	// 6. Build checkFn for the confirm token.
	// IMPORTANT: IncrementAttemptsTx must NOT be called from inside checkFn for
	// the same reason as in VerifyCurrentEmail: checkFn runs while the TX holds a
	// SELECT FOR UPDATE lock on the confirm token row. Calling IncrementAttemptsTx
	// from inside checkFn would deadlock. Capture the token here; increment after
	// the TX has committed or rolled back and released its lock.
	var capturedConfirmToken authshared.VerificationToken
	checkFn := func(token authshared.VerificationToken) error {
		capturedConfirmToken = token
		return authshared.CheckOTPToken(token, in.Code, time.Now())
	}

	// 7. Swap email, revoke sessions (context.WithoutCancel — T-38).
	if err := s.store.ConfirmEmailChangeTx(context.WithoutCancel(ctx), ConfirmEmailChangeTxInput{
		UserID:    in.UserID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
	}, checkFn); err != nil {
		// The TX has now rolled back and released its row lock — safe to increment.
		if errors.Is(err, authshared.ErrInvalidCode) && capturedConfirmToken.Attempts < capturedConfirmToken.MaxAttempts {
			if incErr := s.store.IncrementAttemptsTx(
				context.WithoutCancel(ctx),
				authshared.IncrementInput{
					TokenID:      capturedConfirmToken.ID,
					UserID:       capturedConfirmToken.UserID,
					Attempts:     capturedConfirmToken.Attempts,
					MaxAttempts:  capturedConfirmToken.MaxAttempts,
					IPAddress:    in.IPAddress,
					UserAgent:    in.UserAgent,
					AttemptEvent: audit.EventEmailChangeConfirmAttemptFailed,
				},
			); incErr != nil {
				slog.ErrorContext(ctx, "email.ConfirmEmailChange: increment attempts", "error", incErr)
			}
		}
		if errors.Is(err, ErrEmailTaken) ||
			errors.Is(err, authshared.ErrTokenAlreadyUsed) ||
			errors.Is(err, authshared.ErrTokenNotFound) ||
			errors.Is(err, authshared.ErrTokenExpired) ||
			errors.Is(err, authshared.ErrInvalidCode) ||
			errors.Is(err, authshared.ErrTooManyAttempts) {
			return ConfirmEmailChangeResult{}, err
		}
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return ConfirmEmailChangeResult{}, profileshared.ErrUserNotFound
		}
		return ConfirmEmailChangeResult{}, fmt.Errorf("email.ConfirmEmailChange: confirm tx: %w", err)
	}

	// 8. Best-effort: delete grant token (single-use; natural expiry is the fallback).
	if delErr := s.kv.Delete(context.WithoutCancel(ctx), "echg:gt:"+in.GrantToken); delErr != nil {
		slog.ErrorContext(ctx, "email.ConfirmEmailChange: delete grant token", "error", delErr)
	}

	// 9. Best-effort: blocklist the caller's access token.
	if s.blocklist != nil {
		if blErr := s.blocklist.BlockToken(
			context.WithoutCancel(ctx), in.AccessJTI, s.accessTokenTTL,
		); blErr != nil {
			slog.ErrorContext(ctx, "email.ConfirmEmailChange: blocklist access token", "error", blErr)
		}
	}

	return ConfirmEmailChangeResult{OldEmail: oldEmail}, nil
}
