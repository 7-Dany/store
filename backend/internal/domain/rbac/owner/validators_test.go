package owner_test

import (
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/rbac/owner"
	"github.com/stretchr/testify/require"
)

// ── validateAssignOwnerRequest ────────────────────────────────────────────────

func TestValidateAssignOwnerRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		secret  string
		wantErr error
	}{
		{
			name:    "empty secret",
			secret:  "",
			wantErr: owner.ErrAssignSecretEmpty,
		},
		{
			name:    "whitespace only",
			secret:  "   ",
			wantErr: owner.ErrAssignSecretEmpty,
		},
		{
			name:    "tab only",
			secret:  "\t",
			wantErr: owner.ErrAssignSecretEmpty,
		},
		{
			name:    "valid secret",
			secret:  "my-bootstrap-secret",
			wantErr: nil,
		},
		{
			name:    "valid secret with spaces inside",
			secret:  "secret with spaces",
			wantErr: nil,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := owner.ValidateAssignOwnerRequest(tc.secret)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// ── validateInitiateRequest ───────────────────────────────────────────────────

func TestValidateInitiateRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		targetUserID string
		wantErr      error
	}{
		{
			name:         "empty target_user_id",
			targetUserID: "",
			wantErr:      owner.ErrTargetUserIDRequired,
		},
		{
			name:         "whitespace only",
			targetUserID: "   ",
			wantErr:      owner.ErrTargetUserIDRequired,
		},
		{
			name:         "not a uuid",
			targetUserID: "not-a-uuid",
			wantErr:      owner.ErrTargetUserIDInvalid,
		},
		{
			name:         "partial uuid",
			targetUserID: "aaaaaaaa-bbbb-cccc",
			wantErr:      owner.ErrTargetUserIDInvalid,
		},
		{
			name:         "valid uuid v4",
			targetUserID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			wantErr:      nil,
		},
		{
			name:         "valid uuid without hyphens",
			targetUserID: "aaaaaaaabbbbccccddddeeeeeeeeeeee",
			wantErr:      nil,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := owner.ValidateInitiateRequest(tc.targetUserID)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// ── validateAcceptRequest ─────────────────────────────────────────────────────

func TestValidateAcceptRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rawToken string
		wantErr  error
	}{
		{
			name:     "empty token",
			rawToken: "",
			wantErr:  owner.ErrTokenRequired,
		},
		{
			name:     "whitespace only",
			rawToken: "   ",
			wantErr:  owner.ErrTokenRequired,
		},
		{
			name:     "tab only",
			rawToken: "\t",
			wantErr:  owner.ErrTokenRequired,
		},
		{
			name:     "valid token",
			rawToken: "someRawBase64UrlEncodedToken",
			wantErr:  nil,
		},
		{
			name:     "valid token with special chars",
			rawToken: "abc-def_xyz",
			wantErr:  nil,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := owner.ValidateAcceptRequest(tc.rawToken)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}
