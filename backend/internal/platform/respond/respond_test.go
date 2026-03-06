package respond_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/stretchr/testify/require"
)

// failWriter wraps httptest.ResponseRecorder but makes Write always fail.
// This exercises the slog.Debug path in writeRaw when w.Write returns an error.
type failWriter struct {
	*httptest.ResponseRecorder
}

func (f *failWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON
// ─────────────────────────────────────────────────────────────────────────────

func TestJSON_HappyPath(t *testing.T) {
	t.Parallel()

	type payload struct {
		Name string `json:"name"`
	}

	w := httptest.NewRecorder()
	respond.JSON(w, http.StatusOK, payload{Name: "alice"})

	res := w.Result()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))

	var got payload
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	require.Equal(t, "alice", got.Name)
}

func TestJSON_StatusCodeIsPreserved(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.JSON(w, http.StatusCreated, map[string]string{"x": "y"})

	require.Equal(t, http.StatusCreated, w.Result().StatusCode)
}

func TestJSON_ContentTypeSetBeforeWriteHeader(t *testing.T) {
	t.Parallel()

	// Use a recorder and confirm Content-Type is present in the final response.
	// httptest.ResponseRecorder captures headers correctly only when they are
	// set before WriteHeader, which is the invariant we are verifying.
	w := httptest.NewRecorder()
	respond.JSON(w, http.StatusOK, struct{}{})

	require.Equal(t, "application/json; charset=utf-8", w.Result().Header.Get("Content-Type"))
}

func TestJSON_NilValue(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.JSON(w, http.StatusOK, nil)

	res := w.Result()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))
	require.Equal(t, "null", w.Body.String())
}

func TestJSON_UnmarshalableValue_FallsBackTo500(t *testing.T) {
	t.Parallel()

	// A channel is not JSON-serialisable and forces the fallback path.
	w := httptest.NewRecorder()
	respond.JSON(w, http.StatusOK, make(chan int))

	res := w.Result()
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))

	var body respond.APIError
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "internal_error", body.Code)
	require.Equal(t, "internal server error", body.Message)
}

// ─────────────────────────────────────────────────────────────────────────────
// Error
// ─────────────────────────────────────────────────────────────────────────────

func TestError_WritesAPIErrorEnvelope(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.Error(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")

	res := w.Result()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))

	var body respond.APIError
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "invalid_credentials", body.Code)
	require.Equal(t, "invalid email or password", body.Message)
}

func TestError_EmptyCodeAndMessage(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.Error(w, http.StatusBadRequest, "", "")

	res := w.Result()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var body respond.APIError
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "", body.Code)
	require.Equal(t, "", body.Message)
}

func TestError_StatusCodesPreserved(t *testing.T) {
	t.Parallel()

	codes := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
	}

	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			respond.Error(w, code, "some_code", "some message")
			require.Equal(t, code, w.Result().StatusCode)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NoContent
// ─────────────────────────────────────────────────────────────────────────────

