package authsharedtest

import (
	"context"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
)

// NopBackoffChecker is a verification.BackoffChecker that always allows
// requests and records no state. Used by handler tests that do not exercise
// backoff behaviour.
type NopBackoffChecker struct{}

var _ verification.BackoffChecker = (*NopBackoffChecker)(nil)

func (n *NopBackoffChecker) Allow(_ context.Context, _ string) (bool, time.Duration) {
	return true, 0
}
func (n *NopBackoffChecker) RecordFailure(_ context.Context, _ string) time.Duration { return 0 }
func (n *NopBackoffChecker) Reset(_ context.Context, _ string)                       {}
