package mailer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// ── Dead-letter store ────────────────────────────────────────────────────────────

// DeadLetterJob is a delivery job that exhausted all retry attempts.
type DeadLetterJob struct {
	Job       Job
	Attempts  int
	LastErr   error
	DroppedAt time.Time
}

// DeadLetterStore receives jobs that have exhausted all delivery attempts.
// Implementations must be safe for concurrent use.
type DeadLetterStore interface {
	Add(job DeadLetterJob)
}

// InMemoryDeadLetterStore is a bounded, thread-safe dead-letter store backed
// by a ring buffer. When capacity is reached the oldest entry is silently
// evicted to make room, preventing unbounded memory growth during a sustained
// SMTP outage.
type InMemoryDeadLetterStore struct {
	mu   sync.Mutex
	jobs []DeadLetterJob
	cap  int
}

// NewInMemoryDeadLetterStore creates a store with the given capacity.
// Panics if capacity < 1.
func NewInMemoryDeadLetterStore(capacity int) *InMemoryDeadLetterStore {
	if capacity < 1 {
		panic("mailer: InMemoryDeadLetterStore capacity must be >= 1")
	}
	return &InMemoryDeadLetterStore{
		jobs: make([]DeadLetterJob, 0, capacity),
		cap:  capacity,
	}
}

// Add appends a dead-letter job. When the store is at capacity the oldest
// entry is dropped to make room for the new one.
func (s *InMemoryDeadLetterStore) Add(job DeadLetterJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs) >= s.cap {
		// Evict oldest: copy remaining elements down and truncate so the backing
		// array does not grow. O(n) copy is acceptable for the expected small size.
		copy(s.jobs, s.jobs[1:])
		s.jobs = s.jobs[:len(s.jobs)-1]
	}
	s.jobs = append(s.jobs, job)
}

// Jobs returns a snapshot of all dead-letter entries in arrival order.
func (s *InMemoryDeadLetterStore) Jobs() []DeadLetterJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeadLetterJob, len(s.jobs))
	copy(out, s.jobs)
	return out
}

// Len returns the current number of dead-letter entries.
func (s *InMemoryDeadLetterStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}

const defaultQueueSize = 256

// maxDeliveryAttempts is the total number of send attempts deliverWithRetry
// makes before routing a job to the dead-letter store.
const maxDeliveryAttempts = 5

// baseRetryDelay is the initial backoff interval between delivery attempts.
// The interval doubles between attempts, capped at maxRetryDelay.
// Declared as a variable (not const) so tests can override it without
// spawning external processes or waiting seconds per test run.
var baseRetryDelay = 500 * time.Millisecond

// maxRetryDelay is the ceiling for the exponential backoff interval.
var maxRetryDelay = 30 * time.Second

// emailToken returns a short, non-reversible correlation token for log lines.
// It must not be used outside of log attributes.
func emailToken(email string) string {
	h := sha256.Sum256([]byte(email))
	return fmt.Sprintf("%x", h[:4]) // 8 hex chars, sufficient for correlation
}

// Job is a single async email delivery request.
//
// Storing a context.Context in a struct is normally discouraged, but is an
// accepted pattern for async queues where the context must be detached from
// the originating request's deadline and the caller's cancellation scope.
// Always construct Ctx from context.Background() with an explicit deadline
// (context.WithDeadline or context.WithTimeout). Do not use context.WithoutCancel
// on the originating request context: it inherits the request's deadline and
// will be cancelled when the request's cancel chain fires.
type Job struct {
	// Ctx is the delivery context. It must be detached from the originating request
	// (so the send is not cancelled when the HTTP handler returns) and must carry
	// a deadline so that Queue.Shutdown is bounded. Use context.WithDeadline or
	// context.WithTimeout on context.Background(). Never use the originating
	// request's context directly, and never pass context.Background() without a
	// deadline (doing so blocks Shutdown indefinitely).
	Ctx    context.Context
	UserID string
	Email  string
	Code   string
	// Deliver is the send function invoked by the queue worker. It must not be
	// nil. Callers set this to the appropriate Mailer method (e.g.
	// m.SendVerificationEmail or m.SendUnlockEmail) so the queue is agnostic
	// about which email template is used.
	Deliver func(ctx context.Context, toEmail, code string) error
}

