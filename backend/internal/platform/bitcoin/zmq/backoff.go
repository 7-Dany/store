package zmq

import (
	"context"
	"math/rand/v2"
	"time"
)

// sleepCtx blocks for d, returning true when the sleep completes and false when
// ctx is cancelled before d elapses. Uses time.NewTimer to avoid the timer leak
// that time.After causes when ctx is cancelled before the duration elapses.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// nextBackoff returns the next backoff duration: doubles current and adds up to
// 50% jitter to prevent thundering-herd reconnects from multiple instances,
// capped at reconnectCeiling.
//
// Note: Jitter is effective during the exponential backoff phase (1s → 2s → 4s →
// ... → 30s → 60s). Once the ceiling is reached, all instances reconnect at
// exactly 60s intervals. This is acceptable because: (a) the exponential phase
// already desynchronises instances that started at different times, and (b) at
// 60s the reconnection rate is already rate-limited to 1/min per instance.
func nextBackoff(current time.Duration) time.Duration {
	doubled := current * 2
	// rand.Int64N(n) panics if n <= 0; guard with max(1, ...).
	jitterRange := max(int64(current/2), 1)
	//nolint:gosec // Exponential-backoff jitter is non-cryptographic.
	jitter := time.Duration(rand.Int64N(jitterRange))
	return min(doubled+jitter, reconnectCeiling)
}
