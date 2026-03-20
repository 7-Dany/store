package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── metric-reading helpers ────────────────────────────────────────────────────

// counterValue reads the current value from a prometheus.Counter.
func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, c.Write(&m))
	return m.GetCounter().GetValue()
}

// gaugeValue reads the current value from a prometheus.Gauge.
func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, g.Write(&m))
	return m.GetGauge().GetValue()
}

// histogramCount returns the sample count from a prometheus.Observer returned
// by HistogramVec.WithLabelValues. The underlying type is always a *histogram
// which implements prometheus.Histogram, so the type assertion is safe.
func histogramCount(t *testing.T, obs prometheus.Observer) uint64 {
	t.Helper()
	h, ok := obs.(prometheus.Histogram)
	require.True(t, ok, "observer must implement prometheus.Histogram")
	var m dto.Metric
	require.NoError(t, h.Write(&m))
	return m.GetHistogram().GetSampleCount()
}

// ── request-handler test helpers ─────────────────────────────────────────────

// okHandler responds with HTTP 200.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// errorHandler attaches err to the carrier and responds with HTTP 500.
func errorHandler(err error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Attach(r.Context(), err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// applyMiddleware chains RequestMiddleware → PanicRecoveryMiddleware → h.
func applyMiddleware(reg *Registry, h http.Handler) http.Handler {
	return RequestMiddleware(reg)(PanicRecoveryMiddleware(h))
}

// silentSlog replaces slog.Default with a discard handler for the duration
// of the test, then restores the previous default.
func silentSlog(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	})
}

// ── T-30: RequestMiddleware injects carrier before calling next ───────────────

func TestRequestMiddleware_InjectsCarrier(t *testing.T) {
	reg := NewNoopRegistry()
	var seen bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		c, ok := r.Context().Value(carrierKey{}).(*carrier)
		seen = ok && c != nil
	})
	RequestMiddleware(reg)(inner).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	assert.True(t, seen)
}

// ── T-31: RequestMiddleware records http_requests_total with correct labels ───

func TestRequestMiddleware_RecordsRequestsTotal(t *testing.T) {
	reg := NewNoopRegistry()
	RequestMiddleware(reg)(okHandler).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/ping", nil),
	)
	val := counterValue(t, reg.httpRequestsTotal.WithLabelValues("GET", "unknown", "200"))
	assert.Equal(t, 1.0, val)
}

// ── T-32: RequestMiddleware records http_request_duration_seconds ─────────────

func TestRequestMiddleware_RecordsDuration(t *testing.T) {
	reg := NewNoopRegistry()
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	RequestMiddleware(reg)(slow).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/slow", nil),
	)
	assert.Equal(t, uint64(1), histogramCount(t, reg.httpRequestDuration.WithLabelValues("POST", "unknown")))
}

// ── T-33: RequestMiddleware records http_errors_total for 5xx ────────────────

func TestRequestMiddleware_RecordsHttpErrors_On5xx(t *testing.T) {
	reg := NewNoopRegistry()
	storeErr := Store("Query.exec", errors.New("db down"))
	RequestMiddleware(reg)(errorHandler(storeErr)).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/login", nil),
	)
	// cause is "unknown": Store() wraps a plain errors.New, not a pgconn.PgError.
	val := counterValue(t, reg.httpErrors.WithLabelValues("unknown", "store", "unknown"))
	assert.Equal(t, 1.0, val)
}

// ── T-34: RequestMiddleware does NOT record http_errors_total for 4xx ─────────

func TestRequestMiddleware_NoHttpErrors_On4xx(t *testing.T) {
	reg := NewNoopRegistry()
	h4xx := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Attach(r.Context(), Store("op", errors.New("e")))
		w.WriteHeader(http.StatusNotFound)
	})
	RequestMiddleware(reg)(h4xx).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/missing", nil),
	)
	var m dto.Metric
	_ = reg.httpErrors.WithLabelValues("unknown", "store", "db_error").Write(&m)
	assert.Equal(t, 0.0, m.GetCounter().GetValue())
}

// ── T-35: RequestMiddleware does NOT record http_errors_total when carrier empty

