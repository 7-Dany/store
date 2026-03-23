package watch

// WatchInput is the service-layer input for the watch registration operation.
type WatchInput struct {
	// UserID is the authenticated user's ID string from the JWT claim.
	UserID string
	// Addresses contains the normalised addresses to register. Bech32 addresses
	// (bc1*/tb1*) are lowercased; base58check addresses (P2PKH/P2SH) are kept
	// in their original mixed-case form. All addresses have been validated by
	// the handler before this input is built.
	Addresses []string
	// Network is the active Bitcoin network ("testnet4" or "mainnet"),
	// already confirmed to match the server's BTC_NETWORK by the handler.
	Network string
	// SourceIP is the trusted real client IP from respond.ClientIP(r).
	SourceIP string
}

// WatchResult is the service-layer result for a successful watch registration.
type WatchResult struct {
	// Watching contains the normalised addresses from the current request that
	// are now active (whether newly added or already registered).
	//
	// This is NOT the user's complete watch list — it echoes the submitted
	// addresses. Clients must track their own full list.
	Watching []string
}
