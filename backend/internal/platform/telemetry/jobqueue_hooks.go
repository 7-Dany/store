package telemetry

import "time"

// Job queue hook methods implement the jobqueue.MetricsRecorder interface
// structurally. The interface is defined in internal/platform/jobqueue/metrics.go;
// *Registry satisfies it without importing that package (no import cycle).
//
// All methods are nil-safe: calling them on a nil *Registry is a no-op.
// Passing nil as jobqueue.ManagerConfig.Metrics silences all job queue metrics —
// useful in tests that do not need a real registry.

// OnJobSubmitted increments jobqueue_jobs_submitted_total for the given job kind.
func (r *Registry) OnJobSubmitted(kind string) {
	if r == nil {
		return
	}
	r.jobsSubmitted.WithLabelValues(kind).Inc()
}

// OnJobClaimed increments jobqueue_jobs_claimed_total for the given job kind.
func (r *Registry) OnJobClaimed(kind string) {
	if r == nil {
		return
	}
	r.jobsClaimed.WithLabelValues(kind).Inc()
}

// OnJobSucceeded increments jobqueue_jobs_succeeded_total and records the
// execution duration in the jobqueue_job_duration_seconds histogram.
func (r *Registry) OnJobSucceeded(kind string, duration time.Duration) {
	if r == nil {
		return
	}
	r.jobsSucceeded.WithLabelValues(kind).Inc()
	r.jobDuration.WithLabelValues(kind).Observe(duration.Seconds())
}

// OnJobFailed increments jobqueue_jobs_failed_total.
// willRetry indicates whether the job will be attempted again.
// err is accepted to match the interface signature but is not used for labels
// (unbounded cardinality); use app_errors_total for error-level details.
func (r *Registry) OnJobFailed(kind string, _ error, willRetry bool) {
	if r == nil {
		return
	}
	retry := "false"
	if willRetry {
		retry = "true"
	}
	r.jobsFailed.WithLabelValues(kind, retry).Inc()
}

// OnJobDead increments jobqueue_jobs_dead_total for the given job kind.
// Any increment triggers a JobQueueDeadJobsAccumulating alert.
func (r *Registry) OnJobDead(kind string) {
	if r == nil {
		return
	}
	r.jobsDead.WithLabelValues(kind).Inc()
}

// OnJobCancelled increments jobqueue_jobs_cancelled_total for the given job kind.
func (r *Registry) OnJobCancelled(kind string) {
	if r == nil {
		return
	}
	r.jobsCancelled.WithLabelValues(kind).Inc()
}

// OnScheduleFired increments jobqueue_schedules_fired_total for the given
// schedule and job kind pair.
func (r *Registry) OnScheduleFired(scheduleID, kind string) {
	if r == nil {
		return
	}
	r.schedulesFired.WithLabelValues(kind, scheduleID).Inc()
}

// OnJobsRequeued increments jobqueue_jobs_requeued_total by count.
// Any increment means the stall detector reset stalled jobs — indicates
// a worker crash or timeout.
func (r *Registry) OnJobsRequeued(count int) {
	if r == nil || count <= 0 {
		return
	}
	r.jobsRequeued.Add(float64(count))
}