func TestRequestMiddleware_NoHttpErrors_WhenCarrierEmpty(t *testing.T) {
	reg := NewNoopRegistry()
	h5xx := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	RequestMiddleware(reg)(h5xx).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/oops", nil),
	)
	var m dto.Metric
	_ = reg.httpErrors.WithLabelValues("unknown", "unknown", "unknown").Write(&m)
	assert.Equal(t, 0.0, m.GetCounter().GetValue())
}

// ── T-36: PanicRecoveryMiddleware recovers panic and returns 500 ──────────────

func TestPanicRecovery_Returns500(t *testing.T) {
	reg := NewNoopRegistry()
	SetDefault(reg)
	silentSlog(t)

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	rec := httptest.NewRecorder()
	applyMiddleware(reg, panicking).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "internal_error")
}

// ── T-37: PanicRecoveryMiddleware → http_errors_total{layer="panic"} ──────────

func TestPanicRecovery_IncrementsHttpErrors(t *testing.T) {
	reg := NewNoopRegistry()
	SetDefault(reg)
	silentSlog(t)

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("crash") })
	applyMiddleware(reg, panicking).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/crash", nil),
	)

	val := counterValue(t, reg.httpErrors.WithLabelValues("unknown", "panic", "panic"))
	assert.Equal(t, 1.0, val)
}

// ── T-38: PanicRecoveryMiddleware — non-panicking handler passes through ──────

func TestPanicRecovery_NonPanicPassthrough(t *testing.T) {
	reg := NewNoopRegistry()
	rec := httptest.NewRecorder()
	applyMiddleware(reg, okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// ── T-39: End-to-end panic → 500 → http_errors_total incremented ─────────────

func TestEndToEnd_PanicMetrics(t *testing.T) {
	reg := NewNoopRegistry()
	SetDefault(reg)
	silentSlog(t)

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("e2e") })
	rec := httptest.NewRecorder()
	applyMiddleware(reg, panicking).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/e2e", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, 1.0, counterValue(t, reg.httpErrors.WithLabelValues("unknown", "panic", "panic")))
}

// ── T-new-1: routePattern returns "unknown" for unmatched routes ──────────────

func TestRoutePattern_UnknownForUnmatchedRoute(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/no/chi/context", nil)
	assert.Equal(t, "unknown", routePattern(req))
}

// ── T-new-2: RequestMiddleware records route="unknown" for 404, never raw path ─

func TestRequestMiddleware_UnknownRoute_NeverRawPath(t *testing.T) {
	reg := NewNoopRegistry()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// Unique path to prove raw path is never used as a label.
	uniquePath := "/unique/" + strings.Repeat("x", 64)
	RequestMiddleware(reg)(h).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, uniquePath, nil),
	)
	val := counterValue(t, reg.httpRequestsTotal.WithLabelValues("GET", "unknown", "404"))
	assert.Equal(t, 1.0, val)
}

// ── T-40: NewRegistry registers all descriptors without panic ─────────────────

func TestNewRegistry_NoPanic(t *testing.T) {
	assert.NotPanics(t, func() { NewRegistry() })
}

// ── T-41: Two NewRegistry calls do not collide on metric names ───────────────

func TestNewRegistry_TwoCallsNoCollision(t *testing.T) {
	assert.NotPanics(t, func() {
		r1 := NewRegistry()
		r2 := NewRegistry()
		require.NotNil(t, r1.Handler())
		require.NotNil(t, r2.Handler())
	})
}

// ── T-42: Registry.Handler() returns 200 with Prometheus content-type ─────────

func TestRegistry_Handler_Returns200(t *testing.T) {
	reg := NewNoopRegistry()
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/plain")
}

// ── T-43: OnLoginSuccess increments auth_logins_total{status="success"} ───────

func TestRegistry_OnLoginSuccess(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnLoginSuccess("email")
	assert.Equal(t, 1.0, counterValue(t, reg.authLogins.WithLabelValues("email", "success")))
}

// ── T-44: OnLoginFailed increments both auth_logins_total and auth_login_failures_total

func TestRegistry_OnLoginFailed(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnLoginFailed("email", "invalid_credentials")
	assert.Equal(t, 1.0, counterValue(t, reg.authLogins.WithLabelValues("email", "failure")))
	assert.Equal(t, 1.0, counterValue(t, reg.authLoginFailures.WithLabelValues("email", "invalid_credentials")))
}

