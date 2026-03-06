package mailer_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fake deliver funcs ────────────────────────────────────────────────────────

// failMailer always returns ErrSendFailed.
type failMailer struct {
	calls atomic.Int64
}

func (f *failMailer) deliver(_ context.Context, _, _ string) error {
	f.calls.Add(1)
	return mailer.ErrSendFailed
}

// succeedMailer always returns nil.
type succeedMailer struct {
	calls atomic.Int64
}

func (s *succeedMailer) deliver(_ context.Context, _, _ string) error {
	s.calls.Add(1)
	return nil
}

// nthFailMailer fails for the first n calls, then succeeds.
type nthFailMailer struct {
	failFor int64
	calls   atomic.Int64
}

func (m *nthFailMailer) deliver(_ context.Context, _, _ string) error {
	n := m.calls.Add(1)
	if n <= m.failFor {
		return mailer.ErrSendFailed
	}
	return nil
}

// ── deadline context helper ───────────────────────────────────────────────────

func ctxWithDeadline(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func newJob(t *testing.T, deliver func(context.Context, string, string) error, ttl time.Duration) mailer.Job {
	t.Helper()
	return mailer.Job{
		Ctx:     ctxWithDeadline(t, ttl),
		UserID:  "u1",
		Email:   "u@example.com",
		Code:    "123456",
		Deliver: deliver,
	}
}

// ── InMemoryDeadLetterStore ───────────────────────────────────────────────────

func TestInMemoryDeadLetterStore_PanicOnZeroCapacity(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		mailer.NewInMemoryDeadLetterStore(0)
	})
}

func TestInMemoryDeadLetterStore_PanicOnNegativeCapacity(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		mailer.NewInMemoryDeadLetterStore(-1)
	})
}

func TestInMemoryDeadLetterStore_AddAndLen(t *testing.T) {
	t.Parallel()
	s := mailer.NewInMemoryDeadLetterStore(5)
	require.Equal(t, 0, s.Len())

	s.Add(mailer.DeadLetterJob{})
	s.Add(mailer.DeadLetterJob{})
	assert.Equal(t, 2, s.Len())
}

func TestInMemoryDeadLetterStore_JobsReturnsSnapshot(t *testing.T) {
	t.Parallel()
	s := mailer.NewInMemoryDeadLetterStore(5)
	s.Add(mailer.DeadLetterJob{Attempts: 1})
	s.Add(mailer.DeadLetterJob{Attempts: 2})

	jobs := s.Jobs()
	require.Len(t, jobs, 2)
	assert.Equal(t, 1, jobs[0].Attempts)
	assert.Equal(t, 2, jobs[1].Attempts)
}

// TestInMemoryDeadLetterStore_EvictsOldestWhenFull verifies that when the store
// is at capacity the oldest entry is dropped to make room for the new one.
func TestInMemoryDeadLetterStore_EvictsOldestWhenFull(t *testing.T) {
	t.Parallel()
	s := mailer.NewInMemoryDeadLetterStore(3)

	s.Add(mailer.DeadLetterJob{Attempts: 1}) // oldest
	s.Add(mailer.DeadLetterJob{Attempts: 2})
	s.Add(mailer.DeadLetterJob{Attempts: 3})
	// Store is full; adding one more must evict Attempts==1.
	s.Add(mailer.DeadLetterJob{Attempts: 4})

	jobs := s.Jobs()
	require.Len(t, jobs, 3)
	assert.Equal(t, 2, jobs[0].Attempts, "oldest entry must have been evicted")
	assert.Equal(t, 4, jobs[2].Attempts)
}

func TestInMemoryDeadLetterStore_ConcurrentAdd(t *testing.T) {
	t.Parallel()
	const n = 100
	s := mailer.NewInMemoryDeadLetterStore(n)

	done := make(chan struct{})
	for range n {
		go func() {
			s.Add(mailer.DeadLetterJob{})
			done <- struct{}{}
		}()
	}
	for range n {
		<-done
	}
	assert.Equal(t, n, s.Len())
}

// ── Queue constructor options ─────────────────────────────────────────────────

