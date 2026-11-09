package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/certs"
	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/nodecontrol"
)

// fakeTester answers like a node would: per node, a fixed reply or error.
type fakeTester struct {
	mu      sync.Mutex
	asked   map[string][]string
	replies map[string][]nodecontrol.OutboundTestResult
	errs    map[string]error
	hold    time.Duration

	now, max atomic.Int32
}

func (f *fakeTester) TestOutbounds(ctx context.Context, node generated.Node, tags []string) (nodecontrol.OutboundTestResponse, error) {
	n := f.now.Add(1)
	defer f.now.Add(-1)
	for {
		m := f.max.Load()
		if n <= m || f.max.CompareAndSwap(m, n) {
			break
		}
	}
	f.mu.Lock()
	if f.asked == nil {
		f.asked = map[string][]string{}
	}
	f.asked[node.Name] = tags
	f.mu.Unlock()
	if f.hold > 0 {
		time.Sleep(f.hold)
	}
	if err := f.errs[node.Name]; err != nil {
		return nodecontrol.OutboundTestResponse{}, err
	}
	return nodecontrol.OutboundTestResponse{URL: "https://www.gstatic.com/generate_204", Results: f.replies[node.Name]}, nil
}

func probeNodes(names ...string) []generated.Node {
	nodes := make([]generated.Node, len(names))
	for i, name := range names {
		nodes[i] = generated.Node{ID: int32(i + 1), Name: name, Status: "connected"}
	}
	return nodes
}

func TestProbeOutboundsLaysOutEveryNodeForEveryTag(t *testing.T) {
	fake := &fakeTester{
		replies: map[string][]nodecontrol.OutboundTestResult{
			"de": {
				{Tag: "exit-de", DelayMS: 74, StatusCode: 204, Interface: "wg-de", InterfaceHere: true, Via: "exit-de~wg"},
				{Tag: "blackhole", Skipped: "block"},
			},
			"nl": {
				{Tag: "exit-de", DelayMS: 130, StatusCode: 204, Interface: "wg-de", Via: "exit-de~direct", Fallback: true},
				// blackhole missing from the answer
			},
		},
		errs: map[string]error{"fr": errors.New("cannot reach the node's control port 192.0.2.3:62051: connection refused")},
	}
	resp := probeOutbounds(context.Background(), fake, probeNodes("de", "nl", "fr"), []string{"exit-de", "blackhole"})

	if resp.URL != "https://www.gstatic.com/generate_204" {
		t.Errorf("url = %q", resp.URL)
	}
	if len(resp.Nodes) != 3 || resp.Nodes[2].Error == "" || resp.Nodes[0].Error != "" {
		t.Errorf("nodes = %+v, want fr's error and no other", resp.Nodes)
	}
	type cell struct{ tag, node string }
	got := map[cell]outboundProbeResult{}
	var order []cell
	for _, r := range resp.Results {
		got[cell{r.Tag, r.Node}] = r
		order = append(order, cell{r.Tag, r.Node})
	}
	want := []cell{{"exit-de", "de"}, {"exit-de", "nl"}, {"exit-de", "fr"}, {"blackhole", "de"}, {"blackhole", "nl"}, {"blackhole", "fr"}}
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if r := got[cell{"exit-de", "de"}]; r.NodeID != 1 || r.DelayMS != 74 || r.StatusCode != 204 || r.Fallback {
		t.Errorf("exit-de on de = %+v", r)
	}
	if r := got[cell{"exit-de", "nl"}]; !r.Fallback || r.Via != "exit-de~direct" || r.InterfaceHere {
		t.Errorf("exit-de on nl = %+v, want it reported on its fallback", r)
	}
	if r := got[cell{"exit-de", "fr"}]; r.Error != resp.Nodes[2].Error || r.StatusCode != 0 {
		t.Errorf("exit-de on fr = %+v, want the node's error", r)
	}
	if r := got[cell{"blackhole", "de"}]; r.Skipped != "block" || r.Error != "" {
		t.Errorf("blackhole on de = %+v, want skipped, not an error", r)
	}
	if r := got[cell{"blackhole", "nl"}]; r.Error == "" {
		t.Errorf("blackhole on nl = %+v, want an error for the missing answer", r)
	}
	for _, name := range []string{"de", "nl", "fr"} {
		if !slices.Equal(fake.asked[name], []string{"exit-de", "blackhole"}) {
			t.Errorf("%s was asked for %v", name, fake.asked[name])
		}
	}
}