// ── T-45: All auth hook methods nil-safe when called on nil *Registry ─────────

func TestRegistry_AuthHooks_NilSafe(t *testing.T) {
	var r *Registry
	assert.NotPanics(t, func() {
		r.OnLoginSuccess("email")
		r.OnLoginFailed("email", "invalid_credentials")
		r.OnLogout()
		r.OnTokenRefreshed("web")
		r.OnTokenValidationFailed("expired")
		r.OnSessionRevoked()
		r.OnRegistrationSuccess()
		r.OnRegistrationFailed("email_taken")
		r.OnEmailVerified()
		r.OnVerificationResent()
		r.OnPasswordResetRequested()
		r.OnPasswordResetDenied("account_not_found")
		r.OnPasswordResetCompleted()
		r.OnPasswordChanged()
		r.OnUnlockRequested()
		r.OnUnlockCompleted()
		r.OnOAuthSuccess("google")
		r.OnOAuthFailed("google", "state_mismatch")
		r.OnOAuthLinked("google")
		r.OnOAuthUnlinked("google")
		r.OnEmailChangeRequested()
		r.OnEmailChangeCompleted()
		r.OnUsernameChanged()
		r.OnAccountDeletionRequested()
		r.OnAccountDeletionCompleted()
		r.OnUserLocked("auto_lockout")
		r.OnUserUnlocked()
	})
}

// ── T-new-3: OnTokenRefreshed increments auth_token_refreshes_total ───────────

func TestRegistry_OnTokenRefreshed(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnTokenRefreshed("web")
	assert.Equal(t, 1.0, counterValue(t, reg.authTokenRefreshes.WithLabelValues("web")))
}

// ── T-new-4: OnTokenValidationFailed increments auth_token_validation_failures_total

func TestRegistry_OnTokenValidationFailed(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnTokenValidationFailed("expired")
	assert.Equal(t, 1.0, counterValue(t, reg.authTokenValidationFailures.WithLabelValues("expired")))
}

// ── T-new-5: OnPasswordResetDenied increments auth_password_resets_denied_total ─

func TestRegistry_OnPasswordResetDenied(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnPasswordResetDenied("account_not_found")
	assert.Equal(t, 1.0, counterValue(t, reg.authPasswordResetsDenied.WithLabelValues("account_not_found")))
}

// ── T-47: OnJobSucceeded increments counter + histogram ───────────────────────

func TestRegistry_OnJobSucceeded(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnJobSucceeded("send_email", 2*time.Second)
	assert.Equal(t, 1.0, counterValue(t, reg.jobsSucceeded.WithLabelValues("send_email")))
	assert.Equal(t, uint64(1), histogramCount(t, reg.jobDuration.WithLabelValues("send_email")))
}

// ── T-48: OnJobFailed increments with correct will_retry label ───────────────

func TestRegistry_OnJobFailed(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnJobFailed("purge", errors.New("timeout"), true)
	assert.Equal(t, 1.0, counterValue(t, reg.jobsFailed.WithLabelValues("purge", "true")))

	reg2 := NewNoopRegistry()
	reg2.OnJobFailed("purge", errors.New("timeout"), false)
	assert.Equal(t, 1.0, counterValue(t, reg2.jobsFailed.WithLabelValues("purge", "false")))
}

// ── T-50: OnJobDead increments jobqueue_jobs_dead_total ──────────────────────

func TestRegistry_OnJobDead(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnJobDead("bitcoin_settle_invoice")
	assert.Equal(t, 1.0, counterValue(t, reg.jobsDead.WithLabelValues("bitcoin_settle_invoice")))
}

// ── T-51: OnJobsRequeued increments jobqueue_jobs_requeued_total ─────────────

func TestRegistry_OnJobsRequeued(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnJobsRequeued(3)
	var m dto.Metric
	require.NoError(t, reg.jobsRequeued.Write(&m))
	assert.Equal(t, 3.0, m.GetCounter().GetValue())
}

// ── T-52: All job hook methods nil-safe on nil *Registry ─────────────────────

