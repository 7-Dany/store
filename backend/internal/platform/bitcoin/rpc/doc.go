// Package rpc provides a thin JSON-RPC client for the Bitcoin Core HTTP API.
//
// It translates Go method calls into HTTP POST requests to Bitcoin Core's RPC
// endpoint and parses the JSON responses into typed Go structs. Every domain
// package that needs Bitcoin network data calls through this client.
//
// Design constraints:
//   - Zero domain imports — this is a pure platform concern.
//   - Credential safety: user/pass are stored in an unexported type whose
//     Stringer/GoStringer/MarshalText/MarshalJSON/LogValue all return
//     "[redacted]", making accidental logging impossible.
//   - BTC-to-satoshi precision: all BTC amounts are typed as the unexported
//     btcRawAmount; callers must use BtcToSat() or BtcToSatSigned() for
//     conversion.
//   - No txindex dependency on wallet/mempool paths: wallet-native RPCs
//     (gettransaction, getaddressinfo, getrawtransaction for mempool, etc.)
//     work without txindex=1. Block-hash readers still depend on the node
//     retaining the referenced block data.
//   - Full observability: every call is metered via RPCRecorder (recorder.go).
//     Pass deps.Metrics directly — *telemetry.Registry satisfies the interface.
//   - Host safety: New() panics if the host is not a loopback address. RPC
//     credentials must never be transmitted over a non-loopback interface.
//     Additionally, every TCP connection is validated at dial time via
//     net.Dialer.Control to prevent TOCTOU DNS attacks.
//
// Tested against Bitcoin Core 30.x. Response struct fields cover the Bitcoin
// Core 30.x schema. Fields added in newer versions require updating types.go.
//
// Authentication uses HTTP Basic Auth. Cookie-based authentication (.cookie file)
// is NOT supported — configure -rpcuser and -rpcpassword in bitcoin.conf.
package rpc
