package telemetry

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds all Prometheus metric descriptors for the store backend.
//
// A single Registry instance is created in server.New and shared across every
// domain. Domain code never touches the Prometheus registry directly — it
// interacts via the typed hook methods ([OnLoginSuccess], [OnJobDead], etc.)
// or via the structural interfaces it satisfies ([jobqueue.MetricsRecorder],
// authshared.AuthRecorder, bitcoinshared.BitcoinRecorder).
//
// All hook methods are nil-safe: calling them on a nil *Registry is a no-op.
// This allows tests to pass nil without panics.
type Registry struct {
	promReg *prometheus.Registry // scoped; never uses prometheus.DefaultRegisterer

	// ── Family 1 — HTTP ───────────────────────────────────────────────────
	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	httpRequestsInFlight prometheus.Gauge
	httpErrors           *prometheus.CounterVec

	// ── Family 2 — Application Errors ────────────────────────────────────
	appErrors *prometheus.CounterVec

	// ── Family 3 — Auth ───────────────────────────────────────────────────
	authLogins                  *prometheus.CounterVec
	authLoginFailures           *prometheus.CounterVec
	authRegistrations           *prometheus.CounterVec
	authRegistrationFailures    *prometheus.CounterVec
	authTokenRefreshes          *prometheus.CounterVec
	authTokenValidationFailures *prometheus.CounterVec
	authLogouts                 prometheus.Counter
	authSessionRevocations      prometheus.Counter
	authEmailVerifications      prometheus.Counter
	authVerificationResends     prometheus.Counter
	authPasswordResets          *prometheus.CounterVec
	authPasswordResetsDenied    *prometheus.CounterVec
	authPasswordChanges         prometheus.Counter
	authAccountUnlocks          *prometheus.CounterVec
	authOAuth                   *prometheus.CounterVec
	authOAuthFailures           *prometheus.CounterVec
	authOAuthLinks              *prometheus.CounterVec
	authOAuthUnlinks            *prometheus.CounterVec
	authEmailChanges            *prometheus.CounterVec
	authUsernameChanges         prometheus.Counter
	authAccountDeletions        *prometheus.CounterVec
	authUserLocks               *prometheus.CounterVec
	authUserUnlocks             prometheus.Counter

	// ── Family 4 — Infrastructure ─────────────────────────────────────────
	dbPoolTotal               prometheus.Gauge
	dbPoolIdle                prometheus.Gauge
	dbPoolAcquired            prometheus.Gauge
	dbPoolMax                 prometheus.Gauge
	dbUp                      prometheus.Gauge // 1 = last ping succeeded, 0 = failed; flips within one poller tick
	redisPoolTotal            prometheus.Gauge
	redisPoolIdle             prometheus.Gauge
	redisPoolStale            prometheus.Gauge
	redisUp                   prometheus.Gauge // 1 = last ping succeeded, 0 = failed; flips within one poller tick
	processGoroutines         prometheus.Gauge
	processMemAlloc           prometheus.Gauge
	infraPollerLastRunSeconds prometheus.Gauge

	// ── Family 5 — Job Queue ──────────────────────────────────────────────
	jobsSubmitted  *prometheus.CounterVec
	jobsClaimed    *prometheus.CounterVec
	jobsSucceeded  *prometheus.CounterVec
	jobsFailed     *prometheus.CounterVec
	jobsDead       *prometheus.CounterVec
	jobsCancelled  *prometheus.CounterVec
	jobDuration    *prometheus.HistogramVec
	schedulesFired *prometheus.CounterVec
	jobsRequeued   prometheus.Counter

	// ── Family 6 — Bitcoin ────────────────────────────────────────────────
	bitcoinZMQConnected         prometheus.Gauge
	bitcoinRPCConnected         prometheus.Gauge
	bitcoinZMQLastMessageAge    prometheus.Gauge
	bitcoinHandlerPanics        *prometheus.CounterVec
	bitcoinHandlerGoroutines    prometheus.Gauge
	bitcoinDroppedMessages      *prometheus.CounterVec
	bitcoinSSEConnections       prometheus.Gauge
	bitcoinTokenConsumeFailures *prometheus.CounterVec
	bitcoinBalanceDrift         prometheus.Gauge
	bitcoinReconciliationHold   prometheus.Gauge
	bitcoinReorgDetected        prometheus.Counter
	bitcoinPayoutFailures       prometheus.Counter
	bitcoinFeeEstimate          *prometheus.GaugeVec
	bitcoinSweepStuck           prometheus.Counter
	bitcoinWalletBackupAge      prometheus.Gauge
	bitcoinUTXOCount            prometheus.Gauge
	bitcoinInvoiceState         *prometheus.GaugeVec
	bitcoinInvoiceDetection     prometheus.Histogram
	bitcoinRateFeedStaleness    prometheus.Gauge
	bitcoinReconciliationLag    prometheus.Gauge
}

