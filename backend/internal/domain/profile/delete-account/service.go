package deleteaccount

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
)

// Storer is the subset of persistence methods the Service requires.
// *Store satisfies this interface; tests supply DeleteAccountFakeStorer.
type Storer interface {
	// GetUserForDeletion returns the minimal user row needed to gate the deletion
	// request. Maps no-rows to profileshared.ErrUserNotFound.
	GetUserForDeletion(ctx context.Context, userID [16]byte) (DeletionUser, error)

	// GetUserAuthMethods returns whether the user has a password and how many
	// OAuth identities are linked. Used to dispatch the correct confirmation path (D-11).
	GetUserAuthMethods(ctx context.Context, userID [16]byte) (UserAuthMethods, error)

	// GetIdentityByUserAndProvider returns the provider_uid for the user's identity
	// with the given provider. Maps no-rows to authshared.ErrUserNotFound.
	GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte, provider string) (string, error)

	// ScheduleDeletionTx stamps deleted_at = NOW(), writes the audit row, and
	// returns DeletionScheduled{ScheduledDeletionAt: deleted_at + 30 days}.
	// Maps no-rows from ScheduleUserDeletion to profileshared.ErrUserNotFound.
	// in.Provider is used for the audit row (AuthProviderEmail or AuthProviderTelegram).
	ScheduleDeletionTx(ctx context.Context, in ScheduleDeletionInput) (DeletionScheduled, error)

	// SendDeletionOTPTx invalidates existing deletion tokens, generates a new OTP,
	// persists the token, writes the audit row, and returns the raw OTP code.
	// The service must dispatch the code by email; the store never stores it plaintext.
	SendDeletionOTPTx(ctx context.Context, in SendDeletionOTPInput) (SendDeletionOTPResult, error)

	// GetAccountDeletionToken fetches the active account_deletion token for the user.
	// The underlying query uses FOR UPDATE; when called outside a transaction the lock
	// is released immediately at statement end. ConfirmOTPDeletionTx re-acquires
	// the lock inside its own transaction for TOCTOU protection — calling this outside
	// first is intentional (read-then-lock pattern).
	// Maps no-rows to authshared.ErrTokenNotFound.
	GetAccountDeletionToken(ctx context.Context, userID [16]byte) (authshared.VerificationToken, error)

	// IncrementAttemptsTx records a failed OTP attempt and locks the account when
	// max_attempts is reached. Promoted from the embedded authshared.BaseStore.
	IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error

	// ConfirmOTPDeletionTx re-locks the active token FOR UPDATE, consumes it,
	// stamps deleted_at, writes the audit row, and returns DeletionScheduled.
	// tokenID is the token ID already validated by the service layer.
	ConfirmOTPDeletionTx(ctx context.Context, in ScheduleDeletionInput, tokenID [16]byte) (DeletionScheduled, error)

	// CancelDeletionTx clears deleted_at and writes the audit row.
	// Returns ErrNotPendingDeletion when the user has no pending deletion.
	CancelDeletionTx(ctx context.Context, in CancelDeletionInput) error
}

// Service is the business-logic layer for DELETE /me and POST /me/cancel-deletion.
type Service struct {
	store            Storer
	otpTTL           time.Duration // from deps.OTPTokenTTL
	telegramBotToken string        // from deps config; used for HMAC verification
}

// NewService constructs a Service backed by s.
// otpTTL controls the lifetime of account_deletion OTP tokens (same as all OTP flows).
// telegramBotToken is the Telegram Bot API token used to verify HMAC re-auth payloads.
func NewService(s Storer, otpTTL time.Duration, telegramBotToken string) *Service {
	return &Service{store: s, otpTTL: otpTTL, telegramBotToken: telegramBotToken}
}

// ── Service methods ───────────────────────────────────────────────────────────