func TestProbeOutboundsAsksABoundedNumberOfNodesAtOnce(t *testing.T) {
	fake := &fakeTester{hold: 40 * time.Millisecond}
	var names []string
	for i := range outboundProbeNodeParallel + 5 {
		names = append(names, "node-"+string(rune('a'+i)))
	}
	resp := probeOutbounds(context.Background(), fake, probeNodes(names...), []string{"exit"})
	if len(resp.Results) != len(names) {
		t.Fatalf("%d results, want %d", len(resp.Results), len(names))
	}
	if got := fake.max.Load(); got > outboundProbeNodeParallel || got < 2 {
		t.Errorf("%d nodes asked at once, want several but at most %d", got, outboundProbeNodeParallel)
	}
}

func TestProbeOutboundsGivesUpWhenTheRequestEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp := probeOutbounds(ctx, blockingTester{}, probeNodes("de"), []string{"exit"})
	if len(resp.Results) != 1 || resp.Results[0].Error == "" {
		t.Errorf("results = %+v, want an error once the request is over", resp.Results)
	}
}

// blockingTester honours ctx the way the real client does.
type blockingTester struct{}

func (b blockingTester) TestOutbounds(ctx context.Context, node generated.Node, tags []string) (nodecontrol.OutboundTestResponse, error) {
	<-ctx.Done()
	return nodecontrol.OutboundTestResponse{}, ctx.Err()
}

func TestOutboundTestRouteNeedsSudo(t *testing.T) {
	router, sudoToken, h := newTestRouterAndHandler(t)
	h.outboundTester = &fakeTester{}
	body := map[string]any{"tags": []string{"exit"}}

	if resp := doRequest(t, router, "POST", "/api/core/outbounds/test", "", body); resp.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", resp.Code)
	}
	if resp := doRequest(t, router, "POST", "/api/admin", sudoToken, map[string]any{
		"username": "probe_reseller", "password": "SomePassword123", "is_sudo": false,
	}); resp.Code != http.StatusOK {
		t.Fatalf("create admin: %d %s", resp.Code, resp.Raw)
	}
	reseller := loginAs(t, router, "probe_reseller", "SomePassword123")
	if resp := doRequest(t, router, "POST", "/api/core/outbounds/test", reseller, body); resp.Code != http.StatusForbidden {
		t.Errorf("non-sudo admin: %d, want 403", resp.Code)
	}
}

