package zmq

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// requireLoopbackTCP panics if endpoint is not a well-formed loopback TCP
// address. IPC endpoints are always rejected — use tcp://127.0.0.1:<port>.
//
// This is a panic, not a returned error, so a misconfigured endpoint fails at
// startup rather than at the first connection attempt. The ZMQ port must never
// be reachable from outside the machine running Bitcoin Core.
func requireLoopbackTCP(endpoint, envName string) {
	if strings.HasPrefix(endpoint, "ipc://") {
		panic(fmt.Sprintf("zmq: %s: ipc:// endpoints are not supported; use tcp://127.0.0.1:<port>", envName))
	}
	if !strings.HasPrefix(endpoint, "tcp://") {
		panic(fmt.Sprintf("zmq: %s: endpoint must be a loopback TCP address (tcp://127.0.0.1:port), got %q", envName, endpoint))
	}
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(endpoint, "tcp://"))
	if err != nil {
		panic(fmt.Sprintf("zmq: %s: invalid TCP endpoint %q: %v", envName, endpoint, err))
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		panic(fmt.Sprintf("zmq: %s: invalid port in endpoint %q (must be 1–65535)", envName, endpoint))
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		panic(fmt.Sprintf("zmq: %s: endpoint host must be a loopback address, got %q", envName, endpoint))
	}
}
