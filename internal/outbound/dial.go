package outbound

import (
	"context"
	"net"
	"os"
	"strings"
	"time"
)

// forceIPv4 reports whether M365_FORCE_IPV4 is enabled. Some networks have
// broken IPv6 routes to Microsoft endpoints (substrate.office.com resolves to
// several AAAA records), so Go's dialer tries IPv6 first and eats a timeout on
// every new connection. Forcing IPv4 avoids that (issue #79).
func forceIPv4() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("M365_FORCE_IPV4"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// applyNetworkPolicy rewrites the dial network to tcp4 when IPv4 is forced.
func applyNetworkPolicy(network string) string {
	if forceIPv4() && network == "tcp" {
		return "tcp4"
	}
	return network
}

// dialContextDirect is the shared direct dial function with the IPv4 policy
// applied; it is used by the HTTP transport and the WebSocket dialer.
func dialContextDirect(ctx context.Context, network, address string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return d.DialContext(ctx, applyNetworkPolicy(network), address)
}
