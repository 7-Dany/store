package txstatus

import "time"

// trackedTxStatusResponse is the JSON body for one durable txstatus row.
type trackedTxStatusResponse struct {
	ID              int64                            `json:"id"`
	Network         string                           `json:"network"`
	TrackingMode    string                           `json:"tracking_mode"`
	Address         *string                          `json:"address,omitempty"`
	Addresses       []trackedTxStatusAddressResponse `json:"addresses,omitempty"`
	TxID            string                           `json:"txid"`
	Status          TxStatus                         `json:"status"`
	Confirmations   int                              `json:"confirmations"`
	AmountSat       int64                            `json:"amount_sat"`
	FeeRateSatVByte float64                          `json:"fee_rate_sat_vbyte"`
	FirstSeenAt     time.Time                        `json:"first_seen_at"`
	LastSeenAt      time.Time                        `json:"last_seen_at"`
	ConfirmedAt     *time.Time                       `json:"confirmed_at,omitempty"`
	BlockHash       *string                          `json:"block_hash,omitempty"`
	BlockHeight     *int64                           `json:"block_height,omitempty"`
	ReplacementTxID *string                          `json:"replacement_txid,omitempty"`
	CreatedAt       time.Time                        `json:"created_at"`
	UpdatedAt       time.Time                        `json:"updated_at"`
}

// trackedTxStatusAddressResponse is one related watched address on a tx row.
type trackedTxStatusAddressResponse struct {
	Address   string `json:"address"`
	AmountSat int64  `json:"amount_sat"`
}

// trackedTxStatusesResponse is the list envelope for GET /bitcoin/tx.
type trackedTxStatusesResponse struct {
	Items      []trackedTxStatusResponse `json:"items"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}