// ResolveUserForDeletion fetches the user and their auth methods for empty-body
// dispatch. The handler calls this to route to Path B (email-OTP) or Path C (Telegram).
//
// Guard ordering:
//  1. ParseUserID
//  2. GetUserForDeletion → ErrUserNotFound wraps as 500
//  3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
//  4. GetUserAuthMethods → wrap errors
//  5. return (user, authMethods, nil)
func (s *Service) ResolveUserForDeletion(ctx context.Context, userID string) (DeletionUser, UserAuthMethods, error) {
	// 1. Parse user ID.
	uid, err := authshared.ParseUserID("deleteaccount.ResolveUserForDeletion", userID)
	if err != nil {
		return DeletionUser{}, UserAuthMethods{}, err
	}

	// 2. Fetch user.
	user, err := s.store.GetUserForDeletion(ctx, uid)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return DeletionUser{}, UserAuthMethods{}, fmt.Errorf("deleteaccount.ResolveUserForDeletion: user not found for authenticated JWT: %w", err)
		}
		return DeletionUser{}, UserAuthMethods{}, fmt.Errorf("deleteaccount.ResolveUserForDeletion: get user: %w", err)
	}

	// 3. Guard: deletion already pending.
	if user.DeletedAt != nil {
		return DeletionUser{}, UserAuthMethods{}, ErrAlreadyPendingDeletion
	}

	// 4. Fetch auth methods for dispatch.
	authMethods, err := s.store.GetUserAuthMethods(ctx, uid)
	if err != nil {
		return DeletionUser{}, UserAuthMethods{}, fmt.Errorf("deleteaccount.ResolveUserForDeletion: get auth methods: %w", err)
	}

	// 5. Return.
	return user, authMethods, nil
}

// DeleteWithPassword completes soft-deletion for a password-authenticated user (Path A).
//
// Guard ordering:
//  1. ParseUserID
//  2. GetUserForDeletion → ErrUserNotFound wraps as 500; other errors wrap
//  3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
//  4. user.PasswordHash == nil → ErrInvalidCredentials (not a password account)
//  5. CheckPassword(*user.PasswordHash, in.Password) → ErrInvalidCredentials on mismatch
//  6. ScheduleDeletionTx → ErrUserNotFound wraps as 500; other errors wrap
//  7. return result, nil
func (s *Service) DeleteWithPassword(ctx context.Context, in DeleteWithPasswordInput) (DeletionScheduled, error) {
	// 1. Parse user ID.
	uid, err := authshared.ParseUserID("deleteaccount.DeleteWithPassword", in.UserID)
	if err != nil {
		return DeletionScheduled{}, err
	}

	// 2. Fetch user.
	user, err := s.store.GetUserForDeletion(ctx, uid)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return DeletionScheduled{}, fmt.Errorf("deleteaccount.DeleteWithPassword: user not found for authenticated JWT: %w", err)
		}
		return DeletionScheduled{}, fmt.Errorf("deleteaccount.DeleteWithPassword: get user: %w", err)
	}

	// 3. Guard: deletion already pending.
	if user.DeletedAt != nil {
		return DeletionScheduled{}, ErrAlreadyPendingDeletion
	}

	// 4. Guard: must be a password account.
	if user.PasswordHash == nil {
		return DeletionScheduled{}, authshared.ErrInvalidCredentials
	}

	// 5. Verify password.
	if err := authshared.CheckPassword(*user.PasswordHash, in.Password); err != nil {
		return DeletionScheduled{}, authshared.ErrInvalidCredentials
	}

	// 6. Soft-delete + audit.
	result, err := s.store.ScheduleDeletionTx(ctx, ScheduleDeletionInput{
		UserID:    in.UserID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
		Provider:  db.AuthProviderEmail,
	})
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return DeletionScheduled{}, fmt.Errorf("deleteaccount.DeleteWithPassword: schedule deletion: user not found: %w", err)
		}
		return DeletionScheduled{}, fmt.Errorf("deleteaccount.DeleteWithPassword: schedule deletion: %w", err)
	}

	return result, nil
}

