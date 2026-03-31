package watch

// CreateWatchRequest is the HTTP body for POST /bitcoin/watch.
type CreateWatchRequest struct {
	Network   string `json:"network"`
	WatchType string `json:"watch_type"`
	Address   string `json:"address,omitempty"`
	TxID      string `json:"txid,omitempty"`
}

// UpdateWatchRequest is the HTTP body for PUT /bitcoin/watch/{id}.
type UpdateWatchRequest struct {
	WatchType string `json:"watch_type"`
	Address   string `json:"address,omitempty"`
	TxID      string `json:"txid,omitempty"`
}
