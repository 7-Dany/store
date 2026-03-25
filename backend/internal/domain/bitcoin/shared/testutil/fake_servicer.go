// Package bitcoinsharedtest provides test-only helpers shared across all
// Bitcoin domain feature sub-packages. It must never be imported by production code.
//
// # Import rules for test files
//
// This package imports watch (and other bitcoin domain sub-packages) to provide
// shared fakes. That is safe for EXTERNAL test packages (package foo_test) which
// are compiled in their own binary and are exempt from the standard cycle rules.
//
// It must NOT be imported by INTERNAL test files (package foo, not package foo_test)
// because those files are compiled as part of the foo package itself, closing the
// cycle:  foo → bitcoinsharedtest → foo.
//
// Pattern:
//
//	handler_test.go  (package watch_test) — may import bitcoinsharedtest ✓
//	service_test.go  (package watch)      — must NOT import bitcoinsharedtest ✗
package bitcoinsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/domain/bitcoin/events"
)

// ── EventsFakeServicer ────────────────────────────────────────────────────────

// EventsFakeServicer implements events.Servicer for handler unit tests.
// Each method delegates to its Fn field if non-nil; otherwise it returns a safe
// zero value and nil error so tests only configure the paths they exercise.
type EventsFakeServicer struct {
	IssueTokenFn            func(ctx context.Context, in events.IssueTokenInput) (events.IssueTokenResult, error)
	VerifyAndConsumeTokenFn func(ctx context.Context, in events.VerifyTokenInput) (events.VerifiedTokenResult, error)
	AcquireSlotFn           func(ctx context.Context, userID string) error
	SubscribeFn             func(ctx context.Context, userID string) (<-chan events.Event, error)
	ReleaseSlotFn           func(userID string, ch <-chan events.Event)
	IsZMQRunningFn          func() error
	StatusFn                func(ctx context.Context) events.StatusResult
	WriteAuditLogFn         func(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error
	ShutdownFn              func()
}

// compile-time check that *EventsFakeServicer satisfies events.Servicer.
var _ events.Servicer = (*EventsFakeServicer)(nil)

// IssueToken delegates to IssueTokenFn if set.
func (f *EventsFakeServicer) IssueToken(ctx context.Context, in events.IssueTokenInput) (events.IssueTokenResult, error) {
	if f.IssueTokenFn != nil {
		return f.IssueTokenFn(ctx, in)
	}
	return events.IssueTokenResult{}, nil
}

// VerifyAndConsumeToken delegates to VerifyAndConsumeTokenFn if set.
func (f *EventsFakeServicer) VerifyAndConsumeToken(ctx context.Context, in events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
	if f.VerifyAndConsumeTokenFn != nil {
		return f.VerifyAndConsumeTokenFn(ctx, in)
	}
	return events.VerifiedTokenResult{}, nil
}

// AcquireSlot delegates to AcquireSlotFn if set.
func (f *EventsFakeServicer) AcquireSlot(ctx context.Context, userID string) error {
	if f.AcquireSlotFn != nil {
		return f.AcquireSlotFn(ctx, userID)
	}
	return nil
}

// Subscribe delegates to SubscribeFn if set.
func (f *EventsFakeServicer) Subscribe(ctx context.Context, userID string) (<-chan events.Event, error) {
	if f.SubscribeFn != nil {
		return f.SubscribeFn(ctx, userID)
	}
	ch := make(chan events.Event, 1)
	return ch, nil
}

// ReleaseSlot delegates to ReleaseSlotFn if set.
func (f *EventsFakeServicer) ReleaseSlot(userID string, ch <-chan events.Event) {
	if f.ReleaseSlotFn != nil {
		f.ReleaseSlotFn(userID, ch)
	}
}

// IsZMQRunning delegates to IsZMQRunningFn if set.
func (f *EventsFakeServicer) IsZMQRunning() error {
	if f.IsZMQRunningFn != nil {
		return f.IsZMQRunningFn()
	}
	return nil
}

// Status delegates to StatusFn if set.
func (f *EventsFakeServicer) Status(ctx context.Context) events.StatusResult {
	if f.StatusFn != nil {
		return f.StatusFn(ctx)
	}
	return events.StatusResult{}
}

// WriteAuditLog delegates to WriteAuditLogFn if set.
func (f *EventsFakeServicer) WriteAuditLog(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error {
	if f.WriteAuditLogFn != nil {
		return f.WriteAuditLogFn(ctx, event, userID, metadata)
	}
	return nil
}

// Shutdown delegates to ShutdownFn if set.
func (f *EventsFakeServicer) Shutdown() {
	if f.ShutdownFn != nil {
		f.ShutdownFn()
	}
}