// InitiateEmailDeletion triggers the email-OTP flow (Path B step 1).
//
// Guard ordering:
//  1. ParseUserID
//  2. GetUserForDeletion → ErrUserNotFound wraps as 500; other errors wrap
//  3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
//  4. user.Email == nil → wrap as 500 (handler routes here only when email is non-nil)
//  5. SendDeletionOTPTx → wrap errors
//  6. return authshared.NewOTPIssuanceResult(uid, *user.Email, result.RawCode), nil
func (s *Service) InitiateEmailDeletion(ctx context.Context, in ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
	// 1. Parse user ID.
	uid, err := authshared.ParseUserID("deleteaccount.InitiateEmailDeletion", in.UserID)
	if err != nil {
		return authshared.OTPIssuanceResult{}, err
	}

	// 2. Fetch user.
	user, err := s.store.GetUserForDeletion(ctx, uid)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return authshared.OTPIssuanceResult{}, fmt.Errorf("deleteaccount.InitiateEmailDeletion: user not found for authenticated JWT: %w", err)
		}
		return authshared.OTPIssuanceResult{}, fmt.Errorf("deleteaccount.InitiateEmailDeletion: get user: %w", err)
	}

	// 3. Guard: deletion already pending.
	if user.DeletedAt != nil {
		return authshared.OTPIssuanceResult{}, ErrAlreadyPendingDeletion
	}

	// 4. Guard: must have email (handler invariant).
	if user.Email == nil {
		return authshared.OTPIssuanceResult{}, fmt.Errorf("deleteaccount.InitiateEmailDeletion: user has no email (internal routing error)")
	}

	// 5. Issue OTP and write audit row.
	result, err := s.store.SendDeletionOTPTx(ctx, SendDeletionOTPInput{
		UserID:     in.UserID,
		Email:      *user.Email,
		TTLSeconds: s.otpTTL.Seconds(),
		IPAddress:  in.IPAddress,
		UserAgent:  in.UserAgent,
	})
	if err != nil {
		return authshared.OTPIssuanceResult{}, fmt.Errorf("deleteaccount.InitiateEmailDeletion: send OTP: %w", err)
	}

	// 6. Return issuance result for handler to enqueue email.
	return authshared.NewOTPIssuanceResult(uid, *user.Email, result.RawCode), nil
}

// ConfirmEmailDeletion validates the OTP and completes soft-deletion (Path B step 2).
//
// Guard ordering:
//  1. ParseUserID
//  2. GetUserForDeletion → ErrUserNotFound wraps as 500; other errors wrap
//  3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
//  4. ValidateOTPCode(in.Code) → return error as-is (already an authshared sentinel)
//  5. GetAccountDeletionToken → ErrTokenNotFound return as-is; other errors wrap
//  6. CheckOTPToken(token, in.Code, time.Now())
//     - ErrInvalidCode: IncrementAttemptsTx then return ErrInvalidCode
//     - ErrTokenExpired: return as ErrTokenNotFound (authshared convention)
//     - ErrTooManyAttempts / other sentinels: return as-is
//  7. ConfirmOTPDeletionTx → ErrTokenAlreadyUsed return as-is; ErrUserNotFound wrap as 500; other errors wrap
//  8. return result, nil
func (s *Service) ConfirmEmailDeletion(ctx context.Context, in ConfirmOTPDeletionInput) (DeletionScheduled, error) {
	// 1. Parse user ID.
	uid, err := authshared.ParseUserID("deleteaccount.ConfirmEmailDeletion", in.UserID)
	if err != nil {
		return DeletionScheduled{}, err
	}

	// 2. Fetch user.
	user, err := s.store.GetUserForDeletion(ctx, uid)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmEmailDeletion: user not found for authenticated JWT: %w", err)
		}
		return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmEmailDeletion: get user: %w", err)
	}

	// 3. Guard: deletion already pending.
	if user.DeletedAt != nil {
		return DeletionScheduled{}, ErrAlreadyPendingDeletion
	}

	// 4. Validate OTP code format.
	if err := authshared.ValidateOTPCode(in.Code); err != nil {
		return DeletionScheduled{}, err
	}

	// 5. Fetch the active deletion token (FOR UPDATE).
	token, err := s.store.GetAccountDeletionToken(ctx, uid)
	if err != nil {
		if errors.Is(err, authshared.ErrTokenNotFound) {
			return DeletionScheduled{}, authshared.ErrTokenNotFound
		}
		return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmEmailDeletion: get token: %w", err)
	}

	// 6. Validate the OTP (expiry, attempt budget, hash).
	if checkErr := authshared.CheckOTPToken(token, in.Code, time.Now()); checkErr != nil {
		if errors.Is(checkErr, authshared.ErrInvalidCode) {
			// Security: detach from the request context so a client disconnect cannot
			// abort the counter increment and grant unlimited OTP retries (ADR-004).
			if incErr := s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), authshared.IncrementInput{
				TokenID:      token.ID,
				UserID:       uid,
				Attempts:     token.Attempts,
				MaxAttempts:  token.MaxAttempts,
				AttemptEvent: audit.EventAccountDeletionOTPFailed,
				IPAddress:    in.IPAddress,
				UserAgent:    in.UserAgent,
			}); incErr != nil {
				return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmEmailDeletion: increment attempts: %w", incErr)
			}
			return DeletionScheduled{}, authshared.ErrInvalidCode
		}
		// ErrTokenExpired maps to ErrTokenNotFound per authshared convention.
		if errors.Is(checkErr, authshared.ErrTokenExpired) {
			return DeletionScheduled{}, authshared.ErrTokenNotFound
		}
		return DeletionScheduled{}, checkErr
	}

	// 7. Consume token and stamp deleted_at in one transaction.
	result, err := s.store.ConfirmOTPDeletionTx(ctx, ScheduleDeletionInput{
		UserID:    in.UserID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
	}, token.ID)
	if err != nil {
		if errors.Is(err, authshared.ErrTokenAlreadyUsed) {
			return DeletionScheduled{}, authshared.ErrTokenAlreadyUsed
		}
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmEmailDeletion: confirm deletion: user not found: %w", err)
		}
		return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmEmailDeletion: confirm deletion: %w", err)
	}

	return result, nil
}

