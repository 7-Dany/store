package config

import (
	"strings"
	"testing"
	"time"
)

// validBaseConfig returns a Config that passes all validation rules.
// Tests mutate specific fields to exercise a single validation branch.
func validBaseConfig() *Config {
	return &Config{
		AppEnv:              "development",
		Addr:                ":8080",
		AppName:             "TestStore",
		DatabaseURL:         "postgres://user:pass@localhost/testdb",
		SMTPHost:            "smtp.example.com",
		SMTPPort:            587,
		SMTPUsername:        "user@example.com",
		SMTPPassword:        "password",
		SMTPFrom:            "no-reply@example.com",
		OTPValidMinutes:     15,
		AllowedOrigins:      []string{"http://localhost:3000"},
		JWTAccessSecret:     "abcdef1234567890abcdef1234567890ab",                               // 34 unique chars
		JWTRefreshSecret:    "1234567890abcdef1234567890abcdef12",                               // distinct, unique chars
		TokenEncryptionKey:  "a1b2c3d4e5f67890abcdef1234567890a1b2c3d4e5f67890abcdef1234567890", // 64 hex chars
		BootstrapSecret:     "test-bootstrap-secret-value-here",
		MailWorkers:         4,
		MailDeliveryTimeout: 30 * time.Second,
		AccessTokenTTL:      15 * time.Minute,
		DBMaxConns:          20,
		DBMinConns:          2,
		DBMaxConnLifetime:   30 * time.Minute,
		DBMaxConnIdle:       5 * time.Minute,
		DBHealthCheck:       1 * time.Minute,
		GoogleClientID:     "fake-google-client-id.apps.googleusercontent.com",
		GoogleClientSecret: "fake-google-client-secret",
		GoogleRedirectURI:  "http://localhost:8080/api/v1/oauth/google/callback",
		OAuthSuccessURL:    "http://localhost:3000/dashboard",
		OAuthErrorURL:      "http://localhost:3000/login",
		TelegramBotToken:   "1234567890:ABCdefGHIjklMNOpqrSTUvwxYZ-fake-token",
	}
}

// ─────────────────────────────────────────────────────────────
// Required fields
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsMissingDatabaseURL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.DatabaseURL = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingTokenEncryptionKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.TokenEncryptionKey = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing TOKEN_ENCRYPTION_KEY, got nil")
	}
	if !strings.Contains(err.Error(), "TOKEN_ENCRYPTION_KEY") {
		t.Errorf("error should mention TOKEN_ENCRYPTION_KEY, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingGoogleClientID(t *testing.T) {
	cfg := validBaseConfig()
	cfg.GoogleClientID = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing GOOGLE_CLIENT_ID, got nil")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CLIENT_ID") {
		t.Errorf("error should mention GOOGLE_CLIENT_ID, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingGoogleClientSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.GoogleClientSecret = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing GOOGLE_CLIENT_SECRET, got nil")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CLIENT_SECRET") {
		t.Errorf("error should mention GOOGLE_CLIENT_SECRET, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingGoogleRedirectURI(t *testing.T) {
	cfg := validBaseConfig()
	cfg.GoogleRedirectURI = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing GOOGLE_REDIRECT_URI, got nil")
	}
	if !strings.Contains(err.Error(), "GOOGLE_REDIRECT_URI") {
		t.Errorf("error should mention GOOGLE_REDIRECT_URI, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingOAuthSuccessURL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.OAuthSuccessURL = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing OAUTH_SUCCESS_URL, got nil")
	}
	if !strings.Contains(err.Error(), "OAUTH_SUCCESS_URL") {
		t.Errorf("error should mention OAUTH_SUCCESS_URL, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingOAuthErrorURL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.OAuthErrorURL = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing OAUTH_ERROR_URL, got nil")
	}
	if !strings.Contains(err.Error(), "OAUTH_ERROR_URL") {
		t.Errorf("error should mention OAUTH_ERROR_URL, got: %v", err)
	}
}

func TestConfigValidate_ReportsAllMissingFieldsAtOnce(t *testing.T) {
	cfg := &Config{
		AppEnv:           "development",
		AllowedOrigins:   []string{"http://localhost:3000"},
		JWTAccessSecret:  "abcdef1234567890abcdef1234567890ab",
		JWTRefreshSecret: "1234567890abcdef1234567890abcdef12",
		MailWorkers:      4,
		DBMaxConns:       20,
		DBMinConns:       2,
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for multiple missing fields, got nil")
	}
	for _, field := range []string{
		"DATABASE_URL", "SMTP_HOST", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM",
		"TOKEN_ENCRYPTION_KEY", "BOOTSTRAP_SECRET",
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URI",
		"OAUTH_SUCCESS_URL", "OAUTH_ERROR_URL",
	} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error should mention %s, got: %v", field, err)
		}
	}
}