func TestNewQueue_DefaultsApplied(t *testing.T) {
	t.Parallel()
	// Smoke test: NewQueue returns a usable *Queue.
	q := mailer.NewQueue()
	require.NotNil(t, q)
	q.Shutdown() // safe to call without Start
}

func TestWithQueueSize_IgnoresZeroOrNegative(t *testing.T) {
	t.Parallel()
	// A queue with size 0 or negative must not panic; it keeps the default.
	q := mailer.NewQueue(mailer.WithQueueSize(0))
	require.NoError(t, q.Start(1))
	q.Shutdown()
}

func TestWithMaxConcurrency_IgnoresZeroOrNegative(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue(mailer.WithMaxConcurrency(0))
	require.NoError(t, q.Start(1))
	q.Shutdown()
}

// TestWithMaxConcurrency_ValidValue_SetsConcurrency exercises the
// `q.maxConc = n` assignment inside the option closure by passing a valid
// positive value. The queue must deliver the job, which is only possible if
// the semaphore inside Start was created with non-zero capacity.
func TestWithMaxConcurrency_ValidValue_SetsConcurrency(t *testing.T) {
	t.Parallel()
	sm := &succeedMailer{}
	q := mailer.NewQueue(mailer.WithMaxConcurrency(4))
	require.NoError(t, q.Start(1))
	require.NoError(t, q.Enqueue(newJob(t, sm.deliver, 5*time.Second)))
	q.Shutdown()
	require.Equal(t, int64(1), sm.calls.Load(),
		"job must be delivered, proving the semaphore was created with capacity 4")
}

func TestWithDeadLetterStore_NilIgnored(t *testing.T) {
	t.Parallel()
	// Passing nil to WithDeadLetterStore must not panic and must be a no-op.
	q := mailer.NewQueue(mailer.WithDeadLetterStore(nil))
	require.NoError(t, q.Start(1))
	q.Shutdown()
}

// ── Queue.Start guard conditions ──────────────────────────────────────────────

func TestQueue_StartRejectsZeroWorkers(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()
	require.Error(t, q.Start(0))
}

func TestQueue_StartRejectsNegativeWorkers(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()
	require.Error(t, q.Start(-1))
}

func TestQueue_StartTwiceReturnsError(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()
	require.NoError(t, q.Start(1))
	require.Error(t, q.Start(1), "second Start must return an error")
	q.Shutdown()
}

func TestQueue_StartAfterShutdownReturnsError(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()
	q.Shutdown()
	require.Error(t, q.Start(1))
}

// ── Queue.Shutdown ────────────────────────────────────────────────────────────

// TestQueue_ShutdownWithoutStart covers the `if q.started` false branch in
// Shutdown: it must return immediately without blocking.
func TestQueue_ShutdownWithoutStart(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()

	done := make(chan struct{})
	go func() {
		q.Shutdown()
		close(done)
	}()
	select {
	case <-done:
		// expected: Shutdown returned quickly
	case <-time.After(time.Second):
		t.Fatal("Shutdown without Start blocked unexpectedly")
	}
}

func TestQueue_ShutdownIdempotent(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()
	require.NoError(t, q.Start(1))
	q.Shutdown()
	// Calling Shutdown a second time must not panic or block.
	done := make(chan struct{})
	go func() {
		q.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second Shutdown call blocked")
	}
}

// ── Queue.Enqueue guard conditions ───────────────────────────────────────────

func TestQueue_EnqueueNilCtx(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()
	require.NoError(t, q.Start(1))
	defer q.Shutdown()

	err := q.Enqueue(mailer.Job{Ctx: nil, Email: "u@example.com", Code: "1"})
	require.Error(t, err)
	require.ErrorContains(t, err, "Ctx")
}

func TestQueue_EnqueueNoDeadlineCtx(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()
	require.NoError(t, q.Start(1))
	defer q.Shutdown()

	//nolint:forbidigo // intentional: we are testing that a deadline-less context is rejected
	err := q.Enqueue(mailer.Job{Ctx: context.Background(), Email: "u@example.com", Code: "1"})
	require.Error(t, err)
	require.ErrorContains(t, err, "deadline")
}

