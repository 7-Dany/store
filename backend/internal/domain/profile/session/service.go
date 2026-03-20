package session

import (
	"context"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
)

// Storer is the data-access contract that Service depends on.
// *Store satisfies this interface; tests may supply a fake implementation.
type Storer interface {
	GetActiveSessions(ctx context.Context, userID [16]byte) ([]ActiveSession, error)
	RevokeSessionTx(ctx context.Context, sessionID, ownerUserID [16]byte, ipAddress, userAgent string) error
}

var log = telemetry.New("session")

// Service holds pure business logic for the session sub-package.
// It has no knowledge of HTTP, pgtype, pgxpool, or JWT signing.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// GetActiveSessions returns all open sessions for the given user, newest first.
// Returns an empty slice (not an error) when no sessions exist.
// userID is the standard UUID string form.
func (s *Service) GetActiveSessions(ctx context.Context, userID string) ([]ActiveSession, error) {
	uid, err := authshared.ParseUserID("profile.GetActiveSessions", userID)
	if err != nil {
		return nil, err
	}
	sessions, err := s.store.GetActiveSessions(ctx, uid)
	if err != nil {
		return nil, telemetry.Service("GetActiveSessions.get_sessions", err)
	}
	return sessions, nil
}

// RevokeSession revokes a specific session, verifying it belongs to the caller.
// Returns authshared.ErrSessionNotFound if the session does not exist or belongs to a
// different user. Both userID and sessionID are standard UUID string form.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID, ipAddress, userAgent string) error {
	uid, err := authshared.ParseUserID("profile.RevokeSession", userID)
	if err != nil {
		return err
	}
	sid, err := uuid.Parse(sessionID)
	if err != nil {
		return telemetry.Service("RevokeSession.parse_session_id", err)
	}
	// Security: use WithoutCancel so a client disconnect cannot abort the session revocation commit.
	if err := s.store.RevokeSessionTx(context.WithoutCancel(ctx), [16]byte(sid), uid, ipAddress, userAgent); err != nil {
		return telemetry.Service("RevokeSession.revoke_session", err)
	}
	return nil
}
