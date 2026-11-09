package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
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
	drafts  map[string][]json.RawMessage
	replies map[string][]nodecontrol.OutboundTestResult
	errs    map[string]error
	hold    time.Duration

	now, max atomic.Int32
}

func (f *fakeTester) TestOutbounds(ctx context.Context, node generated.Node, req nodecontrol.OutboundTestRequest) (nodecontrol.OutboundTestResponse, error) {
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
	f.asked[node.Name] = req.Tags
	if f.drafts == nil {
		f.drafts = map[string][]json.RawMessage{}
	}
	f.drafts[node.Name] = req.Outbounds
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
	resp := probeOutbounds(context.Background(), fake, probeNodes("de", "nl", "fr"), outboundProbe{tags: []string{"exit-de", "blackhole"}})

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
	resp := probeOutbounds(context.Background(), fake, probeNodes(names...), outboundProbe{tags: []string{"exit"}})
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
	resp := probeOutbounds(ctx, blockingTester{}, probeNodes("de"), outboundProbe{tags: []string{"exit"}})
	if len(resp.Results) != 1 || resp.Results[0].Error == "" {
		t.Errorf("results = %+v, want an error once the request is over", resp.Results)
	}
}

// blockingTester honours ctx the way the real client does.
type blockingTester struct{}

