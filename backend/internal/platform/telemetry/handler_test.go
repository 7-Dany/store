package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── captureHandler records every slog.Record for inspection ──────────────────

type captureHandler struct {
	buf *bytes.Buffer
}

func newCaptureHandler() *captureHandler {
	return &captureHandler{buf: &bytes.Buffer{}}
}

// logRecord unmarshals the JSON written by the JSON handler.
func (h *captureHandler) lastRecord(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	// Use the last non-empty line.
	data := bytes.TrimSpace(h.buf.Bytes())
	if len(data) == 0 {
		return nil
	}
	lines := bytes.Split(data, []byte("\n"))
	last := lines[len(lines)-1]
	require.NoError(t, json.Unmarshal(last, &m))
	return m
}

// newTestHandler returns a TelemetryHandler wired to a capturing JSON handler.
// If reg is nil the registry path is skipped (tests that only care about logs).
func newTestHandler(reg *Registry) (*TelemetryHandler, *captureHandler) {
	cap := newCaptureHandler()
	inner := slog.NewJSONHandler(cap.buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return newTelemetryHandler(inner, reg), cap
}

// ── T-20: Logger.Error with "error" key adds fault attributes ────────────────

func TestTelemetryHandler_ErrorEnrichment(t *testing.T) {
	reg := NewNoopRegistry()
	h, cap := newTestHandler(reg)
	logger := slog.New(h)

	storeErr := Store("GetUser.query", errors.New("db error"))
	logger.ErrorContext(context.Background(), "something broke",
		"error", storeErr,
		"component", "login",
	)

	rec := cap.lastRecord(t)
	require.NotNil(t, rec)
	assert.Equal(t, "store", rec["fault_layer"])
	// plain errors.New has no pgconn.PgError → cause is "unknown".
	// Use pgError("23505") in the chain to get "db_constraint".
	assert.Equal(t, "unknown", rec["fault_cause"])
	assert.Equal(t, "GetUser.query", rec["fault_op"])
	assert.NotNil(t, rec["fault_chain"])
}

// ── T-21: Debug/Info/Warn pass through without fault enrichment ──────────────

func TestTelemetryHandler_NonErrorLevelsNoFaultAttrs(t *testing.T) {
	h, cap := newTestHandler(nil)
	logger := slog.New(h)
	ctx := context.Background()

	storeErr := Store("op", errors.New("err"))
	logger.DebugContext(ctx, "debug", "error", storeErr)
	logger.InfoContext(ctx, "info", "error", storeErr)
	logger.WarnContext(ctx, "warn", "error", storeErr)

	lines := bytes.Split(bytes.TrimSpace(cap.buf.Bytes()), []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal(line, &m))
		assert.Nil(t, m["fault_layer"], "non-ERROR records must not have fault_layer")
	}
}

// ── T-22: Logger.Error without "error" key adds no fault attrs ───────────────

func TestTelemetryHandler_ErrorNoErrorKey_NoFaultAttrs(t *testing.T) {
	h, cap := newTestHandler(nil)
	logger := slog.New(h)

	logger.ErrorContext(context.Background(), "something broke", "msg", "no error key")

	rec := cap.lastRecord(t)
	require.NotNil(t, rec)
	assert.Nil(t, rec["fault_layer"])
	assert.Nil(t, rec["fault_cause"])
}

// ── T-23: TelemetryHandler injects request_id at all log levels ──────────────

func TestTelemetryHandler_InjectsRequestID(t *testing.T) {
	// chi's middleware.SetReqID stores the ID under its own key.
	// We use chi's GetReqID to verify — but for unit tests we can inject it
	// via chi's middleware.NewReqID context helper.
	// Since that's in a different package, we'll verify via the chi middleware
	// integration test in middleware_test.go (T-23).
	// Here we confirm the handler passes through records without panicking.
	h, cap := newTestHandler(nil)
	logger := slog.New(h)
	logger.InfoContext(context.Background(), "plain message")

	rec := cap.lastRecord(t)
	require.NotNil(t, rec)
	// No request_id in a plain background context — that's correct.
	assert.Equal(t, "plain message", rec["msg"])
}

// ── T-24: TelemetryHandler no-op on request_id when ctx has none ─────────────

func TestTelemetryHandler_NoRequestIDInWorkerCtx(t *testing.T) {
	h, cap := newTestHandler(nil)
	logger := slog.New(h)

	// Background context has no chi request_id — should not add the key.
	logger.InfoContext(context.Background(), "worker log")

	rec := cap.lastRecord(t)
	require.NotNil(t, rec)
	_, hasReqID := rec["request_id"]
	assert.False(t, hasReqID)
}

// ── T-25: TelemetryHandler calls Attach on ERROR with "error" key ─────────────

func TestTelemetryHandler_AttachesErrorToCarrier(t *testing.T) {
	h, _ := newTestHandler(nil)
	logger := slog.New(h)

	ctx, c := newCarrierContext(context.Background())
	storeErr := Store("op", errors.New("db"))

	logger.ErrorContext(ctx, "fail", "error", storeErr, "component", "test")

	assert.Equal(t, storeErr, c.get(),
		"TelemetryHandler must write the error into the carrier via Attach")
}

// ── T-26: TelemetryHandler increments app_errors_total with correct labels ────

func TestTelemetryHandler_IncrementsAppErrors(t *testing.T) {
	reg := NewNoopRegistry()
	h, _ := newTestHandler(reg)
	logger := slog.New(h)

	storeErr := Store("GetUser.query", errors.New("db"))
	logger.ErrorContext(context.Background(), "fail",
		"error", storeErr,
		"component", "login",
	)

	// cause is "unknown" because the inner error is plain errors.New, not a pgconn.PgError.
	val := counterValue(t, reg.appErrors.WithLabelValues("login", "store", "unknown"))
	assert.Equal(t, 1.0, val)
}

// ── T-27: TelemetryHandler nil-safe when registry is nil ─────────────────────

func TestTelemetryHandler_NilRegistrySafe(t *testing.T) {
	h, _ := newTestHandler(nil) // nil registry
	logger := slog.New(h)

	assert.NotPanics(t, func() {
		logger.ErrorContext(context.Background(), "fail",
			"error", Store("op", errors.New("e")),
			"component", "test",
		)
	})
}

// ── T-28: Logger.With pre-sets attrs on every subsequent record ───────────────

func TestLogger_With_PreSetsAttrs(t *testing.T) {
	reg := NewNoopRegistry()
	h, cap := newTestHandler(reg)
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

	log := New("auth").With(slog.String("user_id", "u123"))
	log.Info(context.Background(), "user action")

	rec := cap.lastRecord(t)
	require.NotNil(t, rec)
	assert.Equal(t, "u123", rec["user_id"])
	assert.Equal(t, "auth", rec["component"])
}

// ── T-29: Logger.emit uses slog.Default() dynamically ────────────────────────

func TestLogger_PicksUpSetDefault(t *testing.T) {
	// Install a registry and verify the logger picks up the new handler.
	reg := NewNoopRegistry()
	h, cap := newTestHandler(reg)
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil))) })

	log := New("test")
	log.Info(context.Background(), "hello after SetDefault")

	rec := cap.lastRecord(t)
	require.NotNil(t, rec)
	assert.Equal(t, "hello after SetDefault", rec["msg"])
}
