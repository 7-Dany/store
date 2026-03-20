package session

import (
	"context"
	"errors"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
)

var log = telemetry.New("session")

// ── Storer interface ──────────────────────────────────────────────────────────

// Storer is the data-access contract that Service depends on.
// *Store satisfies this interface; tests may supply a fake implementation.
type Storer interface {
	// GetRefreshTokenByJTI fetches a refresh_tokens row by jti.
	// Returns authshared.ErrTokenNotFound when absent.
	GetRefreshTokenByJTI(ctx context.Context, jti [16]byte) (StoredRefreshToken, error)

	// RotateRefreshTokenTx atomically revokes the current token and issues its child.
	RotateRefreshTokenTx(ctx context.Context, in RotateTxInput) (RotatedSession, error)

	// RevokeFamilyTokensTx revokes all non-revoked tokens in the given family and
	// writes a token_family_revoked audit row. reason is stored verbatim in the DB.
	RevokeFamilyTokensTx(ctx context.Context, userID, familyID [16]byte, reason string) error

	// RevokeAllUserTokensTx revokes every active token, ends every session, and
	// writes an all_sessions_revoked audit row. reason is stored verbatim in the DB.
	RevokeAllUserTokensTx(ctx context.Context, userID [16]byte, reason, ipAddress, userAgent string) error

	// GetUserVerifiedAndLocked fetches whether the user is email-verified and
	// whether the account is locked. Used by RotateRefreshToken to guard against
	// issuing new tokens to locked or unverified accounts.
	// Returns authshared.ErrUserNotFound when the user row is absent.
	GetUserVerifiedAndLocked(ctx context.Context, userID [16]byte) (UserStatusResult, error)

	// WriteRefreshFailedAuditTx writes a single refresh_failed audit row.
	// No userID — the token was not found or expired so no trusted identity exists.
	WriteRefreshFailedAuditTx(ctx context.Context, ipAddress, userAgent string) error

	// LogoutTx revokes the given token, ends its session, and writes an audit row.
	LogoutTx(ctx context.Context, in LogoutTxInput) error
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service holds the business logic for the session feature.
// It has no knowledge of HTTP, pgtype, pgxpool, or JWT signing.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// ── Methods ───────────────────────────────────────────────────────────────────

// RotateRefreshToken validates the refresh token identified by jti in five steps:
//  1. Fetch the DB row; write refresh_failed audit on ErrTokenNotFound.
//  2. Reuse detection — if the row is already revoked, revoke the full token
//     family and return ErrTokenReuseDetected.
//  3. Belt-and-suspenders DB expiry check (JWT exp already verified by caller).
//  4. Verify account is active and not locked.
//  5. Atomically rotate: revoke old token, insert child, stamp last_login_at, audit.
//
// The handler is responsible for JWT parsing and JWT signing; the service works
// exclusively with raw [16]byte UUIDs and time.Time.
//
// Returns authshared.ErrInvalidToken when the token is absent, expired, or the
// user no longer exists. Returns authshared.ErrAccountLocked or
// authshared.ErrAccountInactive when the account has been suspended.
func (s *Service) RotateRefreshToken(ctx context.Context, jti [16]byte, ip, ua string) (RotatedSession, error) {
	log.Debug(ctx, "RotateRefreshToken: start", "jti", uuid.UUID(jti).String(), "ip", ip)

	// 1. Look up the DB row by jti. jti is already [16]byte — no uuid.Parse needed.
	stored, err := s.store.GetRefreshTokenByJTI(ctx, jti)
	if err != nil {
		if errors.Is(err, authshared.ErrTokenNotFound) {
			// Security: detach from the request context so a client disconnect cannot
			// abort the audit write and erase the evidence of an invalid token presentation.
			if auditErr := s.store.WriteRefreshFailedAuditTx(context.WithoutCancel(ctx), ip, ua); auditErr != nil {
				log.Warn(ctx, "RotateRefreshToken: write refresh_failed audit failed (not found)",
					"error", auditErr)
			}
			return RotatedSession{}, authshared.ErrInvalidToken
		}
		return RotatedSession{}, telemetry.Service("RotateRefreshToken.get_token", err)
	}

	// 2. Reuse detection: row exists but is already revoked.
	// Revoke the entire family to kill any sibling tokens an attacker may hold.
	if stored.IsRevoked {
		// Security: detach from the request context so a client disconnect cannot
		// abort the family revocation, which is the primary RFC 6819 security action.
		if revokeErr := s.store.RevokeFamilyTokensTx(
			context.WithoutCancel(ctx), stored.UserID, stored.FamilyID, "reuse_detected",
		); revokeErr != nil {
			log.Warn(ctx, "RotateRefreshToken: revoke family after reuse detection failed",
				"family_id", uuid.UUID(stored.FamilyID).String(),
				"error", revokeErr,
			)
		}
		return RotatedSession{}, authshared.ErrTokenReuseDetected
	}

	// 3. Belt-and-suspenders DB expiry check (JWT exp was already verified by
	// the handler; this catches clock-skew edge cases).
	if stored.ExpiresAt.Before(time.Now()) {
		// Security: same rationale as above — must not be abortable by a client disconnect.
		if auditErr := s.store.WriteRefreshFailedAuditTx(context.WithoutCancel(ctx), ip, ua); auditErr != nil {
			log.Warn(ctx, "RotateRefreshToken: write refresh_failed audit failed (expired)",
				"error", auditErr)
		}
		return RotatedSession{}, authshared.ErrInvalidToken
	}

	// 4. Verify the user account is still active and not locked.
	// An attacker who obtained a token before the account was locked must not be
	// able to obtain fresh access tokens.
	status, err := s.store.GetUserVerifiedAndLocked(ctx, stored.UserID)
	if err != nil {
		if errors.Is(err, authshared.ErrUserNotFound) {
			return RotatedSession{}, authshared.ErrInvalidToken
		}
		return RotatedSession{}, telemetry.Service("RotateRefreshToken.get_user_status", err)
	}
	if status.IsLocked {
		return RotatedSession{}, authshared.ErrAccountLocked
	}
	if !status.IsActive {
		return RotatedSession{}, authshared.ErrAccountInactive
	}

	log.Debug(ctx, "RotateRefreshToken: guards passed, rotating token",
		"user_id", uuid.UUID(stored.UserID).String(),
		"session_id", uuid.UUID(stored.SessionID).String(),
	)

	// 5. Rotate: revoke old token, insert child, stamp last_login_at, audit log.
	rotated, err := s.store.RotateRefreshTokenTx(ctx, RotateTxInput{
		CurrentJTI: stored.JTI,
		UserID:     stored.UserID,
		SessionID:  stored.SessionID,
		FamilyID:   stored.FamilyID,
		IPAddress:  ip,
		UserAgent:  ua,
	})
	if err != nil {
		if errors.Is(err, authshared.ErrTokenAlreadyConsumed) {
			return RotatedSession{}, authshared.ErrInvalidToken
		}
		return RotatedSession{}, telemetry.Service("RotateRefreshToken.rotate_tx", err)
	}

	log.Info(ctx, "RotateRefreshToken: success",
		"user_id", uuid.UUID(stored.UserID).String(),
		"session_id", uuid.UUID(stored.SessionID).String(),
	)

	return rotated, nil
}

// RevokeAllUserTokens atomically revokes every active refresh token, ends
// every open session, and writes an audit row for the given user.
// The reason "forced_logout" is hardcoded.
func (s *Service) RevokeAllUserTokens(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	log.Debug(ctx, "RevokeAllUserTokens: start", "user_id", uuid.UUID(userID).String(), "ip", ipAddress)
	// Security: detach from the request context so a client-timed disconnect cannot
	// abort the token revocation or session termination, leaving active tokens in
	// the DB with no audit evidence of the revocation attempt (ADR-004).
	if err := s.store.RevokeAllUserTokensTx(context.WithoutCancel(ctx), userID, "forced_logout", ipAddress, userAgent); err != nil {
		return telemetry.Service("RevokeAllUserTokens.revoke", err)
	}
	log.Info(ctx, "RevokeAllUserTokens: all sessions revoked", "user_id", uuid.UUID(userID).String())
	return nil
}

// Logout invalidates the caller's refresh token and ends the associated session.
//
// Idempotency: the underlying LogoutTx uses AND revoked_at IS NULL and
// AND ended_at IS NULL guards, so calling Logout twice is safe.
//
// The handler is responsible for JWT parsing; it passes the pre-extracted
// token identifiers via LogoutTxInput. Errors are logged but never returned
// so the handler always clears the cookie and writes 204.
func (s *Service) Logout(ctx context.Context, in LogoutTxInput) error {
	log.Debug(ctx, "Logout: start",
		"user_id", uuid.UUID(in.UserID).String(),
		"session_id", uuid.UUID(in.SessionID).String(),
	)
	// Security: detach from the request context so a client disconnect cannot
	// prevent the DB revocation from completing.
	if err := s.store.LogoutTx(context.WithoutCancel(ctx), in); err != nil {
		log.Warn(ctx, "Logout: logout tx failed", "error", err)
	}
	return nil
}