func TestConfigValidate_AcceptsValidConfig(t *testing.T) {
	cfg := validBaseConfig()
	if err := cfg.validate(); err != nil {
		t.Errorf("valid config should pass validation, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// Wildcard origin guard
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsWildcardOrigin(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AllowedOrigins = []string{"*"}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for wildcard ALLOWED_ORIGINS, got nil")
	}
	if !strings.Contains(err.Error(), "ALLOWED_ORIGINS") {
		t.Errorf("error should mention ALLOWED_ORIGINS, got: %v", err)
	}
}

func TestConfigValidate_RejectsWildcardAmongExplicitOrigins(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AllowedOrigins = []string{"http://localhost:3000", "*"}
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error when wildcard is mixed with explicit origins, got nil")
	}
}

func TestConfigValidate_AcceptsMultipleExplicitOrigins(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AllowedOrigins = []string{"http://localhost:3000", "https://app.example.com"}
	if err := cfg.validate(); err != nil {
		t.Errorf("multiple explicit origins should be accepted, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// Redis requirement by environment
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RequiresRedisURLInProduction(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppEnv = "production"
	cfg.RedisURL = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error when REDIS_URL is absent in production, got nil")
	}
	if !strings.Contains(err.Error(), "REDIS_URL") {
		t.Errorf("error should mention REDIS_URL, got: %v", err)
	}
}

func TestConfigValidate_RequiresRedisURLInStaging(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppEnv = "staging"
	cfg.RedisURL = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error when REDIS_URL is absent in staging, got nil")
	}
}

func TestConfigValidate_RedisURLOptionalInDevelopment(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppEnv = "development"
	cfg.RedisURL = ""
	if err := cfg.validate(); err != nil {
		t.Errorf("REDIS_URL should be optional in development, got: %v", err)
	}
}

func TestConfigValidate_AcceptsRedisURLInProduction(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppEnv = "production"
	cfg.RedisURL = "redis://:secret@localhost:6379/0"
	if err := cfg.validate(); err != nil {
		t.Errorf("valid production config with REDIS_URL should pass, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// JWT secret rules
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsShortJWTAccessSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.JWTAccessSecret = "tooshort"
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for short JWT_ACCESS_SECRET, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_ACCESS_SECRET") {
		t.Errorf("error should mention JWT_ACCESS_SECRET, got: %v", err)
	}
}

func TestConfigValidate_RejectsShortJWTRefreshSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.JWTRefreshSecret = "tooshort"
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for short JWT_REFRESH_SECRET, got nil")
	}
}

func TestConfigValidate_RejectsIdenticalJWTSecrets(t *testing.T) {
	cfg := validBaseConfig()
	cfg.JWTAccessSecret = "abcdef1234567890abcdef1234567890ab"
	cfg.JWTRefreshSecret = "abcdef1234567890abcdef1234567890ab"
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for identical JWT secrets, got nil")
	}
	if !strings.Contains(err.Error(), "distinct") {
		t.Errorf("error should mention 'distinct', got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// Contradictory HTTPS flags
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsContradictoryHTTPSFlags(t *testing.T) {
	cfg := validBaseConfig()
	cfg.HTTPSEnabled = true
	cfg.HTTPSDisabled = true
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error when HTTPS_ENABLED and HTTPS_DISABLED are both true, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS_ENABLED") || !strings.Contains(err.Error(), "HTTPS_DISABLED") {
		t.Errorf("error message should mention both flags, got: %v", err)
	}
}

func TestConfigValidate_AcceptsHTTPSEnabledAlone(t *testing.T) {
	cfg := validBaseConfig()
	cfg.HTTPSEnabled = true
	cfg.HTTPSDisabled = false
	if err := cfg.validate(); err != nil {
		t.Errorf("HTTPSEnabled=true, HTTPSDisabled=false should be valid, got: %v", err)
	}
}

func TestConfigValidate_AcceptsHTTPSDisabledAlone(t *testing.T) {
	cfg := validBaseConfig()
	cfg.HTTPSEnabled = false
	cfg.HTTPSDisabled = true
	if err := cfg.validate(); err != nil {
		t.Errorf("HTTPSEnabled=false, HTTPSDisabled=true should be valid, got: %v", err)
	}
}

func TestConfigValidate_AcceptsBothHTTPSFlagsFalse(t *testing.T) {
	cfg := validBaseConfig()
	cfg.HTTPSEnabled = false
	cfg.HTTPSDisabled = false
	if err := cfg.validate(); err != nil {
		t.Errorf("both HTTPS flags false should be valid, got: %v", err)
	}
}

func TestConfigValidate_RejectsHTTPSDisabledInProduction(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppEnv = "production"
	cfg.RedisURL = "redis://:secret@localhost:6379/0"
	cfg.HTTPSDisabled = true
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error when HTTPS_DISABLED=true in production, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS_DISABLED") {
		t.Errorf("error should mention HTTPS_DISABLED, got: %v", err)
	}
}

func TestConfigValidate_AcceptsHTTPSDisabledOutsideProduction(t *testing.T) {
	for _, env := range []string{"development", "staging"} {
		cfg := validBaseConfig()
		cfg.AppEnv = env
		if env == "staging" {
			cfg.RedisURL = "redis://:secret@localhost:6379/0"
		}
		cfg.HTTPSDisabled = true
		if err := cfg.validate(); err != nil {
			t.Errorf("HTTPS_DISABLED=true should be allowed in %s, got: %v", env, err)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Low-entropy JWT secrets
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsLowEntropyJWTAccessSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.JWTAccessSecret = strings.Repeat("a", 32) // all-same, length OK but entropy zero
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for all-same JWT_ACCESS_SECRET, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_ACCESS_SECRET") {
		t.Errorf("error should mention JWT_ACCESS_SECRET, got: %v", err)
	}
}

func TestConfigValidate_RejectsLowEntropyJWTRefreshSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.JWTRefreshSecret = strings.Repeat("z", 40)
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for all-same JWT_REFRESH_SECRET, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_REFRESH_SECRET") {
		t.Errorf("error should mention JWT_REFRESH_SECRET, got: %v", err)
	}
}

func TestConfigValidate_AcceptsHighEntropySecrets(t *testing.T) {
	cfg := validBaseConfig()
	cfg.JWTAccessSecret = "a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6"
	cfg.JWTRefreshSecret = "Z9y8X7w6V5u4T3s2R1q0P9o8N7m6L5k4"
	if err := cfg.validate(); err != nil {
		t.Errorf("high-entropy secrets should pass validation, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// Database pool constraints
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsZeroDBMaxConns(t *testing.T) {
	cfg := validBaseConfig()
	cfg.DBMaxConns = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for DB_MAX_CONNS=0, got nil")
	}
}

func TestConfigValidate_RejectsNegativeDBMinConns(t *testing.T) {
	cfg := validBaseConfig()
	cfg.DBMinConns = -1
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for DB_MIN_CONNS=-1, got nil")
	}
}

func TestConfigValidate_RejectsMinConnsExceedingMaxConns(t *testing.T) {
	cfg := validBaseConfig()
	cfg.DBMaxConns = 5
	cfg.DBMinConns = 10
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error when DB_MIN_CONNS > DB_MAX_CONNS, got nil")
	}
	if !strings.Contains(err.Error(), "DB_MIN_CONNS") {
		t.Errorf("error should mention DB_MIN_CONNS, got: %v", err)
	}
}

