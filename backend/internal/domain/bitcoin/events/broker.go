package events

import (
	"errors"
	"sync"
)

// brokerRecorder is the narrow metric interface Broker needs.
// Satisfied by *telemetry.Registry and NoopBitcoinRecorder; also nil-safe
// (all calls are guarded by a nil check).
type brokerRecorder interface {
	OnMessageDropped(reason string)
}

// ErrCapReached is returned by Broker.Subscribe when the per-process SSE
// connection ceiling (EventsConfig.MaxSSEProcess) is reached.
var ErrCapReached = errors.New("broker: process-wide SSE cap reached")

// Broker is the in-process SSE channel fan-out. It routes domain events to all
// active subscriber channels for each user.
//
// Broker is safe for concurrent use from multiple goroutines.
type Broker struct {
	mu       sync.Mutex
	subs     map[string][]chan Event // userID → subscriber channels
	maxConns int                     // BTC_MAX_SSE_PROCESS ceiling
	total    int                     // current total across all users
	rec      brokerRecorder          // nil-safe metrics recorder
}

// NewBroker constructs a Broker with the given process-wide connection ceiling.
func NewBroker(maxConns int, rec brokerRecorder) *Broker {
	return &Broker{
		subs:     make(map[string][]chan Event),
		maxConns: maxConns,
		rec:      rec,
	}
}

// Subscribe registers a new channel for userID and returns it.
// Returns ErrCapReached when the total exceeds maxConns.
// The returned channel has a small buffer to prevent slow clients from blocking
// the emit path.
func (b *Broker) Subscribe(userID string) (<-chan Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.total >= b.maxConns {
		return nil, ErrCapReached
	}

	ch := make(chan Event, 16)
	b.subs[userID] = append(b.subs[userID], ch)
	b.total++
	return ch, nil
}

// Unsubscribe removes the channel from the broker for userID.
// A no-op if the channel is not registered.
func (b *Broker) Unsubscribe(userID string, ch <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	chans := b.subs[userID]
	for i, c := range chans {
		if c == ch {
			// Swap-remove to avoid shifting the slice.
			chans[i] = chans[len(chans)-1]
			b.subs[userID] = chans[:len(chans)-1]
			if len(b.subs[userID]) == 0 {
				delete(b.subs, userID)
			}
			b.total--
			return
		}
	}
}

// EmitToUser delivers event to every channel subscribed for userID.
// Slow consumers (full channel buffer) are skipped and the drop is counted
// via the provided metric hook.
//
// Channel lifecycle: channels are never explicitly closed by the broker.
// The SSE event loop (handler.go) exits when its request context is cancelled
// or when a write error is detected. Unsubscribe removes the channel from the
// routing map; the channel memory is reclaimed once all senders have dropped
// their local reference. Sending on an unsubscribed channel is safe: the
// select/default branch drops the event rather than blocking or panicking.
func (b *Broker) EmitToUser(userID string, event Event) {
	b.mu.Lock()
	chans := b.subs[userID] // read under lock
	b.mu.Unlock()

	for _, ch := range chans {
		select {
		case ch <- event:
		default:
			// Channel full — client is too slow; drop the event.
			if b.rec != nil {
				b.rec.OnMessageDropped("sse_buffer_full")
			}
		}
	}
}

// Count returns the total number of active subscriptions across all users.
func (b *Broker) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

// ConnectedUserIDs returns a deduplicated snapshot of all userIDs that currently
// have at least one active SSE subscription in the broker.
// The order of the returned slice is unspecified.
// Used by MempoolTracker.HandleTxEvent to fan-out pending_mempool events only to
// users whose watch set overlaps the transaction outputs.
func (b *Broker) ConnectedUserIDs() []string {
	b.mu.Lock()
	ids := make([]string, 0, len(b.subs))
	for id := range b.subs {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	return ids
}
