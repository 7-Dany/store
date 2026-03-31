package events

import "time"

// matchedOutput is one watched-address output match inside a transaction.
type matchedOutput struct {
	Address   string
	AmountSat int64
}

// TrackedStatusUpsertInput is the persistence payload for a watch-discovered tx.
type TrackedStatusUpsertInput struct {
	UserID          string
	Network         string
	TxID            string
	FeeRateSatVByte float64
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	Outputs         []matchedOutput
}
