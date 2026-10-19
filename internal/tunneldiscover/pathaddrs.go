package tunneldiscover

import "github.com/legendary1205/rapido-go/internal/netprobe"

// PathAddrs picks the relay addresses the node should ping to measure the
// path to the relay: the first IPv6 and the first IPv4 address. A detected
// tunnel knows them from the node's own configuration (Details.RelayAddrs,
// primary first); a tunnel the panel built has the address it runs on
// (relayTunnelAddr, when it differs from the SSH one) and the relay host.
// Hostnames are ignored - only IP literals are ever pinged.
func PathAddrs(d Details, relayTunnelAddr, relayHost string) (v6, v4 string) {
	candidates := append([]string{}, d.RelayAddrs...)
	candidates = append(candidates, relayTunnelAddr, relayHost)
	return netprobe.PickAddrs(candidates...)
}
