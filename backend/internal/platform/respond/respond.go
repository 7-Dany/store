// Package respond provides JSON response helpers for HTTP handlers.
package respond

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// contentType is the canonical Content-Type for every JSON response,
// including the fallback 500 error body.
const contentType = "application/json; charset=utf-8"

// fallbackBody is the pre-encoded body used when json.Marshal fails.
// It is a compile-time constant so it can never itself fail to encode.
const fallbackBody = `{"code":"internal_error","message":"internal server error"}`

// APIError is the standard error envelope returned by all handlers.
//
// Client code MUST switch on "code" — never on "message", which is
// human-readable, may be localised, and may change between releases.
type APIError struct {
	Code    string `json:"code"`    // machine-readable: "email_taken", "validation_error", …
	Message string `json:"message"` // human-readable: displayed in the UI
}

var log = telemetry.New("respond")

// JSON writes v as JSON with the given HTTP status code.
// If marshalling fails it writes a 500 application/json error body instead.
// A nil v is marshalled as JSON null; use NoContent for 204 responses with
// no body.
func JSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Error(context.Background(), "JSON: marshal failed",
			"error", err,
			"value_type", fmt.Sprintf("%T", v),
		)
		writeRaw(w, http.StatusInternalServerError, []byte(fallbackBody))
		return
	}
	writeRaw(w, status, b)
}

// Error writes a standard APIError JSON response.
// It is a thin wrapper around JSON so all header-ordering and logging
// guarantees of JSON apply here too.
//
// Caller responsibilities:
//   - message must contain no internal error details or sensitive data;
//     this function writes the string verbatim to the response body.
//   - For 5xx status codes, the caller must log the underlying error before
//     calling Error; this function does not log on its own.
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, APIError{Code: code, Message: message})
}

// NoContent writes a 204 No Content response with no body.
// Prefer this over JSON(w, 204, nil), which would write "null" and violate RFC 9110 §15.3.5.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// MaxBodyBytes is the maximum request body size accepted by all auth handlers.
const MaxBodyBytes = 1 << 20 // 1 MiB

// IsBodyTooLarge reports whether err is an *http.MaxBytesError produced by
// http.MaxBytesReader.
func IsBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// ClientIP extracts the host portion from r.RemoteAddr.
// The TrustedProxyRealIP middleware at the router level already rewrites
// r.RemoteAddr from X-Forwarded-For / X-Real-IP when appropriate.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ── internal helpers ──────────────────────────────────────────────────────────

// writeRaw sets Content-Type, writes the status code, then writes b.
// Keeping this in one place ensures the header-before-WriteHeader invariant
// is enforced for both the happy path and the fallback error path.
//
// If Content-Type is already set it means a previous writeRaw call already
// committed a response on this writer; in that case the call is a no-op so
// that the first response always wins (mirrors net/http's own WriteHeader
// behaviour).
func writeRaw(w http.ResponseWriter, status int, b []byte) {
	if w.Header().Get("Content-Type") != "" {
		return
	}
	if status < 100 || status > 599 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := w.Write(b); err != nil {
		log.Debug(context.Background(), "write failed", "error", err)
	}
}

// DecodeJSON decodes the JSON body of r into a value of type T.
//
// The caller must apply http.MaxBytesReader before calling DecodeJSON.
// DecodeJSON writes a 422 validation_error response and returns false on a
// body-too-large error or any JSON decoding failure. It returns the decoded
// value and true on success.
//
// Unknown fields in the request body are silently ignored. DisallowUnknownFields
// is intentionally not called: it would reject requests from clients that send
// extra fields for forward-compatibility reasons, and field-level validation
// is the responsibility of the calling handler's validator. If this package
// ever adds DisallowUnknownFields, TestDecodeJSON_UnknownFields_AreIgnoredSilently
// will fail and must be updated to assert a 422 response instead.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		if IsBodyTooLarge(err) {
			Error(w, http.StatusRequestEntityTooLarge, "validation_error", "request body exceeds maximum allowed size")
			return v, false
		}
		// The JSON decoder may bail out before reading the full body (e.g. a
		// syntax error at byte 1 of a 2 MiB payload). Drain the remaining bytes
		// so that MaxBytesReader can still fire its size-limit error.
		if _, drainErr := io.Copy(io.Discard, r.Body); IsBodyTooLarge(drainErr) {
			Error(w, http.StatusRequestEntityTooLarge, "validation_error", "request body exceeds maximum allowed size")
			return v, false
		}
		// Syntax errors and truncated bodies are client mistakes → 400 Bad Request.
		// Type-mismatch / unknown-field errors remain → 422 Unprocessable Entity.
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) || errors.Is(err, io.ErrUnexpectedEOF) {
			Error(w, http.StatusBadRequest, "bad_request", "malformed JSON body")
			return v, false
		}
		Error(w, http.StatusUnprocessableEntity, "validation_error", "invalid JSON body")
		return v, false
	}
	return v, true
}
