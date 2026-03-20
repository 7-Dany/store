// Package setpassword provides the HTTP handler, service, and store for
// POST /api/v1/auth/set-password — adding a password to an OAuth-only account.
package setpassword

import (
	"context"
	"errors"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// Storer is the subset of the store that the service requires.
// *Store satisfies this interface; tests may supply a fake implementation.
type Storer interface {
	// GetUserForSetPassword returns whether the authenticated user has no
	// password hash, meaning the account was created exclusively via OAuth.
	// Returns profileshared.ErrUserNotFound when the user row cannot be found.
	GetUserForSetPassword(ctx context.Context, userID [16]byte) (SetPasswordUser, error)

	// SetPasswordHashTx sets the password hash and writes the audit row in a
	// single transaction. Returns ErrPasswordAlreadySet when the
	// WHERE password_hash IS NULL guard catches a concurrent write.
	SetPasswordHashTx(ctx context.Context, in SetPasswordInput, newHash string) error
}

var log = telemetry.New("set-password")

// Service is the business-logic layer for POST /set-password.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// SetPassword adds a password to an OAuth-only account that currently has no
// password_hash. It is not valid for accounts that already have a password —
// those must use POST /change-password instead.
//
// Guard ordering (Stage 0 §5):
//  1. Parse user ID — reject malformed UUIDs before any store call.
//  2. Fetch user row — verify the account exists and has no password.
//  3. HasNoPassword guard — return ErrPasswordAlreadySet if account has one.
//  4. Validate password strength.
//  5. Compute bcrypt hash outside the transaction.
//  6. SetPasswordHashTx — set hash + audit; WHERE IS NULL is the DB concurrency guard.
func (s *Service) SetPassword(ctx context.Context, in SetPasswordInput) error {
	// 1. Parse user ID — validates it is a well-formed UUID before any store call.
	uid, err := authshared.ParseUserID("setpassword.SetPassword", in.UserID)
	if err != nil {
		return err
	}

	// 2. Fetch the user record to verify the account currently has no password.
	user, err := s.store.GetUserForSetPassword(ctx, uid)
	if err != nil {
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return profileshared.ErrUserNotFound
		}
		return telemetry.Service("setpassword.SetPassword: get user", err)
	}

	// 3. Guard: account must not already have a password.
	if !user.HasNoPassword {
		return ErrPasswordAlreadySet
	}

	// 4. Validate password strength before any slow cryptographic operation.
	if err := authshared.ValidatePassword(in.NewPassword); err != nil {
		return err
	}

	// 5. Pre-compute bcrypt hash outside the transaction so the slow ~300 ms
	// operation does not hold a DB lock (same pattern as change-password).
	//
	// Unreachable: ValidatePassword (step 4) rejects empty and too-short
	// passwords; a validated password cannot cause HashPassword to fail on any
	// supported platform (crypto/rand failure requires an OS-level fault).
	newHash, err := authshared.HashPassword(in.NewPassword)
	if err != nil {
		return telemetry.Service("SetPassword.hash_password", err)
	}

	// 6. Set hash + audit in one transaction.
	// WHERE password_hash IS NULL in the SQL is the DB-level concurrency guard
	// against a race between step 2 and this write.
	if err := s.store.SetPasswordHashTx(ctx, in, newHash); err != nil {
		if errors.Is(err, ErrPasswordAlreadySet) {
			return ErrPasswordAlreadySet
		}
		if errors.Is(err, profileshared.ErrUserNotFound) {
			return profileshared.ErrUserNotFound
		}
		return telemetry.Service("setpassword.SetPassword: set password hash tx", err)
	}

	return nil
}