// Queue is a bounded async mail delivery queue backed by a pool of worker
// goroutines. It is safe for concurrent use.
//
// Each job is attempted up to 5 times with exponential back-off starting at
// 500 ms and capped at 30 s. Worst-case per-job retry duration is therefore
// roughly 2 minutes (0.5 s + 1 s + 2 s + 4 s + … capped). Every Job.Ctx must
// carry a deadline to bound Shutdown; see Job for details.
//
// Typical lifecycle:
//
//	q := mailer.NewQueue(m)
//	if err := q.Start(4); err != nil { ... }
//	// enqueue jobs from HTTP handlers
//	q.Shutdown() // call after HTTP server shuts down; blocks until all sends finish
//
// A panic inside a Mailer.SendVerificationEmail call propagates through the
// inner delivery goroutine and crashes the process. The caller is responsible
// for ensuring the Mailer implementation does not panic.
type Queue struct {
	jobs       chan Job
	maxConc    int
	deadLetter DeadLetterStore // nil = log-only (no capture)

	mu      sync.Mutex
	started bool
	closed  bool
	done    chan struct{}
}

// QueueOption configures a Queue.
type QueueOption func(*Queue)

// WithQueueSize sets the job channel buffer capacity. Defaults to 256.
// n must be >= 1; values < 1 are ignored.
func WithQueueSize(n int) QueueOption {
	return func(q *Queue) {
		if n >= 1 {
			q.jobs = make(chan Job, n)
		}
	}
}

// WithMaxConcurrency sets the maximum number of simultaneous in-flight SMTP
// connections across all workers. Defaults to 16.
// n must be >= 1; values < 1 are ignored.
func WithMaxConcurrency(n int) QueueOption {
	return func(q *Queue) {
		if n >= 1 {
			q.maxConc = n
		}
	}
}

// WithDeadLetterStore wires a DeadLetterStore to the queue. Jobs that exhaust
// all delivery attempts are passed to s.Add instead of being silently dropped.
// s must not be nil; passing nil is ignored.
func WithDeadLetterStore(s DeadLetterStore) QueueOption {
	return func(q *Queue) {
		if s != nil {
			q.deadLetter = s
		}
	}
}

// NewQueue creates a new mail delivery queue.
func NewQueue(opts ...QueueOption) *Queue {
	q := &Queue{
		jobs:    make(chan Job, defaultQueueSize),
		maxConc: 16,
		done:    make(chan struct{}),
	}
	for _, o := range opts {
		o(q)
	}
	return q
}