// TestQueue_EnqueueAfterShutdown covers queue.go line 263.
func TestQueue_EnqueueAfterShutdown(t *testing.T) {
	t.Parallel()
	sm := &succeedMailer{}
	q := mailer.NewQueue()
	require.NoError(t, q.Start(1))
	q.Shutdown()

	err := q.Enqueue(newJob(t, sm.deliver, time.Second))
	require.Error(t, err)
	require.ErrorContains(t, err, "shut down")
}

func TestQueue_EnqueueWhenFull(t *testing.T) {
	t.Parallel()
	sm := &succeedMailer{}
	q := mailer.NewQueue(mailer.WithQueueSize(1))
	defer q.Shutdown()

	j := newJob(t, sm.deliver, 5*time.Second)
	require.NoError(t, q.Enqueue(j))   // fills the single slot
	err := q.Enqueue(j)                // channel is full — must return an error
	require.Error(t, err)
	require.ErrorContains(t, err, "full")
}

// blockDeliver blocks on gate until it is closed.
type blockDeliver struct {
	gate <-chan struct{}
}

func (b *blockDeliver) deliver(ctx context.Context, _, _ string) error {
	select {
	case <-b.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ── Successful end-to-end delivery ───────────────────────────────────────────

func TestQueue_SuccessfulDelivery(t *testing.T) {
	t.Parallel()
	sm := &succeedMailer{}

	delivered := make(chan struct{}, 1)
	notify := &notifyDeliver{inner: sm.deliver, done: delivered}
	q := mailer.NewQueue()
	require.NoError(t, q.Start(1))
	defer q.Shutdown()

	require.NoError(t, q.Enqueue(newJob(t, notify.deliver, 5*time.Second)))

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("email was not delivered within timeout")
	}
}

// notifyDeliver wraps a deliver func and signals done on the first success.
type notifyDeliver struct {
	inner func(context.Context, string, string) error
	done  chan<- struct{}
	once  atomic.Bool
}

func (n *notifyDeliver) deliver(ctx context.Context, email, code string) error {
	err := n.inner(ctx, email, code)
	if err == nil && n.once.CompareAndSwap(false, true) {
		n.done <- struct{}{}
	}
	return err
}

// ── Dead-letter exhaustion ────────────────────────────────────────────────────

// TestQueue_DeadLetterOnExhaustedRetries verifies that a job whose Mailer
// always returns an error is eventually moved to the dead-letter store after
// all maxDeliveryAttempts (5) attempts have been made.
//
// The test uses SetTestRetryDelays to reduce wait time to milliseconds.
// It must NOT run in parallel because it mutates package-level retry vars.
func TestQueue_DeadLetterOnExhaustedRetries(t *testing.T) {
	defer mailer.SetTestRetryDelays(2*time.Millisecond, 8*time.Millisecond)()

	dl := mailer.NewInMemoryDeadLetterStore(10)
	fm := &failMailer{}

	q := mailer.NewQueue(
		mailer.WithDeadLetterStore(dl),
		mailer.WithQueueSize(4),
	)
	require.NoError(t, q.Start(1))

	require.NoError(t, q.Enqueue(newJob(t, fm.deliver, 10*time.Second)))

	// Wait until the dead-letter store has exactly one entry; give it ample time.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if dl.Len() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	q.Shutdown()

	require.Equal(t, 1, dl.Len(), "job must appear in dead-letter store")
	entry := dl.Jobs()[0]
	assert.Equal(t, 5, entry.Attempts)
	assert.True(t, errors.Is(entry.LastErr, mailer.ErrSendFailed))
	assert.False(t, entry.DroppedAt.IsZero())
}

// TestQueue_NoDeadLetterStore_ExhaustedRetriesDropped confirms that when no
// dead-letter store is configured, exhausted jobs are simply dropped (no panic).
func TestQueue_NoDeadLetterStore_ExhaustedRetriesDropped(t *testing.T) {
	defer mailer.SetTestRetryDelays(2*time.Millisecond, 8*time.Millisecond)()

	fm := &failMailer{}
	q := mailer.NewQueue(mailer.WithQueueSize(2))
	require.NoError(t, q.Start(1))
	require.NoError(t, q.Enqueue(newJob(t, fm.deliver, 10*time.Second)))

	// Give the worker enough time to exhaust all 5 attempts.
	time.Sleep(200 * time.Millisecond)
	q.Shutdown() // must not panic or block
}

// TestQueue_RetrySucceedsOnNthAttempt verifies the "delivery succeeded after
// retry" log path: the mailer fails twice then succeeds.
func TestQueue_RetrySucceedsOnNthAttempt(t *testing.T) {
	defer mailer.SetTestRetryDelays(2*time.Millisecond, 8*time.Millisecond)()

	dl := mailer.NewInMemoryDeadLetterStore(5)
	nfm := &nthFailMailer{failFor: 2}

	delivered := make(chan struct{}, 1)
	notify := &notifyDeliver{inner: nfm.deliver, done: delivered}

	q := mailer.NewQueue(mailer.WithDeadLetterStore(dl))
	require.NoError(t, q.Start(1))
	defer q.Shutdown()

	require.NoError(t, q.Enqueue(newJob(t, notify.deliver, 10*time.Second)))

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("email was never delivered after retries")
	}
	assert.Equal(t, 0, dl.Len(), "job must not be in dead-letter on eventual success")
}