// ConfirmTelegramDeletion validates HMAC re-auth and completes soft-deletion (Path C step 2).
//
// Guard ordering:
//  1. ParseUserID
//  2. GetUserForDeletion → ErrUserNotFound wraps as 500; other errors wrap
//  3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
//  4. validateTelegramAuthPayload — id, auth_date, hash presence
//  5. Replay check: in.TelegramAuth.AuthDate > time.Now().Unix()-86400
//  6. verifyTelegramHMAC → false: ErrInvalidTelegramAuth
//  7. GetIdentityByUserAndProvider(ctx, uid, "telegram")
//     - ErrUserNotFound → ErrInvalidCredentials
//     - other errors wrap
//  8. providerUID != strconv.FormatInt(in.TelegramAuth.ID, 10) → ErrTelegramIdentityMismatch
//  9. ScheduleDeletionTx → ErrUserNotFound wraps as 500; other errors wrap
//
// 10. return result, nil
func (s *Service) ConfirmTelegramDeletion(ctx context.Context, in ConfirmTelegramDeletionInput) (DeletionScheduled, error) {
	// 1. Parse user ID.
	uid, err := authshared.ParseUserID("deleteaccount.ConfirmTelegramDeletion", in.UserID)
	if err != nil {
		return DeletionScheduled{}, err
	}

	// 2. Fetch user.
	user, err := s.store.GetUserForDeletion(ctx, uid)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmTelegramDeletion: user not found for authenticated JWT: %w", err)
		}
		return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmTelegramDeletion: get user: %w", err)
	}

	// 3. Guard: deletion already pending.
	if user.DeletedAt != nil {
		return DeletionScheduled{}, ErrAlreadyPendingDeletion
	}

	// 4. Defense-in-depth: re-validate payload presence in the service layer in case
	// the service is called directly (e.g. a future background job) without going
	// through the handler's Path C-2 guard.
	if err := validateTelegramAuthPayload(&in.TelegramAuth); err != nil {
		return DeletionScheduled{}, err
	}

	// Security: reject auth_date older than 24 hours to prevent replay attacks.
	if in.TelegramAuth.AuthDate <= time.Now().Unix()-86400 {
		return DeletionScheduled{}, ErrInvalidTelegramAuth
	}

	// Security: HMAC verification proves the payload was signed by Telegram's servers.
	if !verifyTelegramHMAC(s.telegramBotToken, in.TelegramAuth) {
		return DeletionScheduled{}, ErrInvalidTelegramAuth
	}

	// 7. Confirm the Telegram identity is linked to this user.
	providerUID, err := s.store.GetIdentityByUserAndProvider(ctx, uid, "telegram")
	if err != nil {
		if errors.Is(err, authshared.ErrUserNotFound) {
			// No telegram identity linked — treat as unauthorised.
			return DeletionScheduled{}, authshared.ErrInvalidCredentials
		}
		return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmTelegramDeletion: get identity: %w", err)
	}

	// 8. Ownership check: provider_uid must match the Telegram payload id.
	expected := strconv.FormatInt(in.TelegramAuth.ID, 10)
	if providerUID != expected {
		return DeletionScheduled{}, ErrTelegramIdentityMismatch
	}

	// 9. Soft-delete + audit.
	result, err := s.store.ScheduleDeletionTx(ctx, ScheduleDeletionInput{
		UserID:    in.UserID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
		Provider:  db.AuthProviderTelegram,
	})
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmTelegramDeletion: schedule deletion: user not found: %w", err)
		}
		return DeletionScheduled{}, fmt.Errorf("deleteaccount.ConfirmTelegramDeletion: schedule deletion: %w", err)
	}

	return result, nil
}

