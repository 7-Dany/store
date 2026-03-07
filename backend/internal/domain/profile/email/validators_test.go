// Package email_test contains unit tests for the email-change validators.
package email_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/domain/profile/email"
)

// ── TestNormaliseAndValidateNewEmail ──────────────────────────────────────────

func TestNormaliseAndValidateNewEmail(t *testing.T) {
	t.Parallel()

	// 255-byte email that is structurally valid but exceeds the 254-byte limit.
	longLocal := strings.Repeat("a", 243)
	tooLongEmail := longLocal + "@example.com" // 243+12 = 255 bytes

	tests := []struct {
		name      string
		input     string
		wantEmail string
		wantErr   error
	}{
		{
			name:      "valid simple email — returned normalised",
			input:     "alice@example.com",
			wantEmail: "alice@example.com",
		},
		{
			name:      "leading and trailing whitespace is trimmed",
			input:     "  alice@example.com  ",
			wantEmail: "alice@example.com",
		},
		{
			name:      "uppercase letters are lowercased",
			input:     "Alice@EXAMPLE.COM",
			wantEmail: "alice@example.com",
		},
		{
			name:      "subdomain address is preserved",
			input:     "user@mail.example.co.uk",
			wantEmail: "user@mail.example.co.uk",
		},
		{
			name:    "empty string returns ErrInvalidEmailFormat",
			input:   "",
			wantErr: email.ErrInvalidEmailFormat,
		},
		{
			name:    "whitespace-only returns ErrInvalidEmailFormat",
			input:   "   ",
			wantErr: email.ErrInvalidEmailFormat,
		},
		{
			name:    "address exceeding 254 bytes returns ErrEmailTooLong",
			input:   tooLongEmail,
			wantErr: email.ErrEmailTooLong,
		},
		{
			name:    "missing at-sign returns ErrInvalidEmailFormat",
			input:   "notanemail",
			wantErr: email.ErrInvalidEmailFormat,
		},
		{
			name:    "display-name form (Name <addr>) returns ErrInvalidEmailFormat",
			input:   "Alice <alice@example.com>",
			wantErr: email.ErrInvalidEmailFormat,
		},
		{
			name:    "missing domain returns ErrInvalidEmailFormat",
			input:   "alice@",
			wantErr: email.ErrInvalidEmailFormat,
		},
		{
			name:    "domain label exceeding 63 octets returns ErrInvalidEmailFormat",
			input:   "u@" + strings.Repeat("a", 64) + ".com",
			wantErr: email.ErrInvalidEmailFormat,
		},
		{
			name:    "domain with consecutive dots returns ErrInvalidEmailFormat",
			input:   "u@example..com",
			wantErr: email.ErrInvalidEmailFormat,
		},
		{
			name:    "domain starting with hyphen returns ErrInvalidEmailFormat",
			input:   "u@-bad.com",
			wantErr: email.ErrInvalidEmailFormat,
		},
		{
			name:      "unicode domain is punycode-encoded by IDNA",
			input:     "user@münchen.de",
			wantEmail: "user@xn--mnchen-3ya.de",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := email.NormaliseAndValidateNewEmail(tc.input)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantEmail, got)
		})
	}
}

// ── TestValidateOTPCode ───────────────────────────────────────────────────────

func TestValidateOTPCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "six digits — valid", input: "123456"},
		{name: "all zeros — valid", input: "000000"},
		{name: "empty string — invalid", input: "", wantErr: email.ErrInvalidCodeFormat},
		{name: "five digits — too short", input: "12345", wantErr: email.ErrInvalidCodeFormat},
		{name: "seven digits — too long", input: "1234567", wantErr: email.ErrInvalidCodeFormat},
		{name: "alpha character — invalid", input: "12345a", wantErr: email.ErrInvalidCodeFormat},
		{name: "leading space — invalid", input: " 23456", wantErr: email.ErrInvalidCodeFormat},
		{name: "internal space — invalid", input: "123 56", wantErr: email.ErrInvalidCodeFormat},
		{name: "full-width unicode digit — invalid", input: "１23456", wantErr: email.ErrInvalidCodeFormat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := email.ValidateOTPCode(tc.input)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ── TestValidateGrantToken ────────────────────────────────────────────────────

func TestValidateGrantToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "non-empty token — valid", input: "some-token-value"},
		{name: "UUID-shaped token — valid", input: "550e8400-e29b-41d4-a716-446655440000"},
		{name: "single character — valid", input: "x"},
		{name: "empty string — invalid", input: "", wantErr: email.ErrGrantTokenEmpty},
		{name: "whitespace-only — invalid", input: "   ", wantErr: email.ErrGrantTokenEmpty},
		{name: "tab-only — invalid", input: "\t", wantErr: email.ErrGrantTokenEmpty},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := email.ValidateGrantToken(tc.input)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
