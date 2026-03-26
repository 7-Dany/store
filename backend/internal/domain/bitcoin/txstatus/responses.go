package txstatus

// singleStatusResponse is the JSON body for a single-transaction status lookup.
//
// Confirmations and BlockHeight use pointer types so that zero is serialised
// for mempool transactions (Confirmations == 0 is meaningful) while nil is
// omitted for statuses where these fields are not applicable (not_found,
// conflicting, abandoned).
type singleStatusResponse struct {
	Status        TxStatus `json:"status"`
	Confirmations *int     `json:"confirmations,omitempty"`
	BlockHeight   *int     `json:"block_height,omitempty"`
}

// batchStatusResponse is the JSON body for a batch status lookup.
type batchStatusResponse struct {
	Statuses map[string]singleStatusResponse `json:"statuses"`
}