// NewRegistry constructs a Registry, registers all metric descriptors on a
// fresh scoped Prometheus registry, and returns it.
//
// Using prometheus.NewRegistry() (not prometheus.DefaultRegisterer) means two
// NewRegistry() calls in tests do not collide on metric names.
func NewRegistry() *Registry {
	reg := prometheus.NewRegistry()
	r := &Registry{promReg: reg}

	// HTTP request duration buckets: 5ms … 10s (11 buckets)
	httpBuckets := []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

	// Job queue duration buckets: 1s … 600s (6 buckets)
	jobBuckets := []float64{1, 5, 30, 60, 300, 600}

	// ── Family 1 — HTTP ───────────────────────────────────────────────────
	r.httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests by method, route pattern, and status code.",
	}, []string{"method", "route", "status"})

	r.httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency histogram by method and route pattern.",
		Buckets: httpBuckets,
	}, []string{"method", "route"})

	r.httpRequestsInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Current number of HTTP requests being processed.",
	})

	r.httpErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_errors_total",
		Help: "Total HTTP 5xx responses by route, fault layer, and fault cause.",
	}, []string{"route", "layer", "cause"})

	// ── Family 2 — Application Errors ────────────────────────────────────
	r.appErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "app_errors_total",
		Help: "Total application errors by component, fault layer, and cause. Fires on every log.Error().",
	}, []string{"component", "layer", "cause"})

	// ── Family 3 — Auth ───────────────────────────────────────────────────
	r.authLogins = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_logins_total",
		Help: "Total login attempts by provider and status (success|failure).",
	}, []string{"provider", "status"})

	r.authLoginFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_login_failures_total",
		Help: "Total login failures by provider and reason.",
	}, []string{"provider", "reason"})

	r.authRegistrations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_registrations_total",
		Help: "Total registration attempts by status (success|failure).",
	}, []string{"status"})

	r.authRegistrationFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_registration_failures_total",
		Help: "Total registration failures by reason.",
	}, []string{"reason"})

	r.authTokenRefreshes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_token_refreshes_total",
		Help: "Total token refreshes by client_type (web|mobile|api|unknown).",
	}, []string{"client_type"})

	r.authTokenValidationFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_token_validation_failures_total",
		Help: "Total JWT validation failures by reason (expired|invalid_signature|malformed|revoked|unknown).",
	}, []string{"reason"})

	r.authLogouts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_logouts_total",
		Help: "Total logout events.",
	})

	r.authSessionRevocations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_session_revocations_total",
		Help: "Total explicit session revocations.",
	})

	r.authEmailVerifications = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_email_verifications_total",
		Help: "Total successful email verification events.",
	})

	r.authVerificationResends = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_verification_resends_total",
		Help: "Total verification email resend requests.",
	})

	r.authPasswordResets = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_password_resets_total",
		Help: "Total password reset events by event (requested|completed).",
	}, []string{"event"})

	r.authPasswordResetsDenied = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_password_resets_denied_total",
		Help: "Total denied password resets by reason (account_not_found|rate_limited).",
	}, []string{"reason"})

	r.authPasswordChanges = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_password_changes_total",
		Help: "Total password changes by authenticated users.",
	})

	r.authAccountUnlocks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_account_unlocks_total",
		Help: "Total account unlock events by event (requested|completed).",
	}, []string{"event"})

	r.authOAuth = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_oauth_total",
		Help: "Total OAuth flow attempts by provider and status (success|failure). Provider normalised via allowlist.",
	}, []string{"provider", "status"})

	r.authOAuthFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_oauth_failures_total",
		Help: "Total OAuth failures by provider and reason. Provider normalised via allowlist.",
	}, []string{"provider", "reason"})

	r.authOAuthLinks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_oauth_links_total",
		Help: "Total OAuth provider link events by provider.",
	}, []string{"provider"})

	r.authOAuthUnlinks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_oauth_unlinks_total",
		Help: "Total OAuth provider unlink events by provider. Spike indicates possible account takeover.",
	}, []string{"provider"})

	r.authEmailChanges = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_email_changes_total",
		Help: "Total email change events by event (requested|completed).",
	}, []string{"event"})

	r.authUsernameChanges = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_username_changes_total",
		Help: "Total username change events.",
	})

	r.authAccountDeletions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_account_deletions_total",
		Help: "Total account deletion events by event (requested|completed).",
	}, []string{"event"})

	r.authUserLocks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_user_locks_total",
		Help: "Total account lock events by reason (admin_action|auto_lockout).",
	}, []string{"reason"})

	r.authUserUnlocks = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_user_unlocks_total",
		Help: "Total account unlock completions.",
	})

	// ── Family 4 — Infrastructure ─────────────────────────────────────────
	r.dbPoolTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "db_pool_total_connections",
		Help: "Total pgx pool connections (idle + acquired).",
	})
	r.dbPoolIdle = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "db_pool_idle_connections",
		Help: "Number of idle pgx pool connections.",
	})
	r.dbPoolAcquired = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "db_pool_acquired_connections",
		Help: "Number of currently acquired pgx pool connections.",
	})
	r.dbPoolMax = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "db_pool_max_connections",
		Help: "Maximum allowed pgx pool connections (DBMaxConns config).",
	})
	r.dbUp = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "db_up",
		Help: "1 when the last InfraPoller ping to the database succeeded, 0 when it failed. Flips within one poll cycle (15s). Use as the primary real-time DB health signal.",
	})
	r.redisPoolTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_pool_total_connections",
		Help: "Total Redis pool connections.",
	})
	r.redisPoolIdle = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_pool_idle_connections",
		Help: "Number of idle Redis pool connections.",
	})
	r.redisPoolStale = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_pool_stale_connections",
		Help: "Number of stale Redis pool connections found broken on checkout. Rising count indicates instability.",
	})
	r.redisUp = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_up",
		Help: "1 when the last InfraPoller ping to Redis succeeded, 0 when it failed. Flips within one poll cycle (15s). Use this as the primary real-time Redis health signal.",
	})
	r.processGoroutines = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "process_goroutines",
		Help: "Current goroutine count. Sustained growth indicates a goroutine leak.",
	})
	r.processMemAlloc = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "process_memory_alloc_bytes",
		Help: "Bytes of allocated heap memory (runtime.MemStats.Alloc).",
	})
	r.infraPollerLastRunSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "infra_poller_last_run_timestamp_seconds",
		Help: "Unix timestamp of the last successful InfraPoller poll. Stale > 60s triggers InfraPollerDown alert.",
	})

	// ── Family 5 — Job Queue ──────────────────────────────────────────────
	r.jobsSubmitted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobqueue_jobs_submitted_total",
		Help: "Total jobs submitted by kind.",
	}, []string{"kind"})

	r.jobsClaimed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobqueue_jobs_claimed_total",
		Help: "Total jobs claimed by a worker by kind.",
	}, []string{"kind"})

	r.jobsSucceeded = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobqueue_jobs_succeeded_total",
		Help: "Total jobs completed successfully by kind.",
	}, []string{"kind"})

	r.jobsFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobqueue_jobs_failed_total",
		Help: "Total job execution failures by kind and whether the job will be retried.",
	}, []string{"kind", "will_retry"})

	r.jobsDead = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobqueue_jobs_dead_total",
		Help: "Total jobs dead-lettered (all retries exhausted) by kind. Any increment triggers an alert.",
	}, []string{"kind"})

	r.jobsCancelled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobqueue_jobs_cancelled_total",
		Help: "Total jobs explicitly cancelled by kind.",
	}, []string{"kind"})

	r.jobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "jobqueue_job_duration_seconds",
		Help:    "Job execution duration histogram by kind.",
		Buckets: jobBuckets,
	}, []string{"kind"})

	r.schedulesFired = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobqueue_schedules_fired_total",
		Help: "Total scheduled triggers fired by kind and schedule_id.",
	}, []string{"kind", "schedule_id"})

	r.jobsRequeued = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "jobqueue_jobs_requeued_total",
		Help: "Total jobs requeued by the stall detector. Any increment means a worker crashed or timed out.",
	})

	// ── Family 6 — Bitcoin ────────────────────────────────────────────────
	r.bitcoinZMQConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_zmq_connected",
		Help: "1 when the ZMQ subscriber is connected to the Bitcoin node, 0 otherwise.",
	})
	r.bitcoinRPCConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_rpc_connected",
		Help: "1 when the Bitcoin RPC client is reachable, 0 otherwise.",
	})
	r.bitcoinZMQLastMessageAge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_zmq_last_message_age_seconds",
		Help: "Seconds since the last ZMQ message was received.",
	})
	r.bitcoinHandlerPanics = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bitcoin_handler_panics_total",
		Help: "Total recovered panics in Bitcoin ZMQ event handlers by handler name.",
	}, []string{"handler"})
	r.bitcoinHandlerGoroutines = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_handler_goroutines_inflight",
		Help: "Number of in-flight ZMQ event handler goroutines.",
	})
	r.bitcoinDroppedMessages = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dropped_zmq_messages_total",
		Help: "Total ZMQ messages dropped by reason.",
	}, []string{"reason"})
	r.bitcoinSSEConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_sse_connections_active",
		Help: "Number of active SSE connections to the Bitcoin event stream.",
	})
	r.bitcoinTokenConsumeFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bitcoin_token_consume_failures_total",
		Help: "Total Bitcoin SSE token consume failures by reason.",
	}, []string{"reason"})
	r.bitcoinBalanceDrift = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_balance_drift_satoshis",
		Help: "Accounting drift in satoshis. Must be zero at all times — any nonzero value is CRITICAL.",
	})
	r.bitcoinReconciliationHold = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_reconciliation_hold_active",
		Help: "1 when sweep-hold mode is active due to detected balance drift, 0 otherwise.",
	})
	r.bitcoinReorgDetected = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "bitcoin_reorg_detected_total",
		Help: "Total blockchain reorganisations detected.",
	})
	r.bitcoinPayoutFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "bitcoin_payout_failure_total",
		Help: "Total payout sweep failures.",
	})
	r.bitcoinFeeEstimate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bitcoin_fee_estimate_sat_per_vbyte",
		Help: "Current fee estimate in sat/vbyte by target_blocks confirmation target.",
	}, []string{"target_blocks"})
	r.bitcoinSweepStuck = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "bitcoin_sweep_stuck_total",
		Help: "Total times a sweep was detected as stuck.",
	})
	r.bitcoinWalletBackupAge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_wallet_backup_age_seconds",
		Help: "Seconds since the last successful wallet backup.",
	})
	r.bitcoinUTXOCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_utxo_count",
		Help: "Current number of UTXOs in the Bitcoin wallet.",
	})
	r.bitcoinInvoiceState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bitcoin_invoice_state_total",
		Help: "Current invoice count by status.",
	}, []string{"status"})
	r.bitcoinInvoiceDetection = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "bitcoin_invoice_detection_duration_seconds",
		Help:    "Duration from transaction broadcast to invoice detection.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300},
	})
	r.bitcoinRateFeedStaleness = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_rate_feed_staleness_seconds",
		Help: "Seconds since the last exchange rate update.",
	})
	r.bitcoinReconciliationLag = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_reconciliation_lag_blocks",
		Help: "Number of blocks the reconciliation job is behind the chain tip.",
	})

	// ── Register everything ───────────────────────────────────────────────
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		// Family 1
		r.httpRequestsTotal,
		r.httpRequestDuration,
		r.httpRequestsInFlight,
		r.httpErrors,
		// Family 2
		r.appErrors,
		// Family 3
		r.authLogins,
		r.authLoginFailures,
		r.authRegistrations,
		r.authRegistrationFailures,
		r.authTokenRefreshes,
		r.authTokenValidationFailures,
		r.authLogouts,
		r.authSessionRevocations,
		r.authEmailVerifications,
		r.authVerificationResends,
		r.authPasswordResets,
		r.authPasswordResetsDenied,
		r.authPasswordChanges,
		r.authAccountUnlocks,
		r.authOAuth,
		r.authOAuthFailures,
		r.authOAuthLinks,
		r.authOAuthUnlinks,
		r.authEmailChanges,
		r.authUsernameChanges,
		r.authAccountDeletions,
		r.authUserLocks,
		r.authUserUnlocks,
		// Family 4
		r.dbPoolTotal,
		r.dbPoolIdle,
		r.dbPoolAcquired,
		r.dbPoolMax,
		r.dbUp,
		r.redisPoolTotal,
		r.redisPoolIdle,
		r.redisPoolStale,
		r.redisUp,
		r.processGoroutines,
		r.processMemAlloc,
		r.infraPollerLastRunSeconds,
		// Family 5
		r.jobsSubmitted,
		r.jobsClaimed,
		r.jobsSucceeded,
		r.jobsFailed,
		r.jobsDead,
		r.jobsCancelled,
		r.jobDuration,
		r.schedulesFired,
		r.jobsRequeued,
		// Family 6
		r.bitcoinZMQConnected,
		r.bitcoinRPCConnected,
		r.bitcoinZMQLastMessageAge,
		r.bitcoinHandlerPanics,
		r.bitcoinHandlerGoroutines,
		r.bitcoinDroppedMessages,
		r.bitcoinSSEConnections,
		r.bitcoinTokenConsumeFailures,
		r.bitcoinBalanceDrift,
		r.bitcoinReconciliationHold,
		r.bitcoinReorgDetected,
		r.bitcoinPayoutFailures,
		r.bitcoinFeeEstimate,
		r.bitcoinSweepStuck,
		r.bitcoinWalletBackupAge,
		r.bitcoinUTXOCount,
		r.bitcoinInvoiceState,
		r.bitcoinInvoiceDetection,
		r.bitcoinRateFeedStaleness,
		r.bitcoinReconciliationLag,
	)

	return r
}

// Handler returns an http.Handler that serves the Prometheus metrics endpoint.
//
// SECURITY: this handler must NOT be reachable from the public internet.
// Bind to an internal-only port or gate with a bearer token middleware.
// See routes.go for the TODO comment.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.promReg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}