func (b blockingTester) TestOutbounds(ctx context.Context, node generated.Node, req nodecontrol.OutboundTestRequest) (nodecontrol.OutboundTestResponse, error) {
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

	// A draft goes to the node whole, checked first as saving would check it.
	fake.asked, fake.drafts = nil, nil
	fake.replies["de"] = []nodecontrol.OutboundTestResult{{Tag: "new", DelayMS: 180, StatusCode: 204, Draft: true}}
	resp = doRequest(t, router, "POST", "/api/core/outbounds/test", token, map[string]any{"outbounds": []outboundDTO{realityDraft("new")}, "node_id": de.ID})
	if resp.Code != http.StatusOK || len(fake.drafts["de"]) != 1 {
		t.Fatalf("draft: %d %s, sent %s", resp.Code, resp.Raw, fake.drafts["de"])
	}
	if r := resp.Body["results"].([]any)[0].(map[string]any); r["draft"] != true || r["delay_ms"] != float64(180) {
		t.Errorf("draft result = %v", r)
	}
	broken := realityDraft("new")
	broken.RealityPublicKey = "not-a-key"
	resp = doRequest(t, router, "POST", "/api/core/outbounds/test", token, map[string]any{"outbounds": []outboundDTO{broken}})
	if detail, _ := resp.Body["detail"].(string); resp.Code != http.StatusUnprocessableEntity || !strings.Contains(detail, "reality_public_key") {
		t.Errorf("broken draft: %d %s, want 422 naming the key", resp.Code, resp.Raw)
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

// realityDraft is an outbound to another provider's REALITY server, the shape a
// pasted share link fills in.
func realityDraft(tag string) outboundDTO {
	return outboundDTO{
		Tag: tag, Type: "vless", Server: "203.0.113.9", ServerPort: 443,
		UUID: "1b0f8d6e-3c2a-4e5f-9a7b-0c1d2e3f4a5b", Flow: "xtls-rprx-vision", TLSEnabled: true,
		TLSServerName: "www.example.com", RealityPublicKey: "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0", RealityShortID: "0123abcd",
	}
}

func TestDraftOutboundsAreCheckedAsSavingWouldCheckThem(t *testing.T) {
	if _, msg := draftOutbounds([]outboundDTO{realityDraft("new")}, []string{"exit"}); msg != "" {
		t.Fatalf("a valid draft was refused: %s", msg)
	}
	bad := realityDraft("new")
	bad.RealityShortID = "abc"
	many := make([]outboundDTO, outboundProbeMaxDrafts+1)
	for i := range many {
		many[i] = outboundDTO{Tag: "d" + strconv.Itoa(i), Type: "direct"}
	}
	for name, tc := range map[string]struct {
		drafts []outboundDTO
		tags   []string
		want   string
	}{
		"what saving refuses": {[]outboundDTO{bad}, nil, "reality_short_id"},
		"a reserved tag":      {[]outboundDTO{{Tag: "direct", Type: "direct"}}, nil, "reserved"},
		"two of one tag":      {[]outboundDTO{{Tag: "a", Type: "direct"}, {Tag: "a", Type: "direct"}}, nil, "duplicate"},
		"a group":             {[]outboundDTO{{Tag: "g", Type: "urltest", Outbounds: []string{"direct"}}}, nil, "dial by itself"},
		"tor":                 {[]outboundDTO{{Tag: "t", Type: "tor"}}, nil, "tor"},
		"a tag also by name":  {[]outboundDTO{{Tag: "exit", Type: "direct"}}, []string{"exit"}, "both by tag and as a draft"},
		"too many":            {many, nil, "at most 8"},
	} {
		if _, msg := draftOutbounds(tc.drafts, tc.tags); !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q, want it to say %q", name, msg, tc.want)
		}
	}
}

func TestProbeOutboundsSendsDraftsAsNodeConfigDoesAndLabelsThem(t *testing.T) {
	draft := realityDraft("new")
	fake := &fakeTester{
		replies: map[string][]nodecontrol.OutboundTestResult{
			"de": {{Tag: "exit", DelayMS: 74, StatusCode: 204}, {Tag: "new", DelayMS: 180, StatusCode: 204, Draft: true}},
			// An agent from before drafts: it tests the tag and leaves the draft out.
			"nl": {{Tag: "exit", DelayMS: 90, StatusCode: 204}},
		},
	}
	resp := probeOutbounds(context.Background(), fake, probeNodes("de", "nl"), outboundProbe{tags: []string{"exit"}, drafts: []outboundDTO{draft}})

	want, _ := json.Marshal(draft)
	if got := fake.drafts["de"]; len(got) != 1 || string(got[0]) != string(want) {
		t.Fatalf("the node was sent %s, want the outbound as node-config sends it: %s", got, want)
	}
	if !slices.Equal(fake.asked["de"], []string{"exit"}) {
		t.Errorf("tags sent = %v", fake.asked["de"])
	}
	if len(resp.Results) != 4 {
		t.Fatalf("results = %+v", resp.Results)
	}
	if r := resp.Results[0]; r.Tag != "exit" || r.Draft {
		t.Errorf("first = %+v, want exit by tag", r)
	}
	if r := resp.Results[2]; r.Tag != "new" || r.Node != "de" || !r.Draft || r.DelayMS != 180 {
		t.Errorf("draft on de = %+v", r)
	}
	if r := resp.Results[3]; r.Tag != "new" || r.Node != "nl" || !r.Draft || r.Error != errDraftsNeedNewerAgent.Error() {
		t.Errorf("draft on the old agent = %+v, want it called too old", r)
	}
}

func TestProbeOutboundsCallsAnAgentThatRefusesOnlyDraftsTooOld(t *testing.T) {
	fake := &fakeTester{errs: map[string]error{"de": &nodecontrol.StatusError{Code: http.StatusUnprocessableEntity, Detail: "tags: give 1 to 64 outbound tags"}}}
	resp := probeOutbounds(context.Background(), fake, probeNodes("de"), outboundProbe{drafts: []outboundDTO{{Tag: "new", Type: "direct"}}})
	if len(resp.Results) != 1 || resp.Results[0].Error != errDraftsNeedNewerAgent.Error() || !resp.Results[0].Draft {
		t.Errorf("results = %+v", resp.Results)
	}
	if resp.Nodes[0].Error != errDraftsNeedNewerAgent.Error() {
		t.Errorf("node = %+v", resp.Nodes[0])
	}
}
