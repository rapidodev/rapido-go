package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	sbox "github.com/sagernet/sing-box/option"

	"github.com/legendary1205/rapido-go/internal/hostmetrics"
	"github.com/legendary1205/rapido-go/internal/nodecore"
	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
)

// POST /outbounds/test measures outbounds from this node: one HEAD request to
// outboundTestURL through each, timed. Those named by tag are the running
// core's; those sent whole are drafts, each built alone into a throwaway core of
// its own that has no inbound and is closed after its one test, so the running
// core and its users are never touched. It is on the control plane like every
// other panel call, so only the panel's own certificate gets here (see
// controlTLSConfig). The URL is fixed rather than taken from the request, so the
// endpoint can only ever reach that one site.
const (
	// outboundTestTimeout bounds one test: the dial, the handshakes and the
	// request.
	outboundTestTimeout = 5 * time.Second
	// outboundTestParallel is how many tests run at once on this node, across
	// every request: the panel testing a few dozen outbounds, perhaps from
	// several admins, opens no more connections than this.
	outboundTestParallel = 8
	// outboundTestBudget bounds a whole request; a test still waiting for a
	// slot by then is reported as not run. It stays under the panel's own
	// timeout for the call, so the panel always gets an answer.
	outboundTestBudget  = 18 * time.Second
	outboundTestMaxTags = 64
	// outboundTestMaxDrafts caps the drafts of one request: each costs a core
	// of its own, built and closed inside its test slot.
	outboundTestMaxDrafts = 8
	outboundTestMaxBody   = 64 << 10
)

// outboundTestURL is where every test goes. A variable only so tests can point
// it at a local server.
var outboundTestURL = "https://www.gstatic.com/generate_204"

var outboundTestSlots = make(chan struct{}, outboundTestParallel)

// Why an outbound was not measured (outboundTestResult.Skipped).
const (
	outboundSkipMissing = "missing" // no such outbound in the core this node runs
	outboundSkipBlock   = "block"   // it refuses every connection by design
	outboundSkipNoTCP   = "no_tcp"  // it carries no TCP, and the test is HTTP
	outboundSkipBusy    = "busy"    // no free slot before the request's budget ran out
)

type outboundTestRequest struct {
	Tags []string `json:"tags"`
	// Outbounds are drafts: outbounds not (or not yet in this form) in the
	// running core, in the same shape as the core config's.
	Outbounds []outboundSpec `json:"outbounds,omitempty"`
}

// outboundTestResult is one outbound on this node: a measurement (DelayMS and
// StatusCode), an Error, or the reason it was Skipped. Interface and Via say
// what the outbound depends on here, which differs from node to node.
type outboundTestResult struct {
	Tag        string `json:"tag"`
	DelayMS    int64  `json:"delay_ms"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error,omitempty"`
	Skipped    string `json:"skipped,omitempty"`
	// Interface is the network interface the outbound is pinned to;
	// InterfaceHere says whether this host has it (or a WireGuard config for
	// it). Every node is sent every exit in the fleet, so most pinned exits
	// name a tunnel that lives on another server.
	Interface     string `json:"interface,omitempty"`
	InterfaceHere bool   `json:"interface_here"`
	// Via is the member a direct-fallback exit is using right now (tag~wg or
	// tag~direct); Fallback is true when it is the plain direct one, because
	// the tunnel is down or not on this node.
	Via      string `json:"via,omitempty"`
	Fallback bool   `json:"fallback"`
	// Draft marks the result of an outbound sent whole rather than by tag: it
	// was tested alone, with no fallback and nothing else of the running core.
	Draft bool `json:"draft,omitempty"`
}

