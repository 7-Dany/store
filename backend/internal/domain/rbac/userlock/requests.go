package userlock

import "time"

// ── HTTP request ──────────────────────────────────────────────────────────────

type lockUserRequest struct {
	Reason string `json:"reason"`
}

// ── HTTP response ─────────────────────────────────────────────────────────────

type userLockStatusResponse struct {
	UserID           string     `json:"user_id"`
	AdminLocked      bool       `json:"admin_locked"`
	LockedBy         *string    `json:"locked_by,omitempty"`
	LockedReason     *string    `json:"locked_reason,omitempty"`
	LockedAt         *time.Time `json:"locked_at,omitempty"`
	IsLocked         bool       `json:"is_locked"`
	LoginLockedUntil *time.Time `json:"login_locked_until,omitempty"`
}

// ── Mapper ────────────────────────────────────────────────────────────────────

func toLockStatusResponse(s UserLockStatus) userLockStatusResponse {
	return userLockStatusResponse{
		UserID:           s.UserID,
		AdminLocked:      s.AdminLocked,
		LockedBy:         s.LockedBy,
		LockedReason:     s.LockedReason,
		LockedAt:         s.LockedAt,
		IsLocked:         s.IsLocked,
		LoginLockedUntil: s.LoginLockedUntil,
	}
}
