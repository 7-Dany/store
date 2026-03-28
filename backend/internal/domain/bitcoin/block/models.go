package block

// GetBlockInput is the input type for a block-details lookup.
type GetBlockInput struct {
	Hash string
}

// Result holds the resolved details for a single Bitcoin block.
type Result struct {
	Hash              string
	Confirmations     int
	Height            int
	Version           int
	MerkleRoot        string
	Time              int64
	MedianTime        int64
	Nonce             uint32
	Bits              string
	Difficulty        float64
	Chainwork         string
	TxCount           int
	PreviousBlockHash string
	NextBlockHash     string
}