func (s *server) handleTestOutbounds(w http.ResponseWriter, r *http.Request) {
	var req outboundTestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, outboundTestMaxBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": err.Error()})
		return
	}
	var tags []string
	for _, tag := range req.Tags {
		if tag != "" && !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	if len(tags) > outboundTestMaxTags || (len(tags) == 0 && len(req.Outbounds) == 0) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": "tags: give 1 to " + strconv.Itoa(outboundTestMaxTags) + " outbound tags"})
		return
	}
	if msg := checkDraftOutbounds(req.Outbounds, tags); msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": msg})
		return
	}

	// The tests take seconds; the lock is only for reading what runs. A core
	// replaced meanwhile fails the tests still going through it, which is the
	// truth about that moment. Drafts need no running core at all.
	s.mu.Lock()
	node, plan := s.node, s.plan
	s.mu.Unlock()
	if node == nil && len(tags) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"detail": "core is not started"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), outboundTestBudget)
	defer cancel()
	here := interfaceLookup()
	results := make([]outboundTestResult, len(tags)+len(req.Outbounds))
	var wg sync.WaitGroup
	for i, tag := range tags {
		results[i] = describeOutbound(node, plan, tag, here)
		if results[i].Skipped != "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			measureOutbound(ctx, node, &results[i])
		}()
	}
	for j, ob := range req.Outbounds {
		res := &results[len(tags)+j]
		*res = describeDraft(ob, here)
		if res.Skipped != "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			measureDraft(ctx, ob, res)
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"url": outboundTestURL, "results": results})
}

// checkDraftOutbounds refuses a request whose drafts can't each be told apart
// and built alone: every one needs a tag of its own, which a tag tested in the
// same request doesn't also name, and has to dial by itself.
func checkDraftOutbounds(drafts []outboundSpec, tags []string) string {
	if len(drafts) > outboundTestMaxDrafts {
		return "outbounds: give at most " + strconv.Itoa(outboundTestMaxDrafts) + " outbounds to test as drafts"
	}
	seen := make(map[string]bool, len(drafts))
	for _, ob := range drafts {
		switch {
		case ob.Tag == "":
			return "outbounds: every draft needs a tag"
		case seen[ob.Tag]:
			return "outbounds: two drafts are tagged " + ob.Tag
		case slices.Contains(tags, ob.Tag):
			return "outbounds: " + ob.Tag + " is asked for both by tag and as a draft"
		case ob.Type == "selector" || ob.Type == "urltest":
			return "outbound " + ob.Tag + ": a draft is tested alone, so it has to dial by itself, not pick among other outbounds"
		case ob.Type == "tor":
			// It would start a tor process, as root, for one request.
			return "outbound " + ob.Tag + ": a tor outbound can't be tested as a draft; save it and test it by tag"
		}
		seen[ob.Tag] = true
	}
	return ""
}

// describeDraft is describeOutbound for a draft: its interface, and whether it
// can be measured at all.
func describeDraft(ob outboundSpec, here func(string) bool) outboundTestResult {
	res := outboundTestResult{Tag: ob.Tag, Draft: true}
	if ob.BindInterface != "" {
		res.Interface, res.InterfaceHere = ob.BindInterface, here(ob.BindInterface)
	}
	if ob.Type == "block" {
		res.Skipped = outboundSkipBlock
	}
	return res
}

// measureDraft builds the draft into a core of its own, inside a test slot and
// the test's own time limit, measures it like a running outbound, and closes
// that core. It goes out over this node's ordinary network, or the interface the
// draft itself names - never through the running core's exits or fallbacks.
func measureDraft(ctx context.Context, ob outboundSpec, res *outboundTestResult) {
	select {
	case outboundTestSlots <- struct{}{}:
		defer func() { <-outboundTestSlots }()
	case <-ctx.Done():
		res.Skipped = outboundSkipBusy
		return
	}
	testCtx, cancel := context.WithTimeout(ctx, outboundTestTimeout)
	defer cancel()
	core, err := startDraftCore(testCtx, ob)
	if err != nil {
		res.Error = clipText("this node can't build the outbound: "+err.Error(), 300)
		return
	}
	defer core.Close()
	if err := core.CanURLTest(ob.Tag); errors.Is(err, nodecore.ErrNoTCPOutbound) {
		res.Skipped = outboundSkipNoTCP
		return
	}
	got, err := core.URLTest(testCtx, ob.Tag, outboundTestURL)
	recordMeasurement(res, got, err, testCtx, ctx)
}

