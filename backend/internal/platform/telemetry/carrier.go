package telemetry

import (
	"context"
	"sync"
)

// carrier is a concurrency-safe error slot threaded through request context.
// It is unexported — callers interact only through [Attach].
type carrier struct {
	mu  sync.Mutex
	err error
}

func (c *carrier) set(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

func (c *carrier) get() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

type carrierKey struct{}

// newCarrierContext injects a fresh carrier into ctx and returns both the
// new context and the carrier. Called by [RequestMiddleware] before invoking
// the next handler.
func newCarrierContext(ctx context.Context) (context.Context, *carrier) {
	c := &carrier{}
	return context.WithValue(ctx, carrierKey{}, c), c
}

// Attach writes err to the carrier stored in ctx.
//
// No-op when:
//   - ctx has no carrier (worker goroutines, tests without middleware)
//   - err is nil
//
// Called automatically by [TelemetryHandler] on every ERROR log record.
// Domain code must never invoke Attach directly.
func Attach(ctx context.Context, err error) {
	if err == nil {
		return
	}
	c, ok := ctx.Value(carrierKey{}).(*carrier)
	if !ok || c == nil {
		return
	}
	c.set(err)
}
