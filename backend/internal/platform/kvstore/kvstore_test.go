// Using package kvstore gives access to unexported helpers and the jsonMarshal variable without exposing them in the public API.
package kvstore

import (
	"context"
	"errors"
	"testing"
	"time"

	redis_rate "github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// NewRedisStoreWithSHAs creates a RedisStore whose Lua-script SHAs are set tothe caller-supplied values.
// This allows tests to inject "bad" scripts (e.g.scripts that return a string instead of an array)
// so that the defensive type checks inside AtomicBackoffIncrement and AtomicBackoffAllow can be reached.
// The caller is responsible for closing the returned store.
func NewRedisStoreWithSHAs(url, incrSHA, allowSHA string) (*RedisStore, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	return &RedisStore{
		client:   client,
		limiter:  redis_rate.NewLimiter(client),
		incrSHA:  incrSHA,
		allowSHA: allowSHA,
	}, nil
}

// TestExistsFromGet_PropagatesNonNotFoundErrors verifies that existsFromGet
// forwards any error that is not ErrNotFound to the caller unchanged.
// This branch is unreachable through InMemoryStore.Get (which only returns
// nil or ErrNotFound), so we exercise it by injecting a failing getter.
func TestExistsFromGet_PropagatesNonNotFoundErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("unexpected store failure")

	failGet := func(_ context.Context, _ string) (string, error) {
		return "", sentinel
	}

	ok, err := existsFromGet(context.Background(), "any-key", failGet)
	require.ErrorIs(t, err, sentinel, "non-ErrNotFound errors must be propagated")
	require.False(t, ok)
}

// TestExistsFromGet_ReturnsTrueOnSuccess verifies the happy path: a getter
// that returns a value yields (true, nil).
func TestExistsFromGet_ReturnsTrueOnSuccess(t *testing.T) {
	t.Parallel()

	successGet := func(_ context.Context, _ string) (string, error) {
		return "value", nil
	}

	ok, err := existsFromGet(context.Background(), "any-key", successGet)
	require.NoError(t, err)
	require.True(t, ok)
}

// TestExistsFromGet_ReturnsFalseOnNotFound verifies that ErrNotFound is
// translated to (false, nil) — the same behaviour as InMemoryStore.Exists.
func TestExistsFromGet_ReturnsFalseOnNotFound(t *testing.T) {
	t.Parallel()

	notFoundGet := func(_ context.Context, _ string) (string, error) {
		return "", ErrNotFound
	}

	ok, err := existsFromGet(context.Background(), "any-key", notFoundGet)
	require.NoError(t, err)
	require.False(t, ok)
}

// TestAtomicBackoffIncrement_MarshalError verifies that AtomicBackoffIncrement
// surfaces a json.Marshal failure as a non-nil error and returns the zero
// time.Time sentinel. The branch is unreachable in production because
// backoffData (int + time.Time) always marshals successfully; we reach it by
// temporarily substituting a failing jsonMarshal implementation.
func TestAtomicBackoffIncrement_MarshalError(t *testing.T) {
	// Not parallel: mutates the package-level jsonMarshal variable.
	orig := jsonMarshal
	jsonMarshal = func(any) ([]byte, error) {
		return nil, errors.New("injected marshal failure")
	}
	t.Cleanup(func() { jsonMarshal = orig })

	s := NewInMemoryStore(0)
	unlocksAt, failures, err := s.AtomicBackoffIncrement(
		context.Background(), "key",
		100*time.Millisecond, time.Second, 5*time.Minute,
	)

	require.Error(t, err, "marshal failure must be surfaced as an error")
	require.Zero(t, failures, "failures must be zero on error")
	require.True(t, unlocksAt.IsZero(), "unlocksAt must be zero time on error")
}
