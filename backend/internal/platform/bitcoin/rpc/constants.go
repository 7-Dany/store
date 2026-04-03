package rpc

import (
	"time"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// ── Package-level logger ──────────────────────────────────────────────────────

// logger is the structured logger for this package. All records carry component="rpc".
var logger = telemetry.New("rpc")

// ── Transport constants ───────────────────────────────────────────────────────

const (
	// RPCDialTimeout is the maximum time allowed for the TCP connect phase.
	RPCDialTimeout = 5 * time.Second

	// RPCTLSHandshakeTimeout is defensive: RPC uses plain HTTP today, but the
	// transport is ready if TLS is ever added.
	RPCTLSHandshakeTimeout = 5 * time.Second

	// RPCResponseHeaderTimeout is the maximum time Bitcoin Core may hold an open
	// TCP connection without sending a response header. Without this, a node under
	// memory pressure can stall the call indefinitely even after the context
	// deadline fires, because ResponseHeaderTimeout operates at the transport
	// layer independently of context cancellation.
	RPCResponseHeaderTimeout = 10 * time.Second

	// RPCIdleConnTimeout matches http.DefaultTransport.
	RPCIdleConnTimeout = 90 * time.Second

	// RPCMaxIdleConnsPerHost is the keep-alive pool size for one Bitcoin Core node.
	// Bounded so the pool never grows unbounded under burst concurrency.
	RPCMaxIdleConnsPerHost = 4

	// RPCMaxConnsPerHost caps total connections (idle + active) to prevent
	// TCP port exhaustion under burst concurrency. Set to 2× the idle pool
	// size, which also aligns with Bitcoin Core's default -rpcththreads=4
	// (extra connections queue rather than open new sockets).
	RPCMaxConnsPerHost = 8

	// RPCMaxResponseBytes is the hard cap on response body size. Bitcoin Core's
	// largest legitimate response is a verbosity=2 mainnet block at roughly 4 MiB;
	// 8 MiB is generous. Without this cap, a misbehaving or malicious node could
	// exhaust process memory via io.ReadAll.
	RPCMaxResponseBytes = 8 << 20 // 8 MiB
)

// ── Retry constants ───────────────────────────────────────────────────────────

const (
	// RPCRetryBase is the initial backoff before the first retry attempt.
	RPCRetryBase = 1 * time.Second

	// RPCRetryCeiling caps the maximum backoff between retries. Kept shorter than
	// the ZMQ ceiling because RPC callers supply their own context deadlines.
	RPCRetryCeiling = 30 * time.Second

	// RPCMaxRetries is the number of additional attempts after the first call.
	// Total maximum calls per retryCall invocation = 1 + rpcMaxRetries.
	RPCMaxRetries = 4
)

// ── Method name constants ─────────────────────────────────────────────────────

const (
	rpcMethodGetBlockchainInfo      = "getblockchaininfo"
	rpcMethodGetBlockHeader         = "getblockheader"
	rpcMethodGetBlock               = "getblock"
	rpcMethodGetBlockHash           = "getblockhash"
	rpcMethodGetBlockCount          = "getblockcount"
	rpcMethodGetTransaction         = "gettransaction"
	rpcMethodGetNewAddress          = "getnewaddress"
	rpcMethodGetAddressInfo         = "getaddressinfo"
	rpcMethodGetMempoolEntry        = "getmempoolentry"
	rpcMethodGetWalletInfo          = "getwalletinfo"
	rpcMethodKeypoolRefill          = "keypoolrefill"
	rpcMethodEstimateSmartFee       = "estimatesmartfee"
	rpcMethodWalletCreateFundedPSBT = "walletcreatefundedpsbt"
	rpcMethodWalletProcessPSBT      = "walletprocesspsbt"
	rpcMethodFinalizePSBT           = "finalizepsbt"
	rpcMethodSendRawTransaction     = "sendrawtransaction"
	rpcMethodGetRawTransaction      = "getrawtransaction"
)

// ── Invoice address constants ─────────────────────────────────────────────────
//
// These are domain-layer constants ("invoice", "bech32") that live here for
// convenience because the RPC package is the only consumer. If a domain package
// ever needs to reference them directly, move them there.

// InvoiceAddressLabel is the label used when generating invoice addresses.
const InvoiceAddressLabel = "invoice"

// InvoiceAddressType is the address type used for invoice addresses (bech32 = P2WPKH).
const InvoiceAddressType = "bech32"

// jsonNull is a package-level cached []byte("null") to avoid allocation
// in the hot-path null-result guard.
var jsonNull = []byte("null")