func TestConfigValidate_AcceptsMinConnsEqualToMaxConns(t *testing.T) {
	cfg := validBaseConfig()
	cfg.DBMaxConns = 5
	cfg.DBMinConns = 5
	if err := cfg.validate(); err != nil {
		t.Errorf("DB_MIN_CONNS == DB_MAX_CONNS should be valid, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// Mail workers
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsZeroMailWorkers(t *testing.T) {
	cfg := validBaseConfig()
	cfg.MailWorkers = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for MAIL_WORKERS=0, got nil")
	}
}

func TestConfigValidate_RejectsNegativeMailWorkers(t *testing.T) {
	cfg := validBaseConfig()
	cfg.MailWorkers = -1
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for negative MAIL_WORKERS, got nil")
	}
}

// ─────────────────────────────────────────────────────────────
// isLowEntropySecret unit tests
// ─────────────────────────────────────────────────────────────

func TestIsLowEntropySecret_AllSameCharacter(t *testing.T) {
	cases := []string{
		strings.Repeat("a", 1),
		strings.Repeat("a", 32),
		strings.Repeat("0", 64),
		strings.Repeat("Z", 100),
	}
	for _, s := range cases {
		if !isLowEntropySecret(s) {
			t.Errorf("expected isLowEntropySecret(%q) == true", s)
		}
	}
}

func TestIsLowEntropySecret_MixedCharacters(t *testing.T) {
	cases := []string{
		"ab",
		"aab",
		"aaaaaab",
		"a1b2c3d4",
		"abc123XYZ!@#",
	}
	for _, s := range cases {
		if isLowEntropySecret(s) {
			t.Errorf("expected isLowEntropySecret(%q) == false", s)
		}
	}
}

func TestIsLowEntropySecret_EmptyString(t *testing.T) {
	// Empty strings are caught by the required-field check; the entropy check
	// returns false so the two guards do not produce a confusing double-error.
	if isLowEntropySecret("") {
		t.Error("expected isLowEntropySecret(\"\") == false")
	}
}

func TestIsLowEntropySecret_RepeatedPasswordPattern(t *testing.T) {
	// This weak pattern has low Shannon entropy (~2.8 bits/char) despite being
	// long enough to pass the length check.
	weak := "password_password_password_passw"
	if !isLowEntropySecret(weak) {
		t.Error("expected isLowEntropySecret to reject repeated 'password' pattern")
	}
}

func TestIsLowEntropySecret_Accepts32CharHex(t *testing.T) {
	hexSecret := "a1b2c3d4e5f67890abcdef1234567890"
	if isLowEntropySecret(hexSecret) {
		t.Errorf("expected isLowEntropySecret to accept 32-char hex string, got rejected")
	}
}

func TestIsLowEntropySecret_Accepts64CharHex(t *testing.T) {
	hexSecret := "1a2b3c4d5e6f7890abcdef1234567890fedcba0987654321abcdef1234567890"
	if isLowEntropySecret(hexSecret) {
		t.Errorf("expected isLowEntropySecret to accept 64-char hex string, got rejected")
	}
}

// ─────────────────────────────────────────────────────────────
// APP_ENV validation
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsUnknownAppEnv(t *testing.T) {
	cases := []string{"prod", "dev", "Staging", "PRODUCTION", "test", ""}
	for _, env := range cases {
		cfg := validBaseConfig()
		cfg.AppEnv = env
		err := cfg.validate()
		if err == nil {
			t.Errorf("expected error for APP_ENV=%q, got nil", env)
			continue
		}
		if !strings.Contains(err.Error(), "APP_ENV") {
			t.Errorf("error for APP_ENV=%q should mention APP_ENV, got: %v", env, err)
		}
	}
}

func TestConfigValidate_AcceptsAllValidAppEnvs(t *testing.T) {
	for _, env := range []string{"development", "staging", "production"} {
		cfg := validBaseConfig()
		cfg.AppEnv = env
		if env != "development" {
			cfg.RedisURL = "redis://:secret@localhost:6379/0"
		}
		if err := cfg.validate(); err != nil {
			t.Errorf("APP_ENV=%q should be valid, got: %v", env, err)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// OTPValidMinutes validation
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsOTPValidMinutesBelowOne(t *testing.T) {
	cfg := validBaseConfig()
	cfg.OTPValidMinutes = 0
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for OTP_VALID_MINUTES=0, got nil")
	}
	if !strings.Contains(err.Error(), "OTP_VALID_MINUTES") {
		t.Errorf("error should mention OTP_VALID_MINUTES, got: %v", err)
	}
}

func TestConfigValidate_RejectsNegativeOTPValidMinutes(t *testing.T) {
	cfg := validBaseConfig()
	cfg.OTPValidMinutes = -1
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for OTP_VALID_MINUTES=-1, got nil")
	}
}

func TestConfigValidate_RejectsOTPValidMinutesAboveCap(t *testing.T) {
	cfg := validBaseConfig()
	cfg.OTPValidMinutes = 31
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for OTP_VALID_MINUTES=31, got nil")
	}
	if !strings.Contains(err.Error(), "OTP_VALID_MINUTES") {
		t.Errorf("error should mention OTP_VALID_MINUTES, got: %v", err)
	}
}

func TestConfigValidate_AcceptsOTPValidMinutesBoundaryValues(t *testing.T) {
	for _, mins := range []int{1, 15, 30} {
		cfg := validBaseConfig()
		cfg.OTPValidMinutes = mins
		if err := cfg.validate(); err != nil {
			t.Errorf("OTP_VALID_MINUTES=%d should be valid, got: %v", mins, err)
		}
	}
}

func TestLoad_AppliesOTPValidMinutesDefault(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("OTP_VALID_MINUTES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.OTPValidMinutes != 15 {
		t.Errorf("default OTP_VALID_MINUTES should be 15, got %d", cfg.OTPValidMinutes)
	}
}

func TestLoad_ParsesOTPValidMinutesFromEnv(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("OTP_VALID_MINUTES", "20")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.OTPValidMinutes != 20 {
		t.Errorf("OTP_VALID_MINUTES should be 20, got %d", cfg.OTPValidMinutes)
	}
}

func TestLoad_RejectsOTPValidMinutesOutOfRange(t *testing.T) {
	for _, val := range []string{"0", "31", "-5"} {
		setLoadEnv(t)
		t.Setenv("OTP_VALID_MINUTES", val)
		_, err := Load()
		if err == nil {
			t.Errorf("Load() with OTP_VALID_MINUTES=%s should fail, got nil", val)
		}
	}
}

func TestLoad_InvalidOTPValidMinutesFallsBackToDefault(t *testing.T) {
	// A non-integer value is logged and the default (15) is used;
	// validation then passes because 15 is within 1–30.
	setLoadEnv(t)
	t.Setenv("OTP_VALID_MINUTES", "notanumber")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with malformed OTP_VALID_MINUTES should fall back to default, got: %v", err)
	}
	if cfg.OTPValidMinutes != 15 {
		t.Errorf("malformed OTP_VALID_MINUTES should fall back to 15, got %d", cfg.OTPValidMinutes)
	}
}