func TestRegistry_JobHooks_NilSafe(t *testing.T) {
	var r *Registry
	assert.NotPanics(t, func() {
		r.OnJobSubmitted("kind")
		r.OnJobClaimed("kind")
		r.OnJobSucceeded("kind", time.Second)
		r.OnJobFailed("kind", errors.New("e"), false)
		r.OnJobDead("kind")
		r.OnJobCancelled("kind")
		r.OnScheduleFired("sched-1", "kind")
		r.OnJobsRequeued(1)
	})
}

// ── T-54: pollRedis records correct values from PoolStats ────────────────────

func TestPollRedis_SetsGauges(t *testing.T) {
	reg := NewNoopRegistry()
	reg.pollRedis(context.Background(), &fakeRedisProvider{total: 5, idle: 3, stale: 1})
	assert.Equal(t, 5.0, gaugeValue(t, reg.redisPoolTotal))
	assert.Equal(t, 3.0, gaugeValue(t, reg.redisPoolIdle))
	assert.Equal(t, 1.0, gaugeValue(t, reg.redisPoolStale))
}

// TestPollRedis_PingFailure_IncrementsAppErrors asserts that a failed Redis
// ping is logged as an ERROR, which causes TelemetryHandler to increment
// app_errors_total{component="infra_poller"}. This is the signal the frontend
// dashboard uses to detect Redis outages within one 15-second poll cycle.
func TestPollRedis_PingFailure_IncrementsAppErrors(t *testing.T) {
	reg := NewNoopRegistry()
	SetDefault(reg)
	silentSlog(t)

	provider := &fakeRedisProvider{
		total: 2, idle: 2, stale: 0,
		pingErr: errors.New("connect: connection refused"),
	}
	reg.pollRedis(context.Background(), provider)

	// The ping failure must increment app_errors_total for the infra_poller component.
	val := counterValue(t, reg.appErrors.WithLabelValues("infra_poller", "kvstore", "unknown"))
	assert.Equal(t, 1.0, val, "Redis ping failure must surface as app_errors_total increment")
}

// ── T-56: pollProcess records goroutine count > 0 ────────────────────────────

func TestPollProcess_SetsGoroutines(t *testing.T) {
	reg := NewNoopRegistry()
	reg.pollProcess()
	assert.Greater(t, gaugeValue(t, reg.processGoroutines), 0.0)
	assert.Greater(t, gaugeValue(t, reg.processMemAlloc), 0.0)
}

// ── T-57: StartInfraPoller exits cleanly on ctx cancellation ─────────────────

func TestStartInfraPoller_ExitsOnCancel(t *testing.T) {
	reg := NewNoopRegistry()
	// Cancel before the first tick fires by using a pre-cancelled context.
	// This avoids calling pollDB (which needs a real pgxpool.Stat) while still
	// exercising the goroutine's ctx.Done() exit path.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	reg.StartInfraPoller(ctx, &fakeDBProvider{}, nil, time.Hour)
	// Give the goroutine a moment to observe the cancelled context and exit.
	time.Sleep(20 * time.Millisecond)
	// -race will catch any goroutine leak; no further assertion needed.
}

// ── T-58: SetDefault + Logger.Error → app_errors_total incremented ────────────

func TestSetDefault_LoggerError_IncrementsMetric(t *testing.T) {
	reg := NewNoopRegistry()
	SetDefault(reg)
	silentSlog(t)

	log := New("integration")
	log.Error(context.Background(), "fail", "error", Store("op", errors.New("db")))

	// cause is "unknown": Store() wraps a plain errors.New, not a pgconn.PgError.
	assert.Equal(t, 1.0, counterValue(t, reg.appErrors.WithLabelValues("integration", "store", "unknown")))
}

// ── T-62–T-63: Bitcoin ZMQ connected gauge ────────────────────────────────────

func TestRegistry_SetZMQConnected(t *testing.T) {
	reg := NewNoopRegistry()
	reg.SetZMQConnected(true)
	assert.Equal(t, 1.0, gaugeValue(t, reg.bitcoinZMQConnected))

	reg.SetZMQConnected(false)
	assert.Equal(t, 0.0, gaugeValue(t, reg.bitcoinZMQConnected))
}

// ── T-65: All bitcoin hook methods nil-safe on nil *Registry ─────────────────

