//go:build linux

package tunnelhealth

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// resolvers are real public DNS servers to query through the tunnel - a
// complete, ordinary DNS round trip (query out, answer back) rather than a
// bare TCP SYN that is immediately closed with no payload ever exchanged.
// The old probe's wire signature - connect, nothing, disconnect, forever,
// against the same three fixed addresses every 5s - is exactly what a port
// scanner looks like to automated abuse detection, which matters here more
// than most places: the typical exit on the other end of one of these
// tunnels is a third-party provider (e.g. Mullvad) actively watching for
// that pattern. A full DNS exchange is what every ordinary client already
// does constantly, so it looks like nothing worth flagging.
var resolvers = []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}

// probeDomain is a neutral, IANA-reserved, permanently-resolvable name -
// any of resolvers will answer it exactly like it would a real client's
// own lookup.
const probeDomain = "example.com."

// dnsLookup resolves probeDomain against each of resolvers in parallel
// (first answer wins), with the outgoing socket bound to iface via
// SO_BINDTODEVICE when iface is non-empty - the same binding sing-box
// applies for an outbound's bind_interface, so it exercises exactly the
// path user traffic takes. An empty iface dials over the host's own
// normal routing table instead, which is DialProbe's own baseline for
// telling "this one tunnel is broken" apart from "this host has no
// working internet/DNS at all right now."
func dnsLookup(ctx context.Context, iface string) (time.Duration, error) {
	dialer := net.Dialer{}
	if iface != "" {
		dialer.Control = func(network, address string, c syscall.RawConn) error {
			var bindErr error
			if err := c.Control(func(fd uintptr) { bindErr = unix.BindToDevice(int(fd), iface) }); err != nil {
				return err
			}
			return bindErr
		}
	}

	type result struct {
		rtt time.Duration
		err error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, len(resolvers))
	for _, resolver := range resolvers {
		go func(resolver string) {
			start := time.Now()
			r := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					// Ignore the address net.Resolver would otherwise pick
					// from the host's own /etc/resolv.conf - resolver (one
					// of the three well-known servers above) is the one
					// actually under test, through this specific iface.
					return dialer.DialContext(ctx, network, resolver)
				},
			}
			if _, err := r.LookupHost(ctx, probeDomain); err != nil {
				results <- result{err: err}
				return
			}
			results <- result{rtt: time.Since(start)}
		}(resolver)
	}
	var firstErr error
	for range resolvers {
		r := <-results
		if r.err == nil {
			return r.rtt, nil
		}
		if firstErr == nil {
			firstErr = r.err
		}
	}
	return 0, firstErr
}

// DialProbe is tunnelhealth's own Probe. Beyond "is iface usable right
// now", a failure also needs to say WHERE the fault most likely is, since
// the fix is different for each - see ErrMissing/ErrExitUnreachable/
// ErrNodeOffline's own doc comments, and domainFor, for the three
// outcomes this distinguishes:
//  1. iface itself does not exist: this host's own WireGuard config/
//     service never brought it up (ErrMissing).
//  2. A DNS lookup bound to iface fails, but the SAME lookup made over
//     this host's normal route succeeds: this host's own internet access
//     is fine, so the tunnel's own remote exit is the one not answering
//     or not forwarding this host's traffic (ErrExitUnreachable).
//  3. Both fail: this host has no working internet/DNS at all right now,
//     which has nothing to do with any one tunnel (ErrNodeOffline).
func DialProbe(ctx context.Context, iface string) (time.Duration, error) {
	if _, err := net.InterfaceByName(iface); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrMissing, err)
	}
	rtt, err := dnsLookup(ctx, iface)
	if err == nil {
		return rtt, nil
	}
	if _, baseErr := dnsLookup(ctx, ""); baseErr != nil {
		return 0, fmt.Errorf("%w: %v (this host's own lookup also failed: %v)", ErrNodeOffline, err, baseErr)
	}
	return 0, fmt.Errorf("%w: %v", ErrExitUnreachable, err)
}