// Start launches n worker goroutines that drain the queue.
// Returns an error if n < 1, if Start has already been called, or if the
// queue has been shut down.
// A panic inside Mailer.SendVerificationEmail propagates and crashes the process;
// wrap the Mailer to recover panics if this is unacceptable.
func (q *Queue) Start(n int) error {
	if n < 1 {
		return fmt.Errorf("mailer: Queue.Start: worker count must be >= 1, got %d", n)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.started {
		return fmt.Errorf("mailer: Queue.Start: already started")
	}
	if q.closed {
		return fmt.Errorf("mailer: Queue.Start: queue is shut down")
	}
	q.started = true

	sem := make(chan struct{}, q.maxConc)
	dl := q.deadLetter // capture once; q must not be mutated after Start returns
	eg := new(errgroup.Group)
	for range n {
		eg.Go(func() error {
			for job := range q.jobs {
				sem <- struct{}{}
				go func(j Job) {
					// The write to j (j.Deliver) happens-before <-sem, so
					// Shutdown's drain guarantees all writes are visible before
					// it returns.
					defer func() { <-sem }()
					deliverWithRetry(j, dl)
				}(job)
			}
			return nil
		})
	}

	// After all workers exit (queue closed + drained), acquire every semaphore
	// slot to guarantee all in-flight send goroutines have released, then
	// signal done. If SendVerificationEmail hangs indefinitely, Shutdown will
	// too — callers should ensure the mailer has its own timeout.
	go func() {
		eg.Wait() //nolint:errcheck — workers never return non-nil errors
		for range cap(sem) {
			sem <- struct{}{}
		}
		close(q.done)
	}()

	return nil
}

// Enqueue adds a job to the queue without blocking.
// Returns an error if the queue is full or has been shut down.
// Returns an error if j.Ctx is nil, j.Deliver is nil, or if the context does
// not carry a deadline; a context without a deadline causes Shutdown to block
// indefinitely if the SMTP server is slow or unresponsive. Use
// context.WithDeadline or context.WithTimeout on context.Background(). See Job
// for details.
func (q *Queue) Enqueue(j Job) error {
	if j.Ctx == nil {
		return fmt.Errorf("mailer: Job.Ctx must not be nil")
	}
	if _, hasDeadline := j.Ctx.Deadline(); !hasDeadline {
		return fmt.Errorf(
			"mailer: Job.Ctx must carry a deadline; " +
				"use context.WithDeadline or context.WithTimeout on context.Background()",
		)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return fmt.Errorf("mailer: queue is shut down")
	}
	if j.Deliver == nil {
		return fmt.Errorf("mailer: Job.Deliver must not be nil")
	}
	select {
	case q.jobs <- j:
		return nil
	default:
		return fmt.Errorf("mailer: queue full (capacity %d)", cap(q.jobs))
	}
}

// deliverWithRetry attempts to send a verification email for j with exponential
// back-off. It retries up to maxDeliveryAttempts times (starting at baseRetryDelay,
// doubling each attempt, capped at maxRetryDelay). If every attempt fails, the job
// is passed to dl (when non-nil) and an ERROR log is always emitted.
func deliverWithRetry(j Job, dl DeadLetterStore) {

	delay := baseRetryDelay
	var lastErr error
	for attempt := range maxDeliveryAttempts {
		if attempt > 0 {
			// Back off before retrying; respect context cancellation.
			select {
			case <-j.Ctx.Done():
				slog.WarnContext(j.Ctx, "mailer: context cancelled during retry back-off",
				 "user_id", j.UserID, "email_token", emailToken(j.Email))
				return
			case <-time.After(delay):
			}
			if delay*2 < maxRetryDelay {
				delay *= 2
			} else {
				delay = maxRetryDelay
			}
		}

		err := j.Deliver(j.Ctx, j.Email, j.Code)
		if err == nil {
			if attempt > 0 {
				slog.InfoContext(j.Ctx, "mailer: delivery succeeded after retry",
					"attempt", attempt+1, "user_id", j.UserID, "email_token", emailToken(j.Email))
			}
			return
		}
		lastErr = err
		slog.WarnContext(j.Ctx, "mailer: delivery attempt failed",
			"attempt", attempt+1,
			"max_attempts", maxDeliveryAttempts,
			"user_id", j.UserID,
			"email_token", emailToken(j.Email),
			"error", err,
		)
	}

	// All attempts exhausted. Route to dead-letter store if configured so the
	// job is not silently lost. Always emit an ERROR log regardless so operators
	// see the failure even without a store wired.
	slog.ErrorContext(j.Ctx, "mailer: all delivery attempts exhausted — job moved to dead-letter",
		"user_id", j.UserID, "email_token", emailToken(j.Email), "last_err", lastErr)
	if dl != nil {
		dl.Add(DeadLetterJob{
			Job:       j,
			Attempts:  maxDeliveryAttempts,
			LastErr:   lastErr,
			DroppedAt: time.Now(),
		})
	}
}

// Shutdown closes the queue and blocks until all in-flight sends complete.
// It is safe to call without calling Start, and safe to call multiple times.
// Shutdown blocks until every in-flight delivery goroutine completes.
// Because there is no separate timeout, every Job.Ctx must carry a deadline;
// a Job constructed with context.Background() (no deadline) will hold Shutdown
// open until the SMTP server responds or closes the connection, potentially
// stalling graceful application shutdown indefinitely.
func (q *Queue) Shutdown() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	close(q.jobs)
	q.mu.Unlock()

	if q.started {
		<-q.done
	}
}