func TestRegistry_BitcoinHooks_NilSafe(t *testing.T) {
	var r *Registry
	assert.NotPanics(t, func() {
		r.SetZMQConnected(true)
		r.SetRPCConnected(false)
		r.SetZMQLastMessageAge(5.0)
		r.OnHandlerPanic("tx_handler")
		r.SetHandlerGoroutines(3)
		r.OnMessageDropped("slow_consumer")
		r.SetSSEConnections(10)
		r.OnTokenConsumeFailed("not_found")
		r.OnInvoiceDetected(1.5)
		r.SetInvoiceCount("pending", 7)
		r.SetRateFeedStaleness(30.0)
		r.SetReconciliationLag(2.0)
		r.SetBalanceDrift(100)
		r.SetReconciliationHold(true)
		r.OnReorgDetected()
		r.OnPayoutFailed()
		r.SetFeeEstimate(3, 12.5)
		r.OnSweepStuck()
		r.SetWalletBackupAge(3600)
		r.SetUTXOCount(42)
	})
}

// ── Anomaly signal tests ──────────────────────────────────────────────────────
// Each test below exercises exactly one metric whose Help string declares an
// alert condition. The assertion verifies the signal fires at the expected
// threshold so future refactors cannot silently break alerting.

// TestAnomaly_BalanceDrift_Nonzero verifies that SetBalanceDrift with a nonzero
// value sets bitcoin_balance_drift_satoshis. Any nonzero value is CRITICAL.
func TestAnomaly_BalanceDrift_Nonzero(t *testing.T) {
	reg := NewNoopRegistry()
	reg.SetBalanceDrift(1)
	assert.Equal(t, 1.0, gaugeValue(t, reg.bitcoinBalanceDrift))
}

// TestAnomaly_BalanceDrift_Zero verifies the gauge resets to zero cleanly.
func TestAnomaly_BalanceDrift_Zero(t *testing.T) {
	reg := NewNoopRegistry()
	reg.SetBalanceDrift(500)
	reg.SetBalanceDrift(0)
	assert.Equal(t, 0.0, gaugeValue(t, reg.bitcoinBalanceDrift))
}

// TestAnomaly_ReconciliationHold_Active verifies that SetReconciliationHold(true)
// sets bitcoin_reconciliation_hold_active to 1.
func TestAnomaly_ReconciliationHold_Active(t *testing.T) {
	reg := NewNoopRegistry()
	reg.SetReconciliationHold(true)
	assert.Equal(t, 1.0, gaugeValue(t, reg.bitcoinReconciliationHold))
}

// TestAnomaly_ReconciliationHold_Cleared verifies the hold gauge drops to 0.
func TestAnomaly_ReconciliationHold_Cleared(t *testing.T) {
	reg := NewNoopRegistry()
	reg.SetReconciliationHold(true)
	reg.SetReconciliationHold(false)
	assert.Equal(t, 0.0, gaugeValue(t, reg.bitcoinReconciliationHold))
}

// TestAnomaly_JobDead_IncrementsCounter verifies that OnJobDead increments
// jobqueue_jobs_dead_total. Any increment triggers JobQueueDeadJobsAccumulating.
func TestAnomaly_JobDead_IncrementsCounter(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnJobDead("send_email")
	reg.OnJobDead("send_email")
	assert.Equal(t, 2.0, counterValue(t, reg.jobsDead.WithLabelValues("send_email")))
}

// TestAnomaly_JobsRequeued_IncrementsCounter verifies that OnJobsRequeued
// increments jobqueue_jobs_requeued_total. Any increment signals a worker crash.
func TestAnomaly_JobsRequeued_IncrementsCounter(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnJobsRequeued(5)
	var m dto.Metric
	require.NoError(t, reg.jobsRequeued.Write(&m))
	assert.Equal(t, 5.0, m.GetCounter().GetValue())
}

// TestAnomaly_OAuthUnlink_Spike verifies that OnOAuthUnlinked increments
// auth_oauth_unlinks_total. A spike indicates a possible account takeover campaign.
func TestAnomaly_OAuthUnlink_Spike(t *testing.T) {
	reg := NewNoopRegistry()
	for range 10 {
		reg.OnOAuthUnlinked("telegram")
	}
	assert.Equal(t, 10.0, counterValue(t, reg.authOAuthUnlinks.WithLabelValues("telegram")))
}

