// Package gatewayjob is the periodic BACKEND-role-only loop behind the multi-panel
// Gateway: one pass over every peer per tick, see internal/httpapi's SyncGatewayPeers
// for what a pass does. It runs here so a real client's subscription fetch never makes
// a network call to a peer while they wait, the same "never per-refresh round-trip
// cost" philosophy internal/hostmetrics/monitoring already follows for this panel's own
// nodes, applied one hop further out. Mirrors internal/reviewjob's own Run(ctx, ...,
// interval) shape.
package gatewayjob

import (
	"context"
	"time"
)

// Run ticks every interval until ctx is canceled. A pass that outlasts a tick delays
// the next one rather than overlapping it.
func Run(ctx context.Context, pass func(context.Context), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pass(ctx)
		}
	}
}