// startDraftCore is a core holding only ob, under its own tag, built through the
// same leafOutboundOptions a running core's outbounds are. It has no inbound and
// counts no traffic towards any user.
func startDraftCore(ctx context.Context, ob outboundSpec) (*nodecore.Node, error) {
	opts, err := leafOutboundOptions(ob, ob.BindInterface)
	if err != nil {
		return nil, err
	}
	core, err := nodecore.New(ctx, sbox.Options{
		Log:       &sbox.LogOptions{Disabled: true},
		Outbounds: []sbox.Outbound{{Type: ob.Type, Tag: ob.Tag, Options: opts}},
		Route:     &sbox.RouteOptions{Final: ob.Tag},
	}, traffic.NewManager())
	if err != nil {
		return nil, err
	}
	if err := core.Start(); err != nil {
		core.Close()
		return nil, err
	}
	return core, nil
}

// describeOutbound fills in everything about tag that needs no dial: whether
// it can be tested at all, its interface, and which member a direct-fallback
// exit is on.
func describeOutbound(node *nodecore.Node, plan nodePlan, tag string, here func(string) bool) outboundTestResult {
	res := outboundTestResult{Tag: tag}
	if iface := plan.boundInterfaces[tag]; iface != "" {
		res.Interface, res.InterfaceHere = iface, here(iface)
	}
	if g, ok := plan.fallbackGroup(tag); ok {
		if via, err := node.SelectedOutbound(g.Group); err == nil {
			res.Via, res.Fallback = via, via == g.Fallback
		}
	}
	switch err := node.CanURLTest(resolveOutboundTag(tag)); {
	case errors.Is(err, nodecore.ErrNoOutbound):
		res.Skipped = outboundSkipMissing
	case errors.Is(err, nodecore.ErrBlockOutbound):
		res.Skipped = outboundSkipBlock
	case errors.Is(err, nodecore.ErrNoTCPOutbound):
		res.Skipped = outboundSkipNoTCP
	}
	return res
}

// measureOutbound waits for a free slot, then runs the test.
func measureOutbound(ctx context.Context, node *nodecore.Node, res *outboundTestResult) {
	select {
	case outboundTestSlots <- struct{}{}:
		defer func() { <-outboundTestSlots }()
	case <-ctx.Done():
		res.Skipped = outboundSkipBusy
		return
	}
	testCtx, cancel := context.WithTimeout(ctx, outboundTestTimeout)
	defer cancel()
	got, err := node.URLTest(testCtx, resolveOutboundTag(res.Tag), outboundTestURL)
	recordMeasurement(res, got, err, testCtx, ctx)
}

// recordMeasurement puts one URLTest's outcome into res. testCtx is the test's
// own limit and ctx the request's, to tell a slow outbound from a request that
// ran out of time.
func recordMeasurement(res *outboundTestResult, got nodecore.URLTestResult, err error, testCtx, ctx context.Context) {
	switch {
	case err == nil:
		res.DelayMS, res.StatusCode = got.Delay.Milliseconds(), got.StatusCode
	case errors.Is(err, nodecore.ErrNoOutbound):
		// gone between describeOutbound and now: the core was replaced
		res.Skipped = outboundSkipMissing
	case errors.Is(testCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		res.Error = "no answer within " + outboundTestTimeout.String()
	case ctx.Err() != nil:
		res.Error = "the test was cut short: " + ctx.Err().Error()
	default:
		res.Error = clipText(err.Error(), 300)
	}
}

// interfaceLookup answers whether this host has an interface, for one
// request: a pinned exit's tunnel may be up (the interface exists), down but
// configured here (a WireGuard config names it), or another server's.
func interfaceLookup() func(string) bool {
	var configured []string
	loaded := false
	return func(name string) bool {
		if _, err := net.InterfaceByName(name); err == nil {
			return true
		}
		if !loaded {
			configured, loaded = hostmetrics.TunnelNames(), true
		}
		return slices.Contains(configured, name)
	}
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
