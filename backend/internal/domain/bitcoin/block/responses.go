package block

// blockResponse is the JSON body for a block-details lookup.
type blockResponse struct {
	Hash              string  `json:"hash"`
	Confirmations     int     `json:"confirmations"`
	Height            int     `json:"height"`
	Version           int     `json:"version"`
	MerkleRoot        string  `json:"merkle_root"`
	Time              int64   `json:"time"`
	MedianTime        int64   `json:"median_time"`
	Nonce             uint32  `json:"nonce"`
	Bits              string  `json:"bits"`
	Difficulty        float64 `json:"difficulty"`
	Chainwork         string  `json:"chainwork"`
	TxCount           int     `json:"tx_count"`
	PreviousBlockHash string  `json:"previous_block_hash,omitempty"`
	NextBlockHash     string  `json:"next_block_hash,omitempty"`
}
