// Package session provides the HTTP handler, service, and store for
// authenticated user session management: listing and revoking sessions.
package session

import "time"

// ActiveSession is the store-layer representation of an open user session.
type ActiveSession struct {
	ID           [16]byte
	IPAddress    string
	UserAgent    string
	StartedAt    time.Time
	LastActiveAt time.Time
	// IsCurrent is NOT here — it is a handler-layer concern computed from the
	// JWT session claim and set directly on sessionJSON in handler.go.
}
