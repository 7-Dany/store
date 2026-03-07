package me

import (
	"net/url"
	"strings"
	"unicode/utf8"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// ── Validation constants ───────────────────────────────────────────────────────

const (
	maxDisplayNameLen = 100
	maxAvatarURLBytes = 2048
)

// ── Profile-update validation ─────────────────────────────────────────────────

// validateAndNormaliseUpdateProfile validates and normalises req in-place.
//
// Rules:
//   - At least one of DisplayName or AvatarURL must be non-nil (ErrEmptyPatch).
//   - If DisplayName is non-nil: trimmed, must be non-empty (ErrDisplayNameEmpty),
//     ≤ 100 runes (ErrDisplayNameTooLong), no ASCII control chars 0x00–0x1F
//     (ErrDisplayNameInvalid).
//   - If AvatarURL is non-nil: must be non-empty (ErrAvatarURLInvalid), ≤ 2048
//     bytes (ErrAvatarURLTooLong), must be an absolute URL with http or https
//     scheme (ErrAvatarURLInvalid).
//
// Validation order: empty-patch check → display_name → avatar_url.
func validateAndNormaliseUpdateProfile(req *updateProfileRequest) error {
	if req.DisplayName == nil && req.AvatarURL == nil {
		return ErrEmptyPatch
	}

	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if trimmed == "" {
			return authshared.ErrDisplayNameEmpty
		}
		if utf8.RuneCountInString(trimmed) > maxDisplayNameLen {
			return authshared.ErrDisplayNameTooLong
		}
		// Reject any ASCII control character (0x00–0x1F), not just NUL.
		// These can corrupt display rendering, logs, and downstream text processors.
		if strings.IndexFunc(trimmed, func(r rune) bool { return r < 0x20 }) != -1 {
			return authshared.ErrDisplayNameInvalid
		}
		req.DisplayName = &trimmed
	}

	if req.AvatarURL != nil {
		if *req.AvatarURL == "" {
			return ErrAvatarURLInvalid
		}
		// Length check before URL parsing to avoid ReDoS and long-string parse
		// overhead (analogous to the email length-before-regex ordering).
		if len(*req.AvatarURL) > maxAvatarURLBytes {
			return ErrAvatarURLTooLong
		}
		u, err := url.ParseRequestURI(*req.AvatarURL)
		if err != nil || u.Host == "" {
			return ErrAvatarURLInvalid
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return ErrAvatarURLInvalid
		}
	}

	return nil
}
