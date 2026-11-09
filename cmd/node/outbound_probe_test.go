package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/certs"
	"github.com/legendary1205/rapido-go/internal/nodecontrol"
	"github.com/legendary1205/rapido-go/internal/nodecore"
)

// generate204 stands in for the real test URL; hold delays every answer.
func generate204(t *testing.T, hold time.Duration) (inFlightMax *atomic.Int32) {
	t.Helper()
	var now atomic.Int32
	inFlightMax = new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := now.Add(1)
		defer now.Add(-1)
		for {
			max := inFlightMax.Load()
			if n <= max || inFlightMax.CompareAndSwap(max, n) {
				break
			}
		}
		time.Sleep(hold)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	previous := outboundTestURL
	outboundTestURL = server.URL + "/generate_204"
	t.Cleanup(func() { outboundTestURL = previous })
	return inFlightMax
}

func startCore(t *testing.T, srv *server, outbounds ...outboundSpec) {
	t.Helper()
	srv.mu.Lock()
	err := srv.startNodeLocked(startRequest{Core: &coreSpec{Outbounds: outbounds}})
	srv.mu.Unlock()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
}

func testOutbounds(t *testing.T, srv *server, body any) (*httptest.ResponseRecorder, []outboundTestResult) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /outbounds/test", srv.handleTestOutbounds)
	raw, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/outbounds/test", bytes.NewReader(raw)))
	var resp struct {
		URL     string               `json:"url"`
		Results []outboundTestResult `json:"results"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s: %v", rec.Body, err)
		}
		if resp.URL != outboundTestURL {
			t.Errorf("url = %q, want %q", resp.URL, outboundTestURL)
		}
	}
	return rec, resp.Results
}

func TestOutboundTestMeasuresARunningOutboundAndNamesTheRest(t *testing.T) {
	generate204(t, 0)
	srv := newTestServer(t)
	closed := freeTCPPort(t) // nothing listens there
	startCore(t, srv,
		outboundSpec{Tag: "exit", Type: "direct"},
		outboundSpec{Tag: "dead", Type: "socks", Server: "127.0.0.1", ServerPort: int(closed)},
		outboundSpec{Tag: "blackhole", Type: "block"},
	)

	rec, results := testOutbounds(t, srv, outboundTestRequest{Tags: []string{"exit", "gone", "blackhole", "dead", "exit", ""}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body)
	}
	if len(results) != 4 {
		t.Fatalf("results = %+v, want one per distinct tag, in order", results)
	}
	if r := results[0]; r.Tag != "exit" || r.StatusCode != http.StatusNoContent || r.Error != "" || r.Skipped != "" {
		t.Errorf("exit = %+v, want a 204 measurement", r)
	}
	if r := results[1]; r.Tag != "gone" || r.Skipped != outboundSkipMissing || r.StatusCode != 0 {
		t.Errorf("gone = %+v, want skipped as missing", r)
	}
	if r := results[2]; r.Tag != "blackhole" || r.Skipped != outboundSkipBlock || r.Error != "" {
		t.Errorf("blackhole = %+v, want skipped as block, not an error", r)
	}
	if r := results[3]; r.Tag != "dead" || r.Error == "" || r.Skipped != "" || r.StatusCode != 0 {
		t.Errorf("dead = %+v, want the failed dial as an error", r)
	}
}

func TestOutboundTestRefusesWithoutACoreOrTags(t *testing.T) {
	generate204(t, 0)
	srv := newTestServer(t)
	if rec, _ := testOutbounds(t, srv, outboundTestRequest{Tags: []string{"exit"}}); rec.Code != http.StatusConflict {
		t.Errorf("no core: status = %d, want 409", rec.Code)
	}
	startCore(t, srv, outboundSpec{Tag: "exit", Type: "direct"})
	if rec, _ := testOutbounds(t, srv, outboundTestRequest{}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no tags: status = %d, want 422", rec.Code)
	}
	many := make([]string, outboundTestMaxTags+1)
	for i := range many {
		many[i] = "t" + strconv.Itoa(i)
	}
	if rec, _ := testOutbounds(t, srv, outboundTestRequest{Tags: many}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("too many tags: status = %d, want 422", rec.Code)
	}
}

// An exit whose tunnel is not on this node runs over its plain direct member;
// the result has to say so, or a good number would hide a missing tunnel.
func TestOutboundTestReportsAnExitOnItsFallback(t *testing.T) {
	generate204(t, 0)
	srv := newTestServer(t)
	const iface = "rapido-test-gone0"
	startCore(t, srv, outboundSpec{Tag: "exit", Type: "direct", BindInterface: iface, DirectFallback: true})
	health := &fakeHealth{}
	health.setDown(iface, true)
	nodecore.NewSupervisor(srv.node, srv.plan.fallbacks, health, quietLogger()).Reconcile()

	rec, results := testOutbounds(t, srv, outboundTestRequest{Tags: []string{"exit"}})
	if rec.Code != http.StatusOK || len(results) != 1 {
		t.Fatalf("status = %d, results = %+v", rec.Code, results)
	}
	r := results[0]
	if r.Via != "exit~direct" || !r.Fallback {
		t.Errorf("via = %q fallback = %v, want exit~direct on fallback", r.Via, r.Fallback)
	}
	if r.Interface != iface || r.InterfaceHere {
		t.Errorf("interface = %q here = %v, want %s, not on this host", r.Interface, r.InterfaceHere, iface)
	}
	if r.StatusCode != http.StatusNoContent || r.Error != "" {
		t.Errorf("the fallback carries traffic, so the test must pass: %+v", r)
	}
}

// The panel's real client against the real control plane: the mTLS identities
// fit together and both ends read the same wire shape.
func TestOutboundTestOverTheControlPlane(t *testing.T) {
	generate204(t, 0)
	srv := newTestServer(t)
	startCore(t, srv, outboundSpec{Tag: "exit", Type: "direct"})

	ca, key, err := certs.GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := certs.SignNodeCert("node-1", ca.CertPEM, key)
	if err != nil {
		t.Fatal(err)
	}
	nodeCert, err := tls.X509KeyPair([]byte(leaf.CertPEM), []byte(leaf.KeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(ca.CertPEM))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /outbounds/test", srv.handleTestOutbounds)
	control := httptest.NewUnstartedServer(mux)
	control.TLS = controlTLSConfig(nodeCert, pool)
	control.StartTLS()
	defer control.Close()
	addr := control.Listener.Addr().(*net.TCPAddr)

	client, err := nodecontrol.New(ca.CertPEM, ca.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	draft, _ := json.Marshal(outboundSpec{Tag: "new", Type: "direct"})
	resp, err := client.TestOutbounds(context.Background(), addr.IP.String(), addr.Port, nodecontrol.OutboundTestRequest{Tags: []string{"exit", "gone"}, Outbounds: []json.RawMessage{draft}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.URL != outboundTestURL || len(resp.Results) != 3 {
		t.Fatalf("resp = %+v", resp)
	}
	if r := resp.Results[0]; r.Tag != "exit" || r.StatusCode != http.StatusNoContent {
		t.Errorf("exit = %+v", r)
	}
	if r := resp.Results[1]; r.Tag != "gone" || r.Skipped != outboundSkipMissing {
		t.Errorf("gone = %+v", r)
	}
	if r := resp.Results[2]; r.Tag != "new" || !r.Draft || r.StatusCode != http.StatusNoContent {
		t.Errorf("new = %+v, want the draft measured", r)
	}
}

func TestOutboundTestRunsAtMostItsParallelLimit(t *testing.T) {
	inFlightMax := generate204(t, 150*time.Millisecond)
	srv := newTestServer(t)
	count := outboundTestParallel + 4
	var outbounds []outboundSpec
	var tags []string
	for i := range count {
		tag := fmt.Sprintf("exit-%d", i)
		outbounds = append(outbounds, outboundSpec{Tag: tag, Type: "direct"})
		tags = append(tags, tag)
	}
	startCore(t, srv, outbounds...)

	rec, results := testOutbounds(t, srv, outboundTestRequest{Tags: tags})
	if rec.Code != http.StatusOK || len(results) != count {
		t.Fatalf("status = %d, %d results, want 200 and %d", rec.Code, len(results), count)
	}
	for _, r := range results {
		if r.StatusCode != http.StatusNoContent {
			t.Errorf("%s = %+v, want 204", r.Tag, r)
		}
	}
	if got := inFlightMax.Load(); got > outboundTestParallel || got < 2 {
		t.Errorf("%d tests ran at once, want several but at most %d", got, outboundTestParallel)
	}
}

// A draft is built and measured alone, beside the running core and without it:
// the core keeps running the outbounds it had, and its users see nothing.
func TestOutboundTestMeasuresDraftsBesideTheRunningCore(t *testing.T) {
	generate204(t, 0)
	srv := newTestServer(t)
	startCore(t, srv, outboundSpec{Tag: "exit", Type: "direct"})
	running := srv.node
	closed := freeTCPPort(t)

	rec, results := testOutbounds(t, srv, outboundTestRequest{
		Tags: []string{"exit"},
		Outbounds: []outboundSpec{
			{Tag: "new", Type: "direct"},
			{Tag: "dead", Type: "socks", Server: "127.0.0.1", ServerPort: int(closed)},
			{Tag: "nothing", Type: "block"},
			{Tag: "pinned", Type: "direct", BindInterface: "rapido-test-gone0", DirectFallback: true},
			{Tag: "unbuildable", Type: "shadowsocks", Server: "127.0.0.1", ServerPort: 1, Method: "rc4", Password: "x"},
		},
	})
	if rec.Code != http.StatusOK || len(results) != 6 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	if r := results[0]; r.Tag != "exit" || r.Draft || r.StatusCode != http.StatusNoContent {
		t.Errorf("exit = %+v, want the running outbound measured", r)
	}
	if r := results[1]; r.Tag != "new" || !r.Draft || r.StatusCode != http.StatusNoContent || r.Error != "" {
		t.Errorf("new = %+v, want a draft measurement", r)
	}
	if r := results[2]; r.Tag != "dead" || !r.Draft || r.Error == "" {
		t.Errorf("dead = %+v, want the failed dial", r)
	}
	if r := results[3]; r.Tag != "nothing" || !r.Draft || r.Skipped != outboundSkipBlock {
		t.Errorf("nothing = %+v, want skipped as block", r)
	}
	// Alone means no fallback: a draft pinned to an interface is reported on it,
	// never on a direct member. (Whether the dial then fails can't be seen here:
	// sing-box doesn't bind a socket to loopback, where the test URL is.)
	if r := results[4]; !r.Draft || r.Interface != "rapido-test-gone0" || r.InterfaceHere || r.Via != "" || r.Fallback {
		t.Errorf("pinned = %+v, want it reported on its own interface, with no fallback", r)
	}
	if r := results[5]; !r.Draft || !strings.HasPrefix(r.Error, "this node can't build the outbound: ") {
		t.Errorf("unbuildable = %+v, want the core's refusal", r)
	}

	srv.mu.Lock()
	same := srv.node == running
	srv.mu.Unlock()
	if !same {
		t.Fatal("testing drafts replaced the running core")
	}
	if err := running.CanURLTest("new"); !errors.Is(err, nodecore.ErrNoOutbound) {
		t.Errorf("the draft leaked into the running core: %v", err)
	}
	if _, results := testOutbounds(t, srv, outboundTestRequest{Tags: []string{"exit"}}); len(results) != 1 || results[0].StatusCode != http.StatusNoContent {
		t.Errorf("the running core no longer answers: %+v", results)
	}
}

func TestOutboundTestTakesDraftsWithoutARunningCore(t *testing.T) {
	generate204(t, 0)
	srv := newTestServer(t)
	rec, results := testOutbounds(t, srv, outboundTestRequest{Outbounds: []outboundSpec{{Tag: "new", Type: "direct"}}})
	if rec.Code != http.StatusOK || len(results) != 1 || results[0].StatusCode != http.StatusNoContent || !results[0].Draft {
		t.Fatalf("status = %d, results = %+v", rec.Code, results)
	}
	if rec, _ := testOutbounds(t, srv, outboundTestRequest{Tags: []string{"exit"}, Outbounds: []outboundSpec{{Tag: "new", Type: "direct"}}}); rec.Code != http.StatusConflict {
		t.Errorf("a tag with no core: %d, want 409", rec.Code)
	}
}

func TestOutboundTestRefusesDraftsItCannotTellApartOrBuildAlone(t *testing.T) {
	srv := newTestServer(t)
	startCore(t, srv, outboundSpec{Tag: "exit", Type: "direct"})
	many := make([]outboundSpec, outboundTestMaxDrafts+1)
	for i := range many {
		many[i] = outboundSpec{Tag: fmt.Sprintf("d%d", i), Type: "direct"}
	}
	for name, req := range map[string]outboundTestRequest{
		"no tag":          {Outbounds: []outboundSpec{{Type: "direct"}}},
		"one tag twice":   {Outbounds: []outboundSpec{{Tag: "a", Type: "direct"}, {Tag: "a", Type: "direct"}}},
		"by tag as well":  {Tags: []string{"exit"}, Outbounds: []outboundSpec{{Tag: "exit", Type: "direct"}}},
		"a group":         {Outbounds: []outboundSpec{{Tag: "g", Type: "selector", Outbounds: []string{"direct"}}}},
		"tor":             {Outbounds: []outboundSpec{{Tag: "t", Type: "tor"}}},
		"too many":        {Outbounds: many},
		"nothing to test": {},
	} {
		if rec, _ := testOutbounds(t, srv, req); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s, want 422", name, rec.Code, rec.Body)
		}
	}
}

// Drafts share the node's test slots with everything else: a full batch of them
// beside running outbounds still never runs more than the limit at once.
func TestOutboundTestRunsDraftsWithinTheParallelLimit(t *testing.T) {
	inFlightMax := generate204(t, 150*time.Millisecond)
	srv := newTestServer(t)
	var running []outboundSpec
	var tags []string
	for i := range 4 {
		tag := fmt.Sprintf("exit-%d", i)
		running = append(running, outboundSpec{Tag: tag, Type: "direct"})
		tags = append(tags, tag)
	}
	startCore(t, srv, running...)
	var drafts []outboundSpec
	for i := range outboundTestMaxDrafts {
		drafts = append(drafts, outboundSpec{Tag: fmt.Sprintf("draft-%d", i), Type: "direct"})
	}
	rec, results := testOutbounds(t, srv, outboundTestRequest{Tags: tags, Outbounds: drafts})
	if rec.Code != http.StatusOK || len(results) != len(tags)+len(drafts) {
		t.Fatalf("status = %d, %d results", rec.Code, len(results))
	}
	for _, r := range results {
		if r.StatusCode != http.StatusNoContent {
			t.Errorf("%s = %+v, want 204", r.Tag, r)
		}
	}
	if got := inFlightMax.Load(); got > outboundTestParallel || got < 2 {
		t.Errorf("%d tests ran at once, want several but at most %d", got, outboundTestParallel)
	}
}
