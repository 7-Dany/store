// Package bitcoinsharedtest provides test-only helpers shared across all
// Bitcoin domain feature sub-packages. It must never be imported by production code.
package bitcoinsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/domain/bitcoin/watch"
)

// ── WatchFakeStorer ───────────────────────────────────────────────────────────

// WatchFakeStorer is a hand-written implementation of watch.Storer for service
// unit tests. Each method delegates to its Fn field if non-nil; otherwise it
// returns the zero value and nil error so tests only configure the fields they need.
type WatchFakeStorer struct {
	RunWatchCapFn              func(ctx context.Context, userID string, limit int, addresses []string) (success, newCount, addedCount int64, err error)
	IncrGlobalWatchCountFn     func(ctx context.Context) error
	PublishCacheInvalidationFn func(ctx context.Context, userID string) error
	ListWatchAddressKeysFn     func(ctx context.Context, cursor uint64, count int64) (keys []string, nextCursor uint64, err error)
	GetWatchSetSizeFn          func(ctx context.Context, key string) (int64, error)
	WriteAuditLogFn            func(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

// compile-time check that *WatchFakeStorer satisfies watch.Storer.
var _ watch.Storer = (*WatchFakeStorer)(nil)

// RunWatchCap delegates to RunWatchCapFn if set.
func (f *WatchFakeStorer) RunWatchCap(ctx context.Context, userID string, limit int, addresses []string) (int64, int64, int64, error) {
	if f.RunWatchCapFn != nil {
		return f.RunWatchCapFn(ctx, userID, limit, addresses)
	}
	return 1, int64(len(addresses)), int64(len(addresses)), nil
}

// IncrGlobalWatchCount delegates to IncrGlobalWatchCountFn if set.
func (f *WatchFakeStorer) IncrGlobalWatchCount(ctx context.Context) error {
	if f.IncrGlobalWatchCountFn != nil {
		return f.IncrGlobalWatchCountFn(ctx)
	}
	return nil
}

// PublishCacheInvalidation delegates to PublishCacheInvalidationFn if set.
func (f *WatchFakeStorer) PublishCacheInvalidation(ctx context.Context, userID string) error {
	if f.PublishCacheInvalidationFn != nil {
		return f.PublishCacheInvalidationFn(ctx, userID)
	}
	return nil
}

// ListWatchAddressKeys delegates to ListWatchAddressKeysFn if set.
func (f *WatchFakeStorer) ListWatchAddressKeys(ctx context.Context, cursor uint64, count int64) ([]string, uint64, error) {
	if f.ListWatchAddressKeysFn != nil {
		return f.ListWatchAddressKeysFn(ctx, cursor, count)
	}
	return nil, 0, nil
}

// GetWatchSetSize delegates to GetWatchSetSizeFn if set.
func (f *WatchFakeStorer) GetWatchSetSize(ctx context.Context, key string) (int64, error) {
	if f.GetWatchSetSizeFn != nil {
		return f.GetWatchSetSizeFn(ctx, key)
	}
	return 0, nil
}

// WriteAuditLog delegates to WriteAuditLogFn if set.
func (f *WatchFakeStorer) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	if f.WriteAuditLogFn != nil {
		return f.WriteAuditLogFn(ctx, event, userID, sourceIP, metadata)
	}
	return nil
}