// ─────────────────────────────────────────────────────────────
// SMTP_PORT validation
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsInvalidSMTPPort(t *testing.T) {
	for _, port := range []int{0, 80, 443, 3306, 8080, 586, 588} {
		cfg := validBaseConfig()
		cfg.SMTPPort = port
		err := cfg.validate()
		if err == nil {
			t.Errorf("expected error for SMTP_PORT=%d, got nil", port)
			continue
		}
		if !strings.Contains(err.Error(), "SMTP_PORT") {
			t.Errorf("error for SMTP_PORT=%d should mention SMTP_PORT, got: %v", port, err)
		}
	}
}

func TestConfigValidate_AcceptsValidSMTPPorts(t *testing.T) {
	for _, port := range []int{25, 465, 587} {
		cfg := validBaseConfig()
		cfg.SMTPPort = port
		if err := cfg.validate(); err != nil {
			t.Errorf("SMTP_PORT=%d should be valid, got: %v", port, err)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// TOKEN_ENCRYPTION_KEY length validation
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsShortTokenEncryptionKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.TokenEncryptionKey = "abcdef1234567890" // 16 chars, not 64
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for short TOKEN_ENCRYPTION_KEY, got nil")
	}
	if !strings.Contains(err.Error(), "TOKEN_ENCRYPTION_KEY") {
		t.Errorf("error should mention TOKEN_ENCRYPTION_KEY, got: %v", err)
	}
}

func TestConfigValidate_RejectsLongTokenEncryptionKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.TokenEncryptionKey = strings.Repeat("a1", 33) // 66 chars, not 64
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for TOKEN_ENCRYPTION_KEY != 64 chars, got nil")
	}
	if !strings.Contains(err.Error(), "TOKEN_ENCRYPTION_KEY") {
		t.Errorf("error should mention TOKEN_ENCRYPTION_KEY, got: %v", err)
	}
}

func TestConfigValidate_Accepts64CharTokenEncryptionKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.TokenEncryptionKey = "a1b2c3d4e5f67890abcdef1234567890a1b2c3d4e5f67890abcdef1234567890"
	if err := cfg.validate(); err != nil {
		t.Errorf("64-char TOKEN_ENCRYPTION_KEY should be valid, got: %v", err)
	}
}

func TestConfigValidate_RejectsNonHexTokenEncryptionKey(t *testing.T) {
	cfg := validBaseConfig()
	// 64 chars but contains 'g' and 'z' which are not valid hex digits.
	cfg.TokenEncryptionKey = "gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for non-hex TOKEN_ENCRYPTION_KEY, got nil")
	}
	if !strings.Contains(err.Error(), "TOKEN_ENCRYPTION_KEY") {
		t.Errorf("error should mention TOKEN_ENCRYPTION_KEY, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// TRUSTED_PROXIES validation (F-5)
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_AcceptsEmptyTrustedProxies(t *testing.T) {
	cfg := validBaseConfig()
	cfg.TrustedProxies = ""
	if err := cfg.validate(); err != nil {
		t.Errorf("empty TRUSTED_PROXIES should be valid, got: %v", err)
	}
}

func TestConfigValidate_AcceptsValidCIDRTrustedProxies(t *testing.T) {
	cases := []string{
		"10.0.0.0/8",
		"10.0.0.0/8,172.16.0.0/12",
		"10.0.0.0/8, 192.168.0.0/16",
		// Double-comma produces an empty segment after strings.Split; the
		// empty-segment guard (continue) must be exercised to reach 100% coverage.
		"10.0.0.0/8,,192.168.0.0/16",
	}
	for _, cidr := range cases {
		cfg := validBaseConfig()
		cfg.TrustedProxies = cidr
		if err := cfg.validate(); err != nil {
			t.Errorf("valid TRUSTED_PROXIES %q should pass, got: %v", cidr, err)
		}
	}
}

func TestConfigValidate_RejectsInvalidCIDRTrustedProxies(t *testing.T) {
	cases := []string{
		"not-a-cidr",
		"10.0.0.0",       // plain IP, not CIDR
		"10.0.0.0/8,bad", // valid then invalid
	}
	for _, cidr := range cases {
		cfg := validBaseConfig()
		cfg.TrustedProxies = cidr
		err := cfg.validate()
		if err == nil {
			t.Errorf("expected error for TRUSTED_PROXIES=%q, got nil", cidr)
			continue
		}
		if !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
			t.Errorf("error for TRUSTED_PROXIES=%q should mention TRUSTED_PROXIES, got: %v", cidr, err)
		}
	}
}

func TestLoad_ParsesTrustedProxiesFromEnv(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8,172.16.0.0/12")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with valid TRUSTED_PROXIES should succeed, got: %v", err)
	}
	if cfg.TrustedProxies != "10.0.0.0/8,172.16.0.0/12" {
		t.Errorf("TrustedProxies = %q, want 10.0.0.0/8,172.16.0.0/12", cfg.TrustedProxies)
	}
}