// ── Context cancelled during retry back-off ───────────────────────────────────

// TestQueue_CtxCancelledDuringRetryBackoff covers queue.go's
//
//	case <-j.Ctx.Done(): return
//
// branch.  The mailer always fails, so a retry is scheduled after baseRetryDelay.
// The job context is set to expire before that retry fires.  The job must not
// reach the dead-letter store (delivery was abandoned mid-flight, not exhausted).
//
// Must NOT run in parallel; uses SetTestRetryDelays.
func TestQueue_CtxCancelledDuringRetryBackoff(t *testing.T) {
	// First attempt: immediate.  Retry back-off: 50 ms.
	// Context deadline: 20 ms (expires during the back-off sleep).
	defer mailer.SetTestRetryDelays(50*time.Millisecond, 200*time.Millisecond)()

	dl := mailer.NewInMemoryDeadLetterStore(5)
	fm := &failMailer{}

	q := mailer.NewQueue(mailer.WithDeadLetterStore(dl))
	require.NoError(t, q.Start(1))
	defer q.Shutdown()

	// Context expires well before the 50 ms retry sleep.
	job := mailer.Job{
		Ctx:     ctxWithDeadline(t, 20*time.Millisecond),
		UserID:  "u1",
		Email:   "u@example.com",
		Code:    "000",
		Deliver: fm.deliver,
	}
	require.NoError(t, q.Enqueue(job))

	// Allow enough time for the first attempt + context expiry + some slack.
	time.Sleep(200 * time.Millisecond)
	q.Shutdown()

	// Context was cancelled during back-off → job was abandoned, not exhausted.
	assert.Equal(t, 0, dl.Len(), "abandoned job must NOT appear in dead-letter store")
}

// ── Delay cap branch (queue.go else { delay = maxRetryDelay }) ───────────────

// TestQueue_RetryDelayCapApplied covers the `else { delay = maxRetryDelay }`
// branch in deliverWithRetry by setting maxRetryDelay lower than baseRetryDelay*2.
// With base=4ms and max=6ms: after attempt-1 sleep delay doubles to 8ms which
// exceeds max, triggering the cap assignment.
//
// Must NOT run in parallel; uses SetTestRetryDelays.
func TestQueue_RetryDelayCapApplied(t *testing.T) {
	// base=4ms, max=6ms → 4*2=8 > 6, so the else branch fires on attempt 2.
	defer mailer.SetTestRetryDelays(4*time.Millisecond, 6*time.Millisecond)()

	dl := mailer.NewInMemoryDeadLetterStore(5)
	fm := &failMailer{}

	q := mailer.NewQueue(mailer.WithDeadLetterStore(dl))
	require.NoError(t, q.Start(1))
	defer q.Shutdown()

	require.NoError(t, q.Enqueue(newJob(t, fm.deliver, 10*time.Second)))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if dl.Len() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	q.Shutdown()

	require.Equal(t, 1, dl.Len(), "job must be in dead-letter after all retries with capped delay")
}