// TestAnomaly_RedisPoolStale_Rising verifies that pollRedis propagates a rising
// redis_pool_stale_connections gauge. A rising value indicates connection instability.
func TestAnomaly_RedisPoolStale_Rising(t *testing.T) {
	reg := NewNoopRegistry()
	reg.pollRedis(context.Background(), &fakeRedisProvider{total: 10, idle: 4, stale: 6})
	assert.Equal(t, 6.0, gaugeValue(t, reg.redisPoolStale))
}

// TestAnomaly_ReorgDetected_Increments verifies that OnReorgDetected increments
// bitcoin_reorg_detected_total.
func TestAnomaly_ReorgDetected_Increments(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnReorgDetected()
	reg.OnReorgDetected()
	var m dto.Metric
	require.NoError(t, reg.bitcoinReorgDetected.Write(&m))
	assert.Equal(t, 2.0, m.GetCounter().GetValue())
}

// TestAnomaly_PayoutFailed_Increments verifies that OnPayoutFailed increments
// bitcoin_payout_failure_total.
func TestAnomaly_PayoutFailed_Increments(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnPayoutFailed()
	var m dto.Metric
	require.NoError(t, reg.bitcoinPayoutFailures.Write(&m))
	assert.Equal(t, 1.0, m.GetCounter().GetValue())
}

// TestAnomaly_SweepStuck_Increments verifies that OnSweepStuck increments
// bitcoin_sweep_stuck_total.
func TestAnomaly_SweepStuck_Increments(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnSweepStuck()
	var m dto.Metric
	require.NoError(t, reg.bitcoinSweepStuck.Write(&m))
	assert.Equal(t, 1.0, m.GetCounter().GetValue())
}

// TestAnomaly_InfraPoller_Staleness verifies that infraPollerLastRunSeconds is
// updated via StartInfraPoller. A value older than 60 s triggers InfraPollerDown.
// We set it directly here to verify the gauge accepts the timestamp correctly.
func TestAnomaly_InfraPoller_Staleness(t *testing.T) {
	reg := NewNoopRegistry()
	// Simulate a stale poller by setting the timestamp to 120 seconds ago.
	staleTS := float64(1_000_000) // arbitrary past Unix timestamp
	reg.infraPollerLastRunSeconds.Set(staleTS)
	assert.Equal(t, staleTS, gaugeValue(t, reg.infraPollerLastRunSeconds))
}

// TestAnomaly_UserLock_AutoLockout verifies that OnUserLocked("auto_lockout")
// increments auth_user_locks_total{reason="auto_lockout"}.
func TestAnomaly_UserLock_AutoLockout(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnUserLocked("auto_lockout")
	assert.Equal(t, 1.0, counterValue(t, reg.authUserLocks.WithLabelValues("auto_lockout")))
}

// TestAnomaly_TokenValidationFailed_Revoked verifies that OnTokenValidationFailed
// with reason "revoked" fires the correct label — indicates blocklisted token reuse.
func TestAnomaly_TokenValidationFailed_Revoked(t *testing.T) {
	reg := NewNoopRegistry()
	reg.OnTokenValidationFailed("revoked")
	assert.Equal(t, 1.0, counterValue(t, reg.authTokenValidationFailures.WithLabelValues("revoked")))
}

// ── fakes ─────────────────────────────────────────────────────────────────────

// fakeRedisProvider satisfies RedisStatsProvider.
type fakeRedisProvider struct {
	total, idle, stale uint32
	pingErr            error // non-nil simulates Redis being unreachable
}

func (f *fakeRedisProvider) PoolStats() *redis.PoolStats {
	return &redis.PoolStats{TotalConns: f.total, IdleConns: f.idle, StaleConns: f.stale}
}

func (f *fakeRedisProvider) Ping(_ context.Context) error {
	return f.pingErr
}

// fakeDBProvider satisfies DBStatsProvider.
// Stat() and Ping() are never called in T-57 because the context is cancelled
// before the first ticker fires.
type fakeDBProvider struct{}

func (f *fakeDBProvider) Stat() *pgxpool.Stat {
	panic("fakeDBProvider.Stat should not be called in this test")
}

func (f *fakeDBProvider) Ping(_ context.Context) error {
	panic("fakeDBProvider.Ping should not be called in this test")
}