func TestNoContent_Status204(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.NoContent(w)

	res := w.Result()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestNoContent_NoBody(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.NoContent(w)

	require.Empty(t, w.Body.String())
}

func TestNoContent_NoContentTypeHeader(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.NoContent(w)

	// RFC 9110 §15.3.5: 204 responses MUST NOT have a message body.
	// We also must not set Content-Type for a bodyless response.
	require.Empty(t, w.Result().Header.Get("Content-Type"))
}

// ─────────────────────────────────────────────────────────────────────────────
// APIError JSON shape
// ─────────────────────────────────────────────────────────────────────────────

func TestAPIError_JSONTags(t *testing.T) {
	t.Parallel()

	ae := respond.APIError{Code: "foo", Message: "bar"}
	b, err := json.Marshal(ae)
	require.NoError(t, err)

	var m map[string]string
	require.NoError(t, json.Unmarshal(b, &m))
	require.Equal(t, "foo", m["code"])
	require.Equal(t, "bar", m["message"])
	// Ensure no extra fields are emitted.
	require.Len(t, m, 2)
}

// ─────────────────────────────────────────────────────────────────────────────
// writeRaw error path (via JSON)
// ─────────────────────────────────────────────────────────────────────────────

// TestJSON_CalledTwice_SecondCallIsNoOp verifies that calling respond.JSON a
// second time on the same ResponseWriter does not overwrite the first response.
// net/http ignores duplicate WriteHeader calls after the first, so the status
// code and body of the first call are preserved.
func TestJSON_CalledTwice_SecondCallIsNoOp(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()

	respond.JSON(w, http.StatusOK, map[string]string{"call": "first"})
	respond.JSON(w, http.StatusCreated, map[string]string{"call": "second"})

	res := w.Result()
	// WriteHeader is a no-op after first call — status must still be 200.
	require.Equal(t, http.StatusOK, res.StatusCode)
	// Body must contain only the first payload.
	require.Contains(t, w.Body.String(), "first")
	require.NotContains(t, w.Body.String(), "second")
}

// TestJSON_WriteFailure verifies that respond.JSON does not panic when the
// underlying ResponseWriter.Write call fails. The slog.Debug log line inside
// writeRaw is the only observable side-effect; we simply assert no panic and
// that the status code was still set (WriteHeader succeeds before Write).
func TestJSON_WriteFailure_DoesNotPanic(t *testing.T) {
	t.Parallel()

	w := &failWriter{ResponseRecorder: httptest.NewRecorder()}

	// Must not panic even though w.Write will return an error.
	require.NotPanics(t, func() {
		respond.JSON(w, http.StatusOK, map[string]string{"k": "v"})
	})

	// WriteHeader is called before Write, so the recorder captures the status.
	require.Equal(t, http.StatusOK, w.Code)
}

type decodeTarget struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// makeRequest builds a *http.Request whose body is capped by MaxBytesReader,
// mirroring what real handlers do.
func makeRequest(t *testing.T, body string, limit int64) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return w, r
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — happy path
// ─────────────────────────────────────────────────────────────────────────────

func TestDecodeJSON_ValidJSON_ReturnsStructAndTrue(t *testing.T) {
	t.Parallel()

	w, r := makeRequest(t, `{"name":"alice","age":30}`, respond.MaxBodyBytes)

	got, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.True(t, ok)
	require.Equal(t, "alice", got.Name)
	require.Equal(t, 30, got.Age)
	require.Equal(t, http.StatusOK, w.Code) // no error written
}

func TestDecodeJSON_ValidJSON_NoErrorResponse(t *testing.T) {
	t.Parallel()

	w, r := makeRequest(t, `{"name":"bob"}`, respond.MaxBodyBytes)
	_, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.True(t, ok)
	require.Empty(t, w.Body.String())
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — body too large
// ─────────────────────────────────────────────────────────────────────────────

func TestDecodeJSON_BodyTooLarge_Returns413AndFalse(t *testing.T) {
	t.Parallel()

	// Body must be syntactically valid JSON long enough that the decoder
	// reads past the 4-byte limit before flagging a syntax error. A raw
	// JSON string of 100 characters satisfies this: the decoder reads '{'
	// then keeps consuming until MaxBytesReader fires.
	bigBody := `{"name":"` + strings.Repeat("a", 100) + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(bigBody))
	r.Body = http.MaxBytesReader(w, r.Body, 4)

	_, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.False(t, ok)
	res := w.Result()
	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)

	var body respond.APIError
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "validation_error", body.Code)
	require.Contains(t, body.Message, "exceeds")
}

func TestDecodeJSON_MaxBytesReturns422(t *testing.T) {
	t.Parallel()
	bigBody := `{"name":"` + strings.Repeat("a", 1100) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 1024)
	w := httptest.NewRecorder()
	_, ok := respond.DecodeJSON[map[string]string](w, req)
	require.False(t, ok)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — malformed JSON
// ─────────────────────────────────────────────────────────────────────────────

