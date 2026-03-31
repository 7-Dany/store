package watch

import "time"

// WatchType identifies the subject of a Bitcoin watch resource.
type WatchType string

const (
	// WatchTypeAddress watches all discovered chain activity related to an address.
	WatchTypeAddress WatchType = "address"
	// WatchTypeTransaction watches the lifecycle of one transaction on chain.
	WatchTypeTransaction WatchType = "transaction"
)

// WatchStatus is the persisted lifecycle state of a watch resource.
type WatchStatus string

const (
	// WatchStatusActive means the watch is currently active.
	WatchStatusActive WatchStatus = "active"
)

// Watch is one persisted watch resource.
type Watch struct {
	ID        int64
	Network   string
	WatchType WatchType
	Address   *string
	TxID      *string
	Status    WatchStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateWatchInput creates one watch resource for the authenticated user.
type CreateWatchInput struct {
	UserID    string
	Network   string
	WatchType WatchType
	Address   *string
	TxID      *string
	SourceIP  string
}

// UpdateWatchInput updates one existing watch resource owned by the authenticated user.
type UpdateWatchInput struct {
	ID        int64
	UserID    string
	WatchType WatchType
	Address   *string
	TxID      *string
	SourceIP  string
}

// GetWatchInput selects one watch resource by ID.
type GetWatchInput struct {
	ID     int64
	UserID string
}

// ListWatchesInput filters watch resources for one user.
type ListWatchesInput struct {
	UserID          string
	Network         string
	WatchType       string
	Address         string
	TxID            string
	BeforeCreatedAt *time.Time
	BeforeID        int64
	Limit           int
}

// DeleteWatchInput deletes one watch resource.
type DeleteWatchInput struct {
	ID       int64
	UserID   string
	SourceIP string
}