func TestLoad_FailsOnInvalidTrustedProxies(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("TRUSTED_PROXIES", "not-a-cidr")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() with invalid TRUSTED_PROXIES should fail, got nil")
	}
	if !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Errorf("error should mention TRUSTED_PROXIES, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// Duration and timeout validations
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsZeroMailDeliveryTimeout(t *testing.T) {
	cfg := validBaseConfig()
	cfg.MailDeliveryTimeout = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for MAIL_DELIVERY_TIMEOUT=0, got nil")
	}
}

func TestConfigValidate_RejectsNegativeMailDeliveryTimeout(t *testing.T) {
	cfg := validBaseConfig()
	cfg.MailDeliveryTimeout = -time.Second
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for negative MAIL_DELIVERY_TIMEOUT, got nil")
	}
}

func TestConfigValidate_RejectsZeroAccessTokenTTL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AccessTokenTTL = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for ACCESS_TOKEN_TTL=0, got nil")
	}
}

func TestConfigValidate_RejectsNegativeAccessTokenTTL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AccessTokenTTL = -time.Minute
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for negative ACCESS_TOKEN_TTL, got nil")
	}
}

func TestConfigValidate_RejectsZeroDBMaxConnLifetime(t *testing.T) {
	cfg := validBaseConfig()
	cfg.DBMaxConnLifetime = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for DB_MAX_CONN_LIFETIME=0, got nil")
	}
}

func TestConfigValidate_RejectsZeroDBMaxConnIdle(t *testing.T) {
	cfg := validBaseConfig()
	cfg.DBMaxConnIdle = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for DB_MAX_CONN_IDLE=0, got nil")
	}
}

func TestConfigValidate_RejectsZeroDBHealthCheck(t *testing.T) {
	cfg := validBaseConfig()
	cfg.DBHealthCheck = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for DB_HEALTH_CHECK=0, got nil")
	}
}

// ─────────────────────────────────────────────────────────────
// Load() — exercises env-var parsing and defaults
// ─────────────────────────────────────────────────────────────

// setLoadEnv sets the minimum required environment variables so that Load()
// succeeds, then returns a cleanup function that unsets them all.
// Tests may override individual vars after calling this helper.
func setLoadEnv(t *testing.T) func() {
	t.Helper()
	vars := map[string]string{
		"APP_ENV":              "development",
		"DATABASE_URL":         "postgres://user:pass@localhost/testdb",
		"SMTP_HOST":            "smtp.example.com",
		"SMTP_USERNAME":        "user@example.com",
		"SMTP_PASSWORD":        "password",
		"SMTP_FROM":            "no-reply@example.com",
		"ALLOWED_ORIGINS":      "http://localhost:3000",
		"JWT_ACCESS_SECRET":    "abcdef1234567890abcdef1234567890ab",
		"JWT_REFRESH_SECRET":   "1234567890abcdef1234567890abcdef12",
		"TOKEN_ENCRYPTION_KEY": "a1b2c3d4e5f67890abcdef1234567890a1b2c3d4e5f67890abcdef1234567890",
		// OAuth
		"GOOGLE_CLIENT_ID":     "fake-google-client-id.apps.googleusercontent.com",
		"GOOGLE_CLIENT_SECRET": "fake-google-client-secret",
		"GOOGLE_REDIRECT_URI":  "http://localhost:8080/api/v1/oauth/google/callback",
		"OAUTH_SUCCESS_URL":    "http://localhost:3000/dashboard",
		"OAUTH_ERROR_URL":      "http://localhost:3000/login",
		"TELEGRAM_BOT_TOKEN":   "1234567890:ABCdefGHIjklMNOpqrSTUvwxYZ-fake-token",
		"BOOTSTRAP_SECRET":     "test-bootstrap-secret-value-here",
	}
	keys := make([]string, 0, len(vars))
	for k, v := range vars {
		t.Setenv(k, v)
		keys = append(keys, k)
	}
	// Return a no-op — t.Setenv already restores on cleanup.
	return func() {}
}

func TestLoad_SucceedsWithMinimalEnv(t *testing.T) {
	setLoadEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with valid env should succeed, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil Config")
	}
}

func TestLoad_AppliesServerDefaults(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("ADDR", "")
	t.Setenv("APP_NAME", "")
	t.Setenv("MAIL_WORKERS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("default ADDR should be :8080, got %q", cfg.Addr)
	}
	if cfg.AppName != "Store" {
		t.Errorf("default APP_NAME should be Store, got %q", cfg.AppName)
	}
	if cfg.MailWorkers != 4 {
		t.Errorf("default MAIL_WORKERS should be 4, got %d", cfg.MailWorkers)
	}
}

