package events

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Subscribe / Unsubscribe ───────────────────────────────────────────────────

func TestBroker_Subscribe_ReturnsChannel(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	ch, err := b.Subscribe("user-a")
	require.NoError(t, err)
	assert.NotNil(t, ch)
}

func TestBroker_Subscribe_CountIncrements(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	_, _ = b.Subscribe("user-a")
	_, _ = b.Subscribe("user-a")
	assert.Equal(t, 2, b.Count())
}

func TestBroker_Subscribe_CapReached_ReturnsError(t *testing.T) {
	t.Parallel()
	b := NewBroker(2, nil)
	_, err1 := b.Subscribe("user-a")
	_, err2 := b.Subscribe("user-b")
	require.NoError(t, err1)
	require.NoError(t, err2)

	_, err3 := b.Subscribe("user-c")
	assert.ErrorIs(t, err3, ErrCapReached)
}

func TestBroker_Unsubscribe_CountDecrements(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	ch, _ := b.Subscribe("user-a")
	assert.Equal(t, 1, b.Count())

	b.Unsubscribe("user-a", ch)
	assert.Equal(t, 0, b.Count())
}

func TestBroker_Unsubscribe_UnknownChannel_NoOp(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	ch := make(chan Event, 1) // not registered
	// Must not panic.
	b.Unsubscribe("user-x", ch)
	assert.Equal(t, 0, b.Count())
}

func TestBroker_Unsubscribe_RemovesCorrectChannelWhenMultiple(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	ch1, _ := b.Subscribe("user-a")
	ch2, _ := b.Subscribe("user-a")
	assert.Equal(t, 2, b.Count())

	b.Unsubscribe("user-a", ch1)
	assert.Equal(t, 1, b.Count())

	// ch2 must still receive events.
	b.EmitToUser("user-a", Event{Type: "ping", Payload: []byte(`{}`)})
	select {
	case e := <-ch2:
		assert.Equal(t, "ping", e.Type)
	case <-time.After(time.Second):
		t.Fatal("ch2 did not receive event after ch1 was unsubscribed")
	}
}

func TestBroker_Unsubscribe_AfterCapReached_AllowsNewSubscription(t *testing.T) {
	t.Parallel()
	b := NewBroker(1, nil)
	ch, err := b.Subscribe("user-a")
	require.NoError(t, err)

	b.Unsubscribe("user-a", ch)

	_, err2 := b.Subscribe("user-b")
	require.NoError(t, err2, "slot freed by Unsubscribe must be available to new subscriber")
}

// ── EmitToUser ────────────────────────────────────────────────────────────────

func TestBroker_EmitToUser_DeliveredToSubscriber(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	ch, _ := b.Subscribe("user-a")

	b.EmitToUser("user-a", Event{Type: "new_block", Payload: []byte(`{"height":100}`)})

	select {
	case e := <-ch:
		assert.Equal(t, "new_block", e.Type)
		assert.JSONEq(t, `{"height":100}`, string(e.Payload))
	case <-time.After(time.Second):
		t.Fatal("event not delivered within 1s")
	}
}

func TestBroker_EmitToUser_DeliveredToAllSubscribersForUser(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	ch1, _ := b.Subscribe("user-a")
	ch2, _ := b.Subscribe("user-a")

	b.EmitToUser("user-a", Event{Type: "confirmed_tx", Payload: []byte(`{"txid":"abc"}`)})

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			assert.Equal(t, "confirmed_tx", e.Type, "channel %d", i)
		case <-time.After(time.Second):
			t.Fatalf("channel %d did not receive event within 1s", i)
		}
	}
}

func TestBroker_EmitToUser_NoSubscribers_NoOp(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	// Must not panic.
	b.EmitToUser("ghost-user", Event{Type: "ping", Payload: []byte(`{}`)})
}

func TestBroker_EmitToUser_SlowConsumer_EventDropped(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	ch, _ := b.Subscribe("user-slow")

	// Fill the channel buffer (capacity 16) to trigger the drop path.
	for i := 0; i < cap(ch)+5; i++ {
		b.EmitToUser("user-slow", Event{Type: "ping", Payload: []byte(`{}`)})
	}
	// The broker must not block; the extra events are silently dropped.
	assert.LessOrEqual(t, len(ch), cap(ch), "channel must not exceed its buffer capacity")
}

func TestBroker_EmitToUser_DoesNotDeliverToOtherUser(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	chA, _ := b.Subscribe("user-a")
	chB, _ := b.Subscribe("user-b")

	b.EmitToUser("user-a", Event{Type: "ping", Payload: []byte(`{}`)})

	select {
	case <-chA:
		// expected
	case <-time.After(time.Second):
		t.Fatal("user-a did not receive its event")
	}
	select {
	case e := <-chB:
		t.Fatalf("user-b received an event it should not have: %v", e)
	default:
		// correct — channel empty
	}
}

// ── ConnectedUserIDs ──────────────────────────────────────────────────────────

func TestBroker_ConnectedUserIDs_Empty(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	assert.Empty(t, b.ConnectedUserIDs())
}

func TestBroker_ConnectedUserIDs_ReturnsDistinctUsers(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	_, _ = b.Subscribe("user-a")
	_, _ = b.Subscribe("user-a") // second connection for same user
	_, _ = b.Subscribe("user-b")

	ids := b.ConnectedUserIDs()
	assert.Len(t, ids, 2, "each user must appear exactly once regardless of connection count")

	seen := make(map[string]bool)
	for _, id := range ids {
		seen[id] = true
	}
	assert.True(t, seen["user-a"])
	assert.True(t, seen["user-b"])
}

func TestBroker_ConnectedUserIDs_UpdatesAfterUnsubscribe(t *testing.T) {
	t.Parallel()
	b := NewBroker(10, nil)
	ch, _ := b.Subscribe("user-a")
	b.Unsubscribe("user-a", ch)

	assert.Empty(t, b.ConnectedUserIDs())
}

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestBroker_ConcurrentSubscribeEmitUnsubscribe_NoRace(t *testing.T) {
	t.Parallel()
	b := NewBroker(200, nil)
	const goroutines = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			userID := "user-concurrent"
			ch, err := b.Subscribe(userID)
			if err != nil {
				return
			}
			b.EmitToUser(userID, Event{Type: "ping", Payload: []byte(`{}`)})
			// Drain without blocking.
			select {
			case <-ch:
			default:
			}
			b.Unsubscribe(userID, ch)
		}()
	}
	wg.Wait()
}
