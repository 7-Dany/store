// Package mailertest provides test doubles for the mailer package.
// It must never be imported by production code.
package mailertest

import (
	"context"
	"sync"

	"github.com/7-Dany/store/backend/internal/platform/mailer"
)

// Call records one invocation of a Send function.
type Call struct {
	ToEmail string
	Code    string
}

// NoopBase returns an OTPHandlerBase whose Send is a no-op.
// Use in handler tests that do not care about mail delivery.
func NoopBase() mailer.OTPHandlerBase {
	return mailer.OTPHandlerBase{
		Send: func(_ context.Context, _, _ string) error { return nil },
	}
}

// ErrorBase returns an OTPHandlerBase whose Send always returns err.
func ErrorBase(err error) mailer.OTPHandlerBase {
	return mailer.OTPHandlerBase{
		Send: func(_ context.Context, _, _ string) error { return err },
	}
}

// RecordingBase returns an OTPHandlerBase that appends every Send invocation
// to *calls. Safe for concurrent use.
func RecordingBase(calls *[]Call) mailer.OTPHandlerBase {
	var mu sync.Mutex
	return mailer.OTPHandlerBase{
		Send: func(_ context.Context, toEmail, code string) error {
			mu.Lock()
			*calls = append(*calls, Call{ToEmail: toEmail, Code: code})
			mu.Unlock()
			return nil
		},
	}
}