func TestLoad_AppliesDatabaseDefaults(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("DB_MAX_CONNS", "")
	t.Setenv("DB_MIN_CONNS", "")
	t.Setenv("DB_MAX_CONN_LIFETIME", "")
	t.Setenv("DB_MAX_CONN_IDLE", "")
	t.Setenv("DB_HEALTH_CHECK", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.DBMaxConns != 20 {
		t.Errorf("default DB_MAX_CONNS should be 20, got %d", cfg.DBMaxConns)
	}
	if cfg.DBMinConns != 2 {
		t.Errorf("default DB_MIN_CONNS should be 2, got %d", cfg.DBMinConns)
	}
	if cfg.DBMaxConnLifetime != 30*time.Minute {
		t.Errorf("default DB_MAX_CONN_LIFETIME should be 30m, got %s", cfg.DBMaxConnLifetime)
	}
	if cfg.DBMaxConnIdle != 5*time.Minute {
		t.Errorf("default DB_MAX_CONN_IDLE should be 5m, got %s", cfg.DBMaxConnIdle)
	}
	if cfg.DBHealthCheck != time.Minute {
		t.Errorf("default DB_HEALTH_CHECK should be 1m, got %s", cfg.DBHealthCheck)
	}
}

func TestLoad_AppliesJWTDefaults(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("ACCESS_TOKEN_TTL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.AccessTokenTTL != 15*time.Minute {
		t.Errorf("default ACCESS_TOKEN_TTL should be 15m, got %s", cfg.AccessTokenTTL)
	}
}

func TestLoad_AppliesSMTPPortDefault(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("SMTP_PORT", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("default SMTP_PORT should be 587, got %d", cfg.SMTPPort)
	}
}

func TestLoad_ParsesAllowedOriginsFromEnv(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000, https://app.example.com ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("expected 2 origins, got %d: %v", len(cfg.AllowedOrigins), cfg.AllowedOrigins)
	}
	if cfg.AllowedOrigins[0] != "http://localhost:3000" {
		t.Errorf("first origin should be trimmed, got %q", cfg.AllowedOrigins[0])
	}
	if cfg.AllowedOrigins[1] != "https://app.example.com" {
		t.Errorf("second origin should be trimmed, got %q", cfg.AllowedOrigins[1])
	}
}

func TestLoad_ParsesBoolFlags(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("DOCS_ENABLED", "true")
	t.Setenv("HTTPS_ENABLED", "true")
	t.Setenv("HTTPS_DISABLED", "") // must not be true while HTTPS_ENABLED=true
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if !cfg.HTTPSEnabled {
		t.Error("HTTPSEnabled should be true when HTTPS_ENABLED=true")
	}
}

func TestLoad_ReturnsErrorOnMissingRequired(t *testing.T) {
	// Keep APP_ENV valid so we reach the required-field check.
	// Clear all other required vars — Load() must list every missing one.
	for _, key := range []string{
		"DATABASE_URL", "SMTP_HOST", "SMTP_USERNAME",
		"SMTP_PASSWORD", "SMTP_FROM", "ALLOWED_ORIGINS",
		"JWT_ACCESS_SECRET", "JWT_REFRESH_SECRET", "TOKEN_ENCRYPTION_KEY",
		"BOOTSTRAP_SECRET",
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URI",
		"OAUTH_SUCCESS_URL", "OAUTH_ERROR_URL",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "development")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() with missing required vars should fail, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL, got: %v", err)
	}
}

func TestLoad_ParsesOAuthFieldsFromEnv(t *testing.T) {
	setLoadEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.GoogleClientID != "fake-google-client-id.apps.googleusercontent.com" {
		t.Errorf("GoogleClientID = %q, want fake-google-client-id.apps.googleusercontent.com", cfg.GoogleClientID)
	}
	if cfg.GoogleClientSecret != "fake-google-client-secret" {
		t.Errorf("GoogleClientSecret = %q, want fake-google-client-secret", cfg.GoogleClientSecret)
	}
	if cfg.GoogleRedirectURI != "http://localhost:8080/api/v1/oauth/google/callback" {
		t.Errorf("GoogleRedirectURI = %q, unexpected", cfg.GoogleRedirectURI)
	}
	if cfg.OAuthSuccessURL != "http://localhost:3000/dashboard" {
		t.Errorf("OAuthSuccessURL = %q, unexpected", cfg.OAuthSuccessURL)
	}
	if cfg.OAuthErrorURL != "http://localhost:3000/login" {
		t.Errorf("OAuthErrorURL = %q, unexpected", cfg.OAuthErrorURL)
	}
}

// ─────────────────────────────────────────────────────────────
// TestDatabaseURL and TestRedisURL
// ─────────────────────────────────────────────────────────────

func TestTestDatabaseURL_ReturnsEnvVar(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "postgres://test:test@localhost/mytest")
	if got := TestDatabaseURL(); got != "postgres://test:test@localhost/mytest" {
		t.Errorf("TestDatabaseURL() = %q, want postgres://test:test@localhost/mytest", got)
	}
}

func TestTestDatabaseURL_ReturnsEmptyWhenUnset(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "")
	got := TestDatabaseURL()
	if got != "" {
		t.Errorf("TestDatabaseURL() should return empty string when TEST_DATABASE_URL is unset, got %q", got)
	}
}

func TestTestRedisURL_PrefersTESTVar(t *testing.T) {
	t.Setenv("TEST_REDIS_URL", "redis://test:6379/1")
	t.Setenv("REDIS_URL", "redis://prod:6379/0")
	if got := TestRedisURL(); got != "redis://test:6379/1" {
		t.Errorf("TestRedisURL() should prefer TEST_REDIS_URL, got %q", got)
	}
}

func TestTestRedisURL_FallsBackToRedisURL(t *testing.T) {
	t.Setenv("TEST_REDIS_URL", "")
	t.Setenv("REDIS_URL", "redis://fallback:6379/0")
	if got := TestRedisURL(); got != "redis://fallback:6379/0" {
		t.Errorf("TestRedisURL() should fall back to REDIS_URL, got %q", got)
	}
}

