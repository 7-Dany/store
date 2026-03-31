package watch

import "time"

// watchResponse is the JSON body for one watch resource.
type watchResponse struct {
	ID        int64       `json:"id"`
	Network   string      `json:"network"`
	WatchType string      `json:"watch_type"`
	Address   *string     `json:"address,omitempty"`
	TxID      *string     `json:"txid,omitempty"`
	Status    WatchStatus `json:"status"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// listWatchesResponse is the list envelope for GET /bitcoin/watch.
type listWatchesResponse struct {
	Items      []watchResponse `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
