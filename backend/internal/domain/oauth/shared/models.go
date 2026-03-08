// Package oauthshared holds primitives shared by all oauth feature sub-packages.
package oauthshared

import "time"

// LoggedInSession is the session metadata returned by a store Tx method after
// a successful OAuth login or registration. All UUIDs are raw [16]byte; the
// handler converts them to strings for JWT claims.
type LoggedInSession struct {
	UserID        [16]byte
	SessionID     [16]byte
	RefreshJTI    [16]byte
	FamilyID      [16]byte
	RefreshExpiry time.Time
}

// LinkedIdentity is a summary of one OAuth identity linked to a user account.
// Used by the future GET /profile/me/identities endpoint (§E-1).
type LinkedIdentity struct {
	Provider    string
	DisplayName string
	AvatarURL   string
}