func TestDecodeJSON_MalformedJSON_Returns422AndFalse(t *testing.T) {
	t.Parallel()

	w, r := makeRequest(t, `{not valid json`, respond.MaxBodyBytes)

	_, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.False(t, ok)
	res := w.Result()
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)

	var body respond.APIError
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "validation_error", body.Code)
	require.Equal(t, "invalid JSON body", body.Message)
}

func TestDecodeJSON_EmptyBody_Returns422AndFalse(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	r.Body = io.NopCloser(bytes.NewReader(nil))

	_, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.False(t, ok)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — zero value returned on failure
// ─────────────────────────────────────────────────────────────────────────────

func TestDecodeJSON_OnError_ReturnsZeroValue(t *testing.T) {
	t.Parallel()

	w, r := makeRequest(t, `bad`, respond.MaxBodyBytes)

	got, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.False(t, ok)
	require.Equal(t, decodeTarget{}, got) // zero value
}

// ─────────────────────────────────────────────────────────────────────────────
// IsBodyTooLarge
// ─────────────────────────────────────────────────────────────────────────────

func TestIsBodyTooLarge_WithMaxBytesError_ReturnsTrue(t *testing.T) {
	t.Parallel()

	// Trigger a real *http.MaxBytesError by reading a body that exceeds the limit.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"alice"}`))
	r.Body = http.MaxBytesReader(w, r.Body, 1) // 1-byte limit guarantees overflow

	buf := make([]byte, 64)
	_, err := r.Body.Read(buf)

	require.True(t, respond.IsBodyTooLarge(err),
		"expected IsBodyTooLarge to return true for *http.MaxBytesError")
}

func TestIsBodyTooLarge_WithOtherError_ReturnsFalse(t *testing.T) {
	t.Parallel()

	require.False(t, respond.IsBodyTooLarge(errors.New("some other error")))
}

func TestIsBodyTooLarge_WithNilError_ReturnsFalse(t *testing.T) {
	t.Parallel()

	require.False(t, respond.IsBodyTooLarge(nil))
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — unknown fields
// ─────────────────────────────────────────────────────────────────────────────

// TestDecodeJSON_UnknownFields_AreIgnoredSilently documents the current
// behaviour: DecodeJSON does NOT call DisallowUnknownFields, so extra fields
// in the request body are silently discarded and decoding succeeds.
//
// If DisallowUnknownFields is ever added, this test will fail and must be
// updated to assert a 422 response instead.
func TestDecodeJSON_UnknownFields_AreIgnoredSilently(t *testing.T) {
	t.Parallel()

	w, r := makeRequest(t, `{"name":"carol","age":25,"extra_field":"ignored"}`, respond.MaxBodyBytes)

	got, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.True(t, ok, "expected decode to succeed when unknown fields are present")
	require.Equal(t, "carol", got.Name)
	require.Equal(t, 25, got.Age)
	// No error response must have been written.
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Body.String())
}

// ─────────────────────────────────────────────────────────────────────────────
// ClientIP
// ─────────────────────────────────────────────────────────────────────────────

// respond.go:79.39,81.16 — happy path: SplitHostPort succeeds; host is returned.
func TestClientIP_WithPort_ReturnsHost(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.1:54321"
	require.Equal(t, "192.168.1.1", respond.ClientIP(r))
}

// respond.go:81.16,83.3 — error path: SplitHostPort fails (no port); raw
// RemoteAddr is returned unchanged.
func TestClientIP_WithoutPort_ReturnsRaw(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1" // bare IP, no port — SplitHostPort returns an error
	require.Equal(t, "10.0.0.1", respond.ClientIP(r))
}

// respond.go:84.2,84.13 — the non-error "return host" branch is exercised by
// TestClientIP_WithPort_ReturnsHost above; this companion test verifies an IPv6
// host:port address also follows the same path correctly.
func TestClientIP_IPv6WithPort_ReturnsHost(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[2001:db8::1]:8080"
	require.Equal(t, "2001:db8::1", respond.ClientIP(r))
}

// respond.go:101.34,103.3 — writeRaw normalises an out-of-range status code
// to 500 before calling WriteHeader.  A caller that passes 0 (or any value
// outside 100–599) must still receive a valid 500 JSON error response.
func TestJSON_StatusBelowRange_NormalizedTo500(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.JSON(w, 0, map[string]string{"k": "v"})
	require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	require.Equal(t, "application/json; charset=utf-8", w.Result().Header.Get("Content-Type"))
}

func TestJSON_StatusAboveRange_NormalizedTo500(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	respond.JSON(w, 700, map[string]string{"k": "v"})
	require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	require.Equal(t, "application/json; charset=utf-8", w.Result().Header.Get("Content-Type"))
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — caller responsibility: MaxBytesReader must be applied first
// ─────────────────────────────────────────────────────────────────────────────

// TestDecodeJSON_WithoutMaxBytesReader_AcceptsBodyLargerThanMaxBodyBytes
// documents the caller-responsibility contract stated in the DecodeJSON doc
// comment: if the caller omits http.MaxBytesReader, DecodeJSON has no size
// limit of its own and will accept an arbitrarily large body.
//
// This test exists to make that contract explicit and visible. Any future
// change that adds a defensive size limit inside DecodeJSON will cause this
// test to fail (at 413/422), signalling that the doc comment must also be updated.
func TestDecodeJSON_WithoutMaxBytesReader_AcceptsBodyLargerThanMaxBodyBytes(t *testing.T) {
	t.Parallel()

	// Build a body that is bigger than MaxBodyBytes (1 MiB) but still valid JSON.
	bigValue := strings.Repeat("a", int(respond.MaxBodyBytes)+1024)
	body := `{"name":"` + bigValue + `"}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	// Intentionally NOT wrapping r.Body with MaxBytesReader.

	got, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.True(t, ok, "without MaxBytesReader DecodeJSON imposes no size limit")
	require.Equal(t, bigValue, got.Name)
	require.Equal(t, http.StatusOK, w.Code)
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — truncated / partially valid JSON
// ─────────────────────────────────────────────────────────────────────────────

func TestDecodeJSON_TruncatedJSON_Returns422AndFalse(t *testing.T) {
	t.Parallel()

	// Valid JSON start that is cut off before the closing brace.
	w, r := makeRequest(t, `{"name":"dave"`, respond.MaxBodyBytes)

	_, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.False(t, ok)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — wrong type in field
// ─────────────────────────────────────────────────────────────────────────────

// TestDecodeJSON_WrongFieldType_Returns422 asserts that a type mismatch
// (e.g. a string where an int is expected) is treated as a decode error and
// results in a 422, not a silent zero value.
func TestDecodeJSON_WrongFieldType_Returns422(t *testing.T) {
	t.Parallel()

	w, r := makeRequest(t, `{"name":"eve","age":"not-a-number"}`, respond.MaxBodyBytes)

	_, ok := respond.DecodeJSON[decodeTarget](w, r)

	require.False(t, ok)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// ─────────────────────────────────────────────────────────────────────────────
// DecodeJSON — response body on error must not leak Go internals
// ─────────────────────────────────────────────────────────────────────────────

// TestDecodeJSON_ErrorMessage_DoesNotLeakGoTypeInfo verifies that the error
// message written to the client is a fixed string and does not contain Go
// struct names, type info, or file paths from the json package's error.
func TestDecodeJSON_ErrorMessage_DoesNotLeakGoTypeInfo(t *testing.T) {
	t.Parallel()

	w, r := makeRequest(t, `{"age":"wrong-type"}`, respond.MaxBodyBytes)
	_, _ = respond.DecodeJSON[decodeTarget](w, r)

	body := w.Body.String()

	// The message must be the generic fixed string, never a Go error detail.
	require.NotContains(t, body, "decodeTarget")
	require.NotContains(t, body, "unmarshal")
	require.NotContains(t, body, "cannot")
	require.Contains(t, body, "invalid JSON body")
}
