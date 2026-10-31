package main

import (
	"github.com/sagernet/sing/common/json/badoption"

	sbox "github.com/sagernet/sing-box/option"
)

// guardPrivateDestinations keeps proxy users away from what only the node can
// reach. Off only in this package's tests, whose echo servers sit on loopback.
var guardPrivateDestinations = true

// nonPublicCIDRs are the ranges ip_is_private leaves out that are still not the
// public internet: "this network" (0.x reaches localhost on Linux), carrier-grade
// NAT (Alibaba's metadata service and Tailscale live there), the IETF special
// block with Oracle's legacy metadata address, the benchmarking range, and
// Azure's wire server, which is a public-looking address only the host can reach.
var nonPublicCIDRs = []string{
	"0.0.0.0/8",
	"100.64.0.0/10",
	"168.63.129.16/32",
	"192.0.0.0/24",
	"198.18.0.0/15",
}

// destinationGuard builds the rules that stop a proxy user reaching the node's
// loopback, its private networks and its cloud provider's metadata service.
// Without them the node dials whatever a client names, from its own place on the
// network, as nothing else in the route looks at the destination by default.
//
// An admin can still route to a private range on purpose: a rule naming it with
// ip_cidr keeps that range open, and one with ip_is_private keeps them all open.
// A rule to a block outbound opens nothing, since blocking is what this does too.
type destinationGuard struct {
	rule sbox.Rule
	// resolveFirst tells whether a domain reaching tag can land on the node's own
	// network and so has to be looked up before the guard can judge it.
	resolveFirst func(tag string) bool
}

// newDestinationGuard returns nil when there is nothing to guard.
func newDestinationGuard(core *coreSpec) *destinationGuard {
	if !guardPrivateDestinations {
		return nil
	}
	outbounds := map[string]outboundSpec{}
	if core != nil {
		for _, ob := range core.Outbounds {
			outbounds[ob.Tag] = ob
		}
	}
	isBlock := func(tag string) bool {
		return tag == "block" || outbounds[tag].Type == "block"
	}

	var allowed []string
	if core != nil {
		for _, r := range core.RoutingRules {
			if isBlock(r.OutboundTag) {
				continue
			}
			if r.IPIsPrivate {
				return nil
			}
			allowed = append(allowed, r.IPCIDR...)
		}
	}

	return &destinationGuard{
		rule: privateDestinationRule(allowed),
		resolveFirst: func(tag string) bool {
			return reachesNodeNetwork(tag, outbounds, map[string]bool{})
		},
	}
}

// privateDestinationRule blocks what isn't public, except the allowed ranges.
// Inside one sing-box rule these matchers are alternatives, so subtracting needs
// a logical rule around them.
func privateDestinationRule(allowed []string) sbox.Rule {
	matchers := sbox.RawDefaultRule{
		Domain:       badoption.Listable[string]{"localhost"},
		DomainSuffix: badoption.Listable[string]{".localhost"},
		IPCIDR:       badoption.Listable[string](nonPublicCIDRs),
		IPIsPrivate:  true,
	}
	// Reject, not the block outbound: clients on a full tunnel send LAN traffic here
	// all day, and a refused connection is a debug line while a blocked one is an
	// error. NoDrop keeps a burst of them closing at once instead of hanging.
	block := sbox.RuleAction{
		Action:        "reject",
		RejectOptions: sbox.RejectActionOptions{Method: "default", NoDrop: true},
	}
	if len(allowed) == 0 {
		return sbox.Rule{Type: "default", DefaultOptions: sbox.DefaultRule{RawDefaultRule: matchers, RuleAction: block}}
	}
	return sbox.Rule{Type: "logical", LogicalOptions: sbox.LogicalRule{
		RawLogicalRule: sbox.RawLogicalRule{
			Mode: "and",
			Rules: []sbox.Rule{
				{Type: "default", DefaultOptions: sbox.DefaultRule{RawDefaultRule: matchers}},
				{Type: "default", DefaultOptions: sbox.DefaultRule{RawDefaultRule: sbox.RawDefaultRule{
					IPCIDR: badoption.Listable[string](allowed),
					Invert: true,
				}}},
			},
		},
		RuleAction: block,
	}}
}

// reachesNodeNetwork reports whether traffic sent to tag can leave through the
// node's own routes. An outbound bound to an interface can't, unless it falls
// back to the default route, and a proxy outbound hands the name to its server.
func reachesNodeNetwork(tag string, outbounds map[string]outboundSpec, seen map[string]bool) bool {
	if tag == "direct" {
		return true
	}
	if seen[tag] {
		return false
	}
	seen[tag] = true
	ob, known := outbounds[tag]
	if !known {
		return false
	}
	switch ob.Type {
	case "direct":
		return ob.BindInterface == "" || ob.DirectFallback
	case "selector", "urltest":
		for _, member := range ob.Outbounds {
			if reachesNodeNetwork(member, outbounds, seen) {
				return true
			}
		}
	}
	return false
}

// resolveRule looks a domain up before the guard runs, so a name pointing at
// 127.0.0.1 is judged by that address. Only ever placed ahead of traffic leaving
// through the node's own routes: doing it for a proxy outbound would move DNS from
// the exit to this node, and change which CDN edge its users get.
func resolveRule(matchers sbox.RawDefaultRule) sbox.Rule {
	return sbox.Rule{Type: "default", DefaultOptions: sbox.DefaultRule{
		RawDefaultRule: matchers,
		RuleAction:     sbox.RuleAction{Action: "resolve"},
	}}
}
