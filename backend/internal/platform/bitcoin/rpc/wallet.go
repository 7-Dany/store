package rpc

import "context"

// ── Wallet methods ────────────────────────────────────────────────────────────

// GetTransaction fetches a wallet transaction by txid.
// Returns IsNotFoundError if the txid is not known to the wallet.
//
// Include_watchonly is hardcoded to false. This node operates a signing wallet
// only; watch-only addresses are not supported. If watch-only support is ever
// added this must be revisited — watch-only transactions would otherwise be
// silently invisible.
func (c *client) GetTransaction(ctx context.Context, txid string, verbose bool) (WalletTx, error) {
	var result WalletTx
	err := c.retryCall(ctx, rpcMethodGetTransaction, []any{txid, false, verbose}, &result)
	return result, err
}

// GetAddressInfo returns metadata about a wallet address.
func (c *client) GetAddressInfo(ctx context.Context, address string) (AddressInfo, error) {
	var result AddressInfo
	err := c.retryCall(ctx, rpcMethodGetAddressInfo, []any{address}, &result)
	return result, err
}

// GetWalletInfo returns wallet metadata including the current keypool size.
func (c *client) GetWalletInfo(ctx context.Context) (WalletInfo, error) {
	var result WalletInfo
	err := c.retryCall(ctx, rpcMethodGetWalletInfo, nil, &result)
	return result, err
}

// GetNewAddress generates a new P2WPKH bech32 address from the wallet's HD keypool.
//
// Not retried — address generation advances the keypool pointer. A retry after
// a partial success could silently skip a keypool slot. Callers own retry semantics.
func (c *client) GetNewAddress(ctx context.Context, label, addressType string) (string, error) {
	var result string
	err := c.call(ctx, rpcMethodGetNewAddress, []any{label, addressType}, &result)
	return result, err
}

// KeypoolRefill instructs Bitcoin Core to top up its pre-generated address pool.
// Not retried — callers own retry semantics for this mutation.
func (c *client) KeypoolRefill(ctx context.Context, newSize int) error {
	return c.call(ctx, rpcMethodKeypoolRefill, []any{newSize}, nil)
}
