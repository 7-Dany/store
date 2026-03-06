package mailertest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertest "github.com/7-Dany/store/backend/internal/platform/mailer/testutil"
	"github.com/stretchr/testify/require"
)

func TestNoopBase_SendReturnsNil(t *testing.T) {
	t.Parallel()
	base := mailertest.NoopBase()
	err := base.Send(context.Background(), "user@example.com", "123456")
	require.NoError(t, err)
}

func TestNoopBase_QueueAndTimeoutAreZero(t *testing.T) {
	t.Parallel()
	base := mailertest.NoopBase()
	require.Nil(t, base.Queue)
	require.Zero(t, base.Timeout)
}

func TestErrorBase_SendReturnsConfiguredError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("smtp down")
	base := mailertest.ErrorBase(sentinel)
	err := base.Send(context.Background(), "user@example.com", "000000")
	require.ErrorIs(t, err, sentinel)
}

func TestRecordingBase_RecordsCalls(t *testing.T) {
	t.Parallel()
	var calls []mailertest.Call
	base := mailertest.RecordingBase(&calls)

	_ = base.Send(context.Background(), "first@example.com", "111111")
	_ = base.Send(context.Background(), "second@example.com", "222222")

	require.Len(t, calls, 2)
	require.Equal(t, "first@example.com", calls[0].ToEmail)
	require.Equal(t, "111111", calls[0].Code)
	require.Equal(t, "second@example.com", calls[1].ToEmail)
	require.Equal(t, "222222", calls[1].Code)
}

func TestRecordingBase_ReturnsNilError(t *testing.T) {
	t.Parallel()
	var calls []mailertest.Call
	base := mailertest.RecordingBase(&calls)
	err := base.Send(context.Background(), "user@example.com", "123456")
	require.NoError(t, err)
}

func TestNoopBase_CanSetTimeout(t *testing.T) {
	t.Parallel()
	base := mailertest.NoopBase()
	base.Timeout = 5e9 // 5 seconds in nanoseconds
	_ = mailer.OTPHandlerBase(base) // ensure type compatibility
	require.NotZero(t, base.Timeout)
}
