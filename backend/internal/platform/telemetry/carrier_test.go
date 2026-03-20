package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── T-16: Attach writes error visible from outer context ─────────────────────

func TestAttach_WritesError(t *testing.T) {
	ctx, c := newCarrierContext(context.Background())
	err := errors.New("store failure")

	Attach(ctx, err)

	assert.Equal(t, err, c.get())
}

// ── T-17: Attach is no-op when context has no carrier ────────────────────────

func TestAttach_NoopWithoutCarrier(t *testing.T) {
	// Must not panic on a plain context.
	assert.NotPanics(t, func() {
		Attach(context.Background(), errors.New("err"))
	})
}

// ── T-18: Attach is no-op when err is nil ────────────────────────────────────

func TestAttach_NoopWhenNil(t *testing.T) {
	ctx, c := newCarrierContext(context.Background())

	Attach(ctx, nil)

	assert.Nil(t, c.get(), "carrier must remain empty after Attach(nil)")
}

// ── T-19: carrier is concurrency-safe under parallel Attach calls ─────────────

func TestCarrier_ConcurrencySafe(t *testing.T) {
	ctx, c := newCarrierContext(context.Background())
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			Attach(ctx, errors.New("parallel error"))
		}(i)
	}
	wg.Wait()

	// Must not race-detect or panic; carrier must hold some error.
	require.NotNil(t, c.get())
}

// ── newCarrierContext returns distinct context ─────────────────────────────────

func TestNewCarrierContext_ReturnsNewContext(t *testing.T) {
	parent := context.Background()
	child, _ := newCarrierContext(parent)

	assert.NotEqual(t, parent, child)
}

// ── Attach does not overwrite existing error ──────────────────────────────────

func TestAttach_LastWriteWins(t *testing.T) {
	ctx, c := newCarrierContext(context.Background())

	first := errors.New("first")
	second := errors.New("second")
	Attach(ctx, first)
	Attach(ctx, second)

	// carrier.set is not guarded against overwrite — last write wins.
	assert.Equal(t, second, c.get())
}