// CancelDeletion clears deleted_at for a pending-deletion account.
//
// Guard ordering:
//  1. ParseUserID
//  2. CancelDeletionTx → ErrNotPendingDeletion return as-is; other errors wrap
//  3. return nil
//
// Note: no GetUserForDeletion call — CancelDeletionTx maps 0 rows to ErrNotPendingDeletion.
func (s *Service) CancelDeletion(ctx context.Context, in CancelDeletionInput) error {
	// 1. Parse user ID.
	_, err := authshared.ParseUserID("deleteaccount.CancelDeletion", in.UserID)
	if err != nil {
		return err
	}

	// 2. Cancel + audit in one transaction.
	if err := s.store.CancelDeletionTx(ctx, in); err != nil {
		if errors.Is(err, ErrNotPendingDeletion) {
			return ErrNotPendingDeletion
		}
		return fmt.Errorf("deleteaccount.CancelDeletion: %w", err)
	}

	return nil
}

// GetDeletionMethod returns the confirmation method the client should use for this
// user, mirroring the empty-body dispatch logic in handler.Delete exactly.
//
// Priority (same as handler):
//  1. HasPassword → "password"
//  2. user.Email != nil → "email_otp"
//  3. otherwise → "telegram"
//
// Guard ordering:
//  1. ParseUserID
//  2. GetUserForDeletion → ErrUserNotFound wraps as 500; other errors wrap
//  3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
//  4. GetUserAuthMethods → wrap errors
//  5. derive and return DeletionMethodResult
func (s *Service) GetDeletionMethod(ctx context.Context, userID string) (DeletionMethodResult, error) {
	// 1. Parse user ID.
	uid, err := authshared.ParseUserID("deleteaccount.GetDeletionMethod", userID)
	if err != nil {
		return DeletionMethodResult{}, err
	}

	// 2. Fetch user.
	user, err := s.store.GetUserForDeletion(ctx, uid)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return DeletionMethodResult{}, fmt.Errorf("deleteaccount.GetDeletionMethod: user not found for authenticated JWT: %s", err)
		}
		return DeletionMethodResult{}, fmt.Errorf("deleteaccount.GetDeletionMethod: get user: %w", err)
	}

	// 3. Guard: deletion already pending.
	if user.DeletedAt != nil {
		return DeletionMethodResult{}, ErrAlreadyPendingDeletion
	}

	// 4. Fetch auth methods.
	authMethods, err := s.store.GetUserAuthMethods(ctx, uid)
	if err != nil {
		return DeletionMethodResult{}, fmt.Errorf("deleteaccount.GetDeletionMethod: get auth methods: %w", err)
	}

	// 5. Derive method using the same priority as empty-body dispatch.
	switch {
	case authMethods.HasPassword:
		return DeletionMethodResult{Method: "password"}, nil
	case user.Email != nil:
		return DeletionMethodResult{Method: "email_otp"}, nil
	default:
		return DeletionMethodResult{Method: "telegram"}, nil
	}
}


