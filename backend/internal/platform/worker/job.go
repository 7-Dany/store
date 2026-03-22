// Package worker provides a general-purpose async job dispatcher and scheduler.
//
// Typical wiring at startup (in server.New):
//
//	d := worker.NewDispatcher()
//	d.Register(worker.KindPurgeAccounts, worker.NewPurgeHandler(pool))
//	d.Start(4)
//
//	s := worker.NewScheduler(d)
//	s.Add(worker.ScheduleEntry{
//	    Kind:     worker.KindPurgeAccounts,
//	    Interval: time.Hour,
//	    MakeCtx:  func() (context.Context, context.CancelFunc) {
//	        return context.WithTimeout(context.Background(), 10*time.Minute)
//	    },
//	})
//	s.Start(ctx)
//
//	// At shutdown:
//	s.Stop()
//	d.Shutdown()
package worker

import (
	"context"
	"time"
)

// Kind identifies a job type. Use package-level constants so kind strings are
// never inlined as raw literals and typos are caught at compile time.
type Kind string

// Job is a unit of async work submitted to a Dispatcher.
//
// Context rules — same contract as mailer.Job:
//   - Ctx must be detached from the originating HTTP request (so the job
//     is not cancelled when the handler returns).
//   - Ctx must carry a deadline so that Shutdown is bounded. Use
//     context.WithDeadline or context.WithTimeout on context.Background().
//   - Never pass context.WithoutCancel on a request ctx — that inherits
//     the request's deadline and will fire when the cancel chain fires.
type Job struct {
	// Kind identifies which registered Handler processes this job.
	Kind Kind
	// Payload is the handler-specific data. It must be the concrete type
	// expected by the handler. The Dispatcher does not inspect it.
	Payload any
	// Ctx is the job's execution context. Must not be nil; must have a deadline.
	Ctx context.Context
	// attempt is the zero-based retry counter. Set and incremented by the
	// Dispatcher; callers must not set it.
	attempt int
}

// Handler processes a single job. Implementations must be safe for concurrent
// use — the Dispatcher may call Handle from multiple goroutines simultaneously.
type Handler interface {
	Handle(ctx context.Context, payload any) error
}

// HandlerFunc adapts an ordinary function to the Handler interface.
type HandlerFunc func(ctx context.Context, payload any) error

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, payload any) error {
	return f(ctx, payload)
}

// ── Dead-letter ───────────────────────────────────────────────────────────────

// DeadLetterJob wraps a Job that exhausted all delivery attempts.
type DeadLetterJob struct {
	Job       Job
	Attempts  int
	LastErr   error
	DroppedAt time.Time
}

// DeadLetterStore receives jobs that exceed maxAttempts.
// Implementations must be safe for concurrent use.
type DeadLetterStore interface {
	Add(job DeadLetterJob)
}
