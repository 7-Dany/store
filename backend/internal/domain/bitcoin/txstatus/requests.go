package txstatus

// createTrackedTxStatusRequest is the HTTP body for POST /bitcoin/tx.
type createTrackedTxStatusRequest struct {
	Network string `json:"network"`
	TxID    string `json:"txid"`
	Address string `json:"address,omitempty"`
}

// updateTrackedTxStatusRequest is the HTTP body for PUT /bitcoin/tx/{id}.
type updateTrackedTxStatusRequest struct {
	Address string `json:"address,omitempty"`
}
