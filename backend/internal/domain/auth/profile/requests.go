package profile

import "time"

// meResponse is the JSON response body for GET /me.
type meResponse struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	DisplayName   string     `json:"display_name"`
	AvatarURL     string     `json:"avatar_url,omitempty"`
	EmailVerified bool       `json:"email_verified"`
	IsActive      bool       `json:"is_active"`
	IsLocked      bool       `json:"is_locked"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// sessionJSON is the JSON representation of a single active session, used in
// the GET /sessions response body.
type sessionJSON struct {
	ID           string    `json:"id"`
	IPAddress    string    `json:"ip_address"`
	UserAgent    string    `json:"user_agent"`
	StartedAt    time.Time `json:"started_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	IsCurrent    bool      `json:"is_current"`
}