func TestOutboundTestRouteAsksTheConnectedNodes(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	ctx := context.Background()
	q := h.store.Queries
	addNode := func(name, status string) generated.Node {
		n, err := q.CreateNode(ctx, generated.CreateNodeParams{Name: name, Address: "192.0.2.1", Port: 62050, ApiPort: 62051, UsageCoefficient: 1, CoreOverrides: []byte("{}")})
		if err != nil {
			t.Fatal(err)
		}
		if status != "connecting" {
			if _, err := h.store.Pool.Exec(ctx, "UPDATE nodes SET status = $1 WHERE id = $2", status, n.ID); err != nil {
				t.Fatal(err)
			}
		}
		return n
	}
	de := addNode("de", "connected")
	addNode("nl", "connected")
	addNode("idle", "connecting")
	off := addNode("off", "disabled")

	fake := &fakeTester{
		replies: map[string][]nodecontrol.OutboundTestResult{
			"de": {{Tag: "exit", DelayMS: 74, StatusCode: 204}},
			"nl": {{Tag: "exit", Error: "no answer within 5s"}},
		},
	}
	h.outboundTester = fake

	if resp := doRequest(t, router, "POST", "/api/core/outbounds/test", token, map[string]any{"tags": []string{}}); resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("no tags: %d, want 422", resp.Code)
	}
	resp := doRequest(t, router, "POST", "/api/core/outbounds/test", token, map[string]any{"tags": []string{"exit", " exit "}})
	if resp.Code != http.StatusOK {
		t.Fatalf("all nodes: %d %s", resp.Code, resp.Raw)
	}
	results, _ := resp.Body["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results = %s, want exit on de and nl only", resp.Raw)
	}
	first := results[0].(map[string]any)
	if first["node"] != "de" || first["delay_ms"] != float64(74) || first["status_code"] != float64(204) {
		t.Errorf("first = %v", first)
	}
	if second := results[1].(map[string]any); second["node"] != "nl" || second["error"] != "no answer within 5s" {
		t.Errorf("second = %v", second)
	}
	if _, asked := fake.asked["idle"]; asked {
		t.Error("a node that is not connected was asked")
	}

	fake.asked = nil
	resp = doRequest(t, router, "POST", "/api/core/outbounds/test", token, map[string]any{"tags": []string{"exit"}, "node_id": de.ID})
	if resp.Code != http.StatusOK || len(fake.asked) != 1 || fake.asked["de"] == nil {
		t.Errorf("one node: %d %s, asked %v", resp.Code, resp.Raw, fake.asked)
	}
	if resp := doRequest(t, router, "POST", "/api/core/outbounds/test", token, map[string]any{"tags": []string{"exit"}, "node_id": off.ID}); resp.Code != http.StatusConflict {
		t.Errorf("disabled node: %d, want 409", resp.Code)
	}
	if resp := doRequest(t, router, "POST", "/api/core/outbounds/test", token, map[string]any{"tags": []string{"exit"}, "node_id": 9999}); resp.Code != http.StatusNotFound {
		t.Errorf("unknown node: %d, want 404", resp.Code)
	}
}

// With no fake, the route talks to the node over mutual TLS as the panel's CA,
// and accepts the node for a certificate that CA issued.
func TestOutboundTestRouteUsesTheControlPlane(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	ctx := context.Background()
	ca, err := h.store.Queries.GetTLS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	caKey, err := certs.ParseRSAPrivateKeyPEM(ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := certs.SignNodeCert("de", ca.Certificate, caKey)
	if err != nil {
		t.Fatal(err)
	}
	nodeCert, err := tls.X509KeyPair([]byte(leaf.CertPEM), []byte(leaf.KeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(ca.Certificate))
	var sawPanel atomic.Bool
	control := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPanel.Store(len(r.TLS.PeerCertificates) > 0 && r.TLS.PeerCertificates[0].IsCA)
		w.Write([]byte(`{"url":"https://www.gstatic.com/generate_204","results":[{"tag":"exit","delay_ms":74,"status_code":204,"interface_here":false,"fallback":false}]}`))
	}))
	control.TLS = &tls.Config{Certificates: []tls.Certificate{nodeCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	control.StartTLS()
	defer control.Close()
	addr := control.Listener.Addr().(*net.TCPAddr)

	node, err := h.store.Queries.CreateNode(ctx, generated.CreateNodeParams{Name: "de", Address: "127.0.0.1", Port: 62050, ApiPort: int32(addr.Port), UsageCoefficient: 1, CoreOverrides: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	resp := doRequest(t, router, "POST", "/api/core/outbounds/test", token, map[string]any{"tags": []string{"exit"}, "node_id": node.ID})
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %s", resp.Code, resp.Raw)
	}
	results, _ := resp.Body["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["delay_ms"] != float64(74) {
		t.Errorf("results = %s", resp.Raw)
	}
	if !sawPanel.Load() {
		t.Error("the node did not see the panel's CA identity")
	}
}