func TestTestRedisURL_ReturnsEmptyWhenNeitherSet(t *testing.T) {
	t.Setenv("TEST_REDIS_URL", "")
	t.Setenv("REDIS_URL", "")
	if got := TestRedisURL(); got != "" {
		t.Errorf("TestRedisURL() should return empty when neither var is set, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────
// Private helper functions
// ─────────────────────────────────────────────────────────────

func TestGetEnv_ReturnsValue(t *testing.T) {
	t.Setenv("TEST_GET_ENV_KEY", "hello")
	if got := getEnv("TEST_GET_ENV_KEY", "default"); got != "hello" {
		t.Errorf("getEnv() = %q, want %q", got, "hello")
	}
}

func TestGetEnv_ReturnsFallbackWhenUnset(t *testing.T) {
	t.Setenv("TEST_GET_ENV_UNSET", "")
	if got := getEnv("TEST_GET_ENV_UNSET", "default"); got != "default" {
		t.Errorf("getEnv() = %q, want %q", got, "default")
	}
}

func TestGetEnvInt_ParsesInteger(t *testing.T) {
	t.Setenv("TEST_GET_ENV_INT", "42")
	if got := getEnvInt("TEST_GET_ENV_INT", 0); got != 42 {
		t.Errorf("getEnvInt() = %d, want 42", got)
	}
}

func TestGetEnvInt_ReturnsFallbackOnEmpty(t *testing.T) {
	t.Setenv("TEST_GET_ENV_INT_EMPTY", "")
	if got := getEnvInt("TEST_GET_ENV_INT_EMPTY", 7); got != 7 {
		t.Errorf("getEnvInt() = %d, want 7 (fallback)", got)
	}
}

func TestGetEnvDuration_ParsesDuration(t *testing.T) {
	t.Setenv("TEST_GET_ENV_DUR", "10m")
	if got := getEnvDuration("TEST_GET_ENV_DUR", 0); got != 10*time.Minute {
		t.Errorf("getEnvDuration() = %s, want 10m", got)
	}
}

func TestGetEnvDuration_ReturnsFallbackOnEmpty(t *testing.T) {
	t.Setenv("TEST_GET_ENV_DUR_EMPTY", "")
	if got := getEnvDuration("TEST_GET_ENV_DUR_EMPTY", 5*time.Second); got != 5*time.Second {
		t.Errorf("getEnvDuration() = %s, want 5s (fallback)", got)
	}
}

func TestGetEnvInt32_ReturnsFallbackOnInt32Overflow(t *testing.T) {
	// math.MaxInt32 == 2147483647; a value one above it is a valid int64/int on
	// 64-bit platforms but overflows int32. getEnvInt32 must detect this and
	// return the fallback instead of silently wrapping.
	t.Setenv("TEST_GET_ENV_INT32_OVERFLOW", "2147483648") // MaxInt32 + 1
	const fallback int32 = 20
	if got := getEnvInt32("TEST_GET_ENV_INT32_OVERFLOW", fallback); got != fallback {
		t.Errorf("getEnvInt32() = %d, want fallback %d on overflow", got, fallback)
	}

	// Also cover the negative overflow path (below MinInt32).
	t.Setenv("TEST_GET_ENV_INT32_UNDERFLOW", "-2147483649") // MinInt32 - 1
	if got := getEnvInt32("TEST_GET_ENV_INT32_UNDERFLOW", fallback); got != fallback {
		t.Errorf("getEnvInt32() = %d, want fallback %d on underflow", got, fallback)
	}
}

// ─────────────────────────────────────────────────────────────
// parseBoolEnv — unrecognised value (F-6)
// ─────────────────────────────────────────────────────────────

func TestParseBoolEnv_UnrecognisedValueReturnsFalse(t *testing.T) {
	for _, val := range []string{"YES", "NO", "on", "off", "enable", "1x"} {
		t.Setenv("TEST_PARSE_BOOL_BAD", val)
		if got := parseBoolEnv("TEST_PARSE_BOOL_BAD"); got != false {
			t.Errorf("parseBoolEnv(%q) = %v, want false", val, got)
		}
	}
}

func TestParseBoolEnv_EmptyValueReturnsFalse(t *testing.T) {
	t.Setenv("TEST_PARSE_BOOL_EMPTY", "")
	if got := parseBoolEnv("TEST_PARSE_BOOL_EMPTY"); got != false {
		t.Errorf("parseBoolEnv(empty) = %v, want false", got)
	}
}

// ─────────────────────────────────────────────────────────────
// F-10 — warn on malformed env-var parse
// ─────────────────────────────────────────────────────────────

func TestGetEnvInt_ReturnsFallbackAndDoesNotPanicOnInvalid(t *testing.T) {
	t.Setenv("CFG_INT_BAD", "20k")
	if got := getEnvInt("CFG_INT_BAD", 99); got != 99 {
		t.Errorf("expected fallback 99, got %d", got)
	}
}

func TestGetEnvDuration_ReturnsFallbackAndDoesNotPanicOnInvalid(t *testing.T) {
	t.Setenv("CFG_DUR_BAD", "30")
	if got := getEnvDuration("CFG_DUR_BAD", 5*time.Second); got != 5*time.Second {
		t.Errorf("expected fallback 5s, got %s", got)
	}
}

// ─────────────────────────────────────────────────────────────
// F-10 — CRLF injection in SMTP_FROM
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsCRLFInSMTPFrom(t *testing.T) {
	cases := []string{
		"sender@example.com\r\nBcc: attacker@evil.com",
		"sender@example.com\nBcc: attacker@evil.com",
		"sender@example.com\r",
	}
	for _, from := range cases {
		cfg := validBaseConfig()
		cfg.SMTPFrom = from
		if err := cfg.validate(); err == nil {
			t.Errorf("expected error for SmtpFrom=%q with CRLF, got nil", from)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// F-13 — missing test cases
// ─────────────────────────────────────────────────────────────

func TestLoad_AppliesMailDeliveryTimeoutDefault(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("MAIL_DELIVERY_TIMEOUT", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.MailDeliveryTimeout != 30*time.Second {
		t.Errorf("default MAIL_DELIVERY_TIMEOUT should be 30s, got %s", cfg.MailDeliveryTimeout)
	}
}

func TestLoad_InvalidSMTPPortFallsBackToDefault(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("SMTP_PORT", "not-a-port")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("malformed SMTP_PORT should fall back to 587, got %d", cfg.SMTPPort)
	}
}

func TestLoad_HTTPSDisabled(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("HTTPS_DISABLED", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if !cfg.HTTPSDisabled {
		t.Error("HTTPSDisabled should be true when HTTPS_DISABLED=true")
	}
}

func TestConfigValidate_RejectsMissingJWTAccessSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.JWTAccessSecret = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing JWT_ACCESS_SECRET, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_ACCESS_SECRET") {
		t.Errorf("error should mention JWT_ACCESS_SECRET, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingJWTRefreshSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.JWTRefreshSecret = ""
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for missing JWT_REFRESH_SECRET, got nil")
	}
}

func TestConfigValidate_RejectsAllWhitespaceAllowedOrigins(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AllowedOrigins = nil // simulate ALLOWED_ORIGINS=",  ,"
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for empty AllowedOrigins slice, got nil")
	}
	if !strings.Contains(err.Error(), "ALLOWED_ORIGINS") {
		t.Errorf("error should mention ALLOWED_ORIGINS, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// APP_NAME validation and trimming
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsEmptyAppName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppName = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for empty APP_NAME, got nil")
	}
	if !strings.Contains(err.Error(), "APP_NAME") {
		t.Errorf("error should mention APP_NAME, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// BOOTSTRAP_SECRET
// ─────────────────────────────────────────────────────────────

func TestConfigValidate_RejectsMissingBootstrapSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.BootstrapSecret = ""
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing BOOTSTRAP_SECRET, got nil")
	}
	if !strings.Contains(err.Error(), "BOOTSTRAP_SECRET") {
		t.Errorf("error should mention BOOTSTRAP_SECRET, got: %v", err)
	}
}

func TestConfigValidate_AcceptsNonEmptyBootstrapSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.BootstrapSecret = "any-non-empty-value"
	if err := cfg.validate(); err != nil {
		t.Errorf("non-empty BOOTSTRAP_SECRET should be valid, got: %v", err)
	}
}

func TestLoad_ParsesBootstrapSecretFromEnv(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("BOOTSTRAP_SECRET", "my-bootstrap-passphrase")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.BootstrapSecret != "my-bootstrap-passphrase" {
		t.Errorf("BootstrapSecret = %q, want \"my-bootstrap-passphrase\"", cfg.BootstrapSecret)
	}
}

func TestLoad_RejectsEmptyBootstrapSecret(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("BOOTSTRAP_SECRET", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() with empty BOOTSTRAP_SECRET should fail, got nil")
	}
	if !strings.Contains(err.Error(), "BOOTSTRAP_SECRET") {
		t.Errorf("error should mention BOOTSTRAP_SECRET, got: %v", err)
	}
}

func TestConfigValidate_ReportsBootstrapSecretInBulkMissingError(t *testing.T) {
	// BOOTSTRAP_SECRET must appear in the bulk missing-field error so the
	// operator can fix all absent required vars in one restart.
	cfg := &Config{
		AppEnv:           "development",
		AllowedOrigins:   []string{"http://localhost:3000"},
		JWTAccessSecret:  "abcdef1234567890abcdef1234567890ab",
		JWTRefreshSecret: "1234567890abcdef1234567890abcdef12",
		MailWorkers:      4,
		DBMaxConns:       20,
		DBMinConns:       2,
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for multiple missing fields, got nil")
	}
	if !strings.Contains(err.Error(), "BOOTSTRAP_SECRET") {
		t.Errorf("bulk missing-field error should mention BOOTSTRAP_SECRET, got: %v", err)
	}
}

func TestConfigValidate_RejectsAppNameExceedingMaxLength(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppName = strings.Repeat("A", 65) // one over the 64-char cap
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for APP_NAME > 64 chars, got nil")
	}
	if !strings.Contains(err.Error(), "APP_NAME") {
		t.Errorf("error should mention APP_NAME, got: %v", err)
	}
}

func TestConfigValidate_AcceptsAppNameAtMaxLength(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppName = strings.Repeat("A", 64)
	if err := cfg.validate(); err != nil {
		t.Errorf("APP_NAME of exactly 64 chars should be valid, got: %v", err)
	}
}

func TestConfigValidate_AcceptsTypicalAppName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.AppName = "Vend"
	if err := cfg.validate(); err != nil {
		t.Errorf("APP_NAME=Vend should be valid, got: %v", err)
	}
}

// —— trimAppName unit tests ———————————————————————————————————————

func TestTrimAppName_StripsQuotes(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`"Vend"`, "Vend"},           // double-quoted .env value
		{`Vend`, "Vend"},             // unquoted
		{`  Vend  `, "Vend"},         // whitespace only
		{`  "Vend"  `, "Vend"},       // whitespace + quotes
		{`"Acme Corp"`, "Acme Corp"}, // quoted with space inside
		{`""`, ""},                   // empty quoted string → empty (caught by validate)
		{``, ""},                     // truly empty
		{`"`, `"`},                   // single quote, not a pair → unchanged
	}
	for _, tc := range cases {
		if got := trimAppName(tc.input); got != tc.want {
			t.Errorf("trimAppName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLoad_AppliesAppNameDefault(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("APP_NAME", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.AppName != "Store" {
		t.Errorf("default APP_NAME should be Store, got %q", cfg.AppName)
	}
}

func TestLoad_ParsesAppNameFromEnv(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("APP_NAME", "Vend")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.AppName != "Vend" {
		t.Errorf("APP_NAME should be Vend, got %q", cfg.AppName)
	}
}

func TestLoad_StripsQuotesFromAppName(t *testing.T) {
	setLoadEnv(t)
	t.Setenv("APP_NAME", `"Vend"`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.AppName != "Vend" {
		t.Errorf("quoted APP_NAME should be stripped to Vend, got %q", cfg.AppName)
	}
}

func TestLoad_RejectsEmptyAppName(t *testing.T) {
	setLoadEnv(t)
	// Explicitly set to a quoted-empty value so trimAppName produces ""
	// and validate() must reject it.
	t.Setenv("APP_NAME", `""`)
	_, err := Load()
	if err == nil {
		t.Fatal("Load() with APP_NAME=\"\" should fail, got nil")
	}
	if !strings.Contains(err.Error(), "APP_NAME") {
		t.Errorf("error should mention APP_NAME, got: %v", err)
	}
}
