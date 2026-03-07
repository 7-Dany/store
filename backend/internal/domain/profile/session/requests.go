package session

import "time"

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
