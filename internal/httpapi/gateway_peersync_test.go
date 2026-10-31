package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/legendary1205/rapido-go/internal/gatewayclient"
)

// fakePeer is the other panel, as far as the wire goes: what a pass reads from it is
// whatever a test puts here, and what the pass sends it is kept for the test to read.
type fakePeer struct {
	mu       sync.Mutex
	server   *httptest.Server
	protocol int
	hosts    []gatewayclient.EffectiveHost
	refuse   map[string]string    // username to the reason it is refused
	counters map[string]int64     // username to what the peer has counted
	baseline map[string]int64     // username to the counter a first push answers with
	pushed   []gatewaySyncPayload // every push, in the order they arrived
	holding  map[string]bool      // what it holds now
	purged   []string             // origin IDs it was told to purge
	broken   bool                 // answer every call with a 500
}

func newFakePeer(t *testing.T) *fakePeer {
	t.Helper()
	p := &fakePeer{
		protocol: gatewayclient.ProtocolVersion, refuse: map[string]string{}, counters: map[string]int64{},
		baseline: map[string]int64{}, holding: map[string]bool{},
		hosts: []gatewayclient.EffectiveHost{{
			Tag: "peer-vless", Protocol: "vless", Network: "tcp", Port: 443, Address: "peer.example.test",
			Security: "none", Remark: "Peer Host",
		}},
	}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakePeer) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	reply := func(code int, v any) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	if p.broken {
		reply(http.StatusInternalServerError, map[string]string{"detail": "broken"})
		return
	}
	switch r.URL.Path {
	case "/api/internal/gateway/ping":
		reply(http.StatusOK, gatewayclient.PingResult{PanelName: "fake", Protocol: p.protocol})
	case "/api/internal/gateway/status":
		reply(http.StatusOK, gatewayclient.StatusResult{Crowdedness: 7, Hosts: p.hosts})
	case "/api/internal/gateway/usage":
		users := []gatewayclient.PeerUsage{}
		if r.URL.Query().Get("after") == "0" {
			id := int64(1)
			for name, counter := range p.counters {
				users = append(users, gatewayclient.PeerUsage{ID: id, Username: name, UsedTraffic: counter})
				id++
			}
		}
		reply(http.StatusOK, map[string]any{"users": users})
	case "/api/internal/gateway/users/sync":
		var req gatewaySyncPayload
		json.NewDecoder(r.Body).Decode(&req)
		p.pushed = append(p.pushed, req)
		if reason, refused := p.refuse[req.Username]; refused {
			reply(http.StatusConflict, map[string]any{"ok": false, "detail": reason})
			return
		}
		p.holding[req.Username] = !req.Deleted
		reply(http.StatusOK, map[string]any{"ok": true, "used_traffic": p.baseline[req.Username]})
	case "/api/internal/gateway/users/purge":
		var req struct {
			OriginID string `json:"origin_id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		p.purged = append(p.purged, req.OriginID)
		reply(http.StatusOK, map[string]any{"deleted": len(p.holding)})
	default:
		reply(http.StatusNotFound, map[string]string{"detail": "no such route"})
	}
}

func (p *fakePeer) pushes(username string) []gatewaySyncPayload {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []gatewaySyncPayload
	for _, push := range p.pushed {
		if push.Username == username {
			out = append(out, push)
		}
	}
	return out
}

func registerFakePeer(t *testing.T, router http.Handler, token string, peer *fakePeer, enabled bool) int32 {
	t.Helper()
	resp := doRequest(t, router, "POST", "/api/settings/gateway/peers", token, map[string]interface{}{
		"name": "Fake", "base_url": peer.server.URL, "secret": "peer-secret", "enabled": enabled,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("register peer: %d %v", resp.Code, resp.Body)
	}
	id, _ := resp.Body["id"].(float64)
	return int32(id)
}

func createGatewayUser(t *testing.T, router http.Handler, token, username string, limit int64) string {
	t.Helper()
	body := map[string]interface{}{"username": username, "proxies": map[string]interface{}{"vless": map[string]interface{}{}}}
	if limit > 0 {
		body["data_limit"] = limit
	}
	created := doRequest(t, router, "POST", "/api/user", token, body)
	if created.Code != http.StatusOK {
		t.Fatalf("create %s: %d %v", username, created.Code, created.Body)
	}
	url, _ := created.Body["subscription_url"].(string)
	return url
}

func peerView(t *testing.T, router http.Handler, token string, id int32) map[string]interface{} {
	t.Helper()
	resp := doRequest(t, router, "GET", "/api/settings/gateway/peers", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list peers: %d", resp.Code)
	}
	var peers []map[string]interface{}
	if err := json.Unmarshal(resp.Raw, &peers); err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		if got, _ := p["id"].(float64); int32(got) == id {
			return p
		}
	}
	t.Fatalf("peer %d not listed", id)
	return nil
}

func usedTraffic(t *testing.T, router http.Handler, token, username string) int64 {
	t.Helper()
	resp := doRequest(t, router, "GET", "/api/user/"+username, token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("get %s: %d", username, resp.Code)
	}
	used, _ := resp.Body["used_traffic"].(float64)
	return int64(used)
}

// A peer's servers reach the people it took, all of them, and nobody else: the fault
// this replaced was a whole panel being offered servers that refused it.
func TestGatewayPassPushesEveryUserAndOffersServersOnlyToThoseTheyAccept(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	accepted := createGatewayUser(t, router, token, "gw_accepted", 0)
	refused := createGatewayUser(t, router, token, "gw_refused", 0)

	peer := newFakePeer(t)
	peer.refuse["gw_refused"] = "a real local user with this username already exists here"
	id := registerFakePeer(t, router, token, peer, true)

	if links := decodeV2raySubscription(t, router, accepted); len(links) != 1 {
		t.Fatalf("before any pass: %d links, want only the local one: %v", len(links), links)
	}

	handler.SyncGatewayPeers(context.Background())

	if got := peer.pushes("gw_accepted"); len(got) != 1 || got[0].OriginID == "" {
		t.Fatalf("pushes of gw_accepted = %+v, want one that says who it is from", got)
	}
	if links := decodeV2raySubscription(t, router, accepted); len(links) != 2 {
		t.Errorf("accepted user: %d links, want local + peer: %v", len(links), links)
	}
	if links := decodeV2raySubscription(t, router, refused); len(links) != 1 {
		t.Errorf("refused user: %d links, want only local: %v", len(links), links)
	}

	view := peerView(t, router, token, id)
	if view["synced_users"] != float64(1) || view["rejected_users"] != float64(1) || view["total_users"] != float64(2) {
		t.Errorf("counts = %v/%v of %v, want 1/1 of 2", view["synced_users"], view["rejected_users"], view["total_users"])
	}
	if view["last_seen_at"] == nil || view["last_error"] != "" {
		t.Errorf("health = %v / %q", view["last_seen_at"], view["last_error"])
	}
	if hosts, _ := view["hosts"].([]interface{}); len(hosts) != 1 {
		t.Errorf("hosts = %v", view["hosts"])
	}

	rejected := doRequest(t, router, "GET", "/api/settings/gateway/peers/"+itoa32(id)+"/rejected", token, nil)
	if !strings.Contains(string(rejected.Raw), "gw_refused") || !strings.Contains(string(rejected.Raw), "already exists") {
		t.Errorf("rejected list = %s", rejected.Raw)
	}

	// Sorting it out on the other panel, then asking again.
	delete(peer.refuse, "gw_refused")
	if resp := doRequest(t, router, "POST", "/api/settings/gateway/peers/"+itoa32(id)+"/retry", token, nil); resp.Code != http.StatusOK {
		t.Fatalf("retry: %d %v", resp.Code, resp.Body)
	}
	handler.SyncGatewayPeers(context.Background())
	if links := decodeV2raySubscription(t, router, refused); len(links) != 2 {
		t.Errorf("after the retry: %d links, want local + peer: %v", len(links), links)
	}
}

func TestGatewayOnlyPushesWhatChanged(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	createGatewayUser(t, router, token, "gw_steady", 0)
	createGatewayUser(t, router, token, "gw_changing", 0)
	peer := newFakePeer(t)
	registerFakePeer(t, router, token, peer, true)

	handler.SyncGatewayPeers(context.Background())
	handler.SyncGatewayPeers(context.Background())
	if got := len(peer.pushes("gw_steady")); got != 1 {
		t.Errorf("an unchanged user was pushed %d times, want once", got)
	}

	// By a route no code path has to remember to report: the review job, in real life.
	if _, err := handler.store.Pool.Exec(context.Background(), "UPDATE users SET status='limited' WHERE username='gw_changing'"); err != nil {
		t.Fatal(err)
	}
	handler.SyncGatewayPeers(context.Background())
	got := peer.pushes("gw_changing")
	if len(got) != 2 || got[1].Status != "limited" {
		t.Errorf("pushes after the change = %+v, want a second one saying limited", got)
	}
	if got := len(peer.pushes("gw_steady")); got != 1 {
		t.Errorf("the other user was pushed again: %d", got)
	}
}

func TestGatewayTakesBackADeletedUser(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	createGatewayUser(t, router, token, "gw_leaving", 0)
	peer := newFakePeer(t)
	id := registerFakePeer(t, router, token, peer, true)
	handler.SyncGatewayPeers(context.Background())

	// As "delete expired" or the auto-delete policy would: no handler is told.
	if _, err := handler.store.Pool.Exec(context.Background(), "DELETE FROM users WHERE username='gw_leaving'"); err != nil {
		t.Fatal(err)
	}
	handler.SyncGatewayPeers(context.Background())
	got := peer.pushes("gw_leaving")
	if len(got) != 2 || !got[1].Deleted {
		t.Fatalf("pushes = %+v, want a deletion after the create", got)
	}
	if view := peerView(t, router, token, id); view["synced_users"] != float64(0) {
		t.Errorf("still counted as held: %v", view["synced_users"])
	}
}

// Shared limit: what the peer counts adds up here, once, and to the admin too.
func TestGatewayTrafficCountsOnceTowardsTheUsersLimit(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	ctx := context.Background()
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	createGatewayUser(t, router, token, "gw_quota", 1000)

	var adminID int32
	if err := handler.store.Pool.QueryRow(ctx, "INSERT INTO admins (username, hashed_password) VALUES ('gw_seller','x') RETURNING id").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.store.Pool.Exec(ctx, "UPDATE users SET admin_id=$1 WHERE username='gw_quota'", adminID); err != nil {
		t.Fatal(err)
	}

	peer := newFakePeer(t)
	// A copy that had counted traffic before this panel started adding it up.
	peer.baseline["gw_quota"] = 100
	registerFakePeer(t, router, token, peer, true)
	handler.SyncGatewayPeers(ctx)
	if got := usedTraffic(t, router, token, "gw_quota"); got != 0 {
		t.Fatalf("switching a peer on charged %d for traffic from before", got)
	}

	peer.mu.Lock()
	peer.counters["gw_quota"] = 400
	peer.mu.Unlock()
	handler.SyncGatewayPeers(ctx)
	if got := usedTraffic(t, router, token, "gw_quota"); got != 300 {
		t.Errorf("used = %d, want 300", got)
	}
	var sold int64
	if err := handler.store.Pool.QueryRow(ctx, "SELECT users_usage FROM admins WHERE id=$1", adminID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 300 {
		t.Errorf("the admin was charged %d, want 300", sold)
	}

	handler.SyncGatewayPeers(ctx)
	if got := usedTraffic(t, router, token, "gw_quota"); got != 300 {
		t.Errorf("reading the same counter again: used = %d, want 300", got)
	}

	// The copy was made anew on the peer, and started counting from nothing.
	peer.mu.Lock()
	peer.counters["gw_quota"] = 50
	peer.mu.Unlock()
	handler.SyncGatewayPeers(ctx)
	if got := usedTraffic(t, router, token, "gw_quota"); got != 350 {
		t.Errorf("after the counter went down: used = %d, want 350", got)
	}

	// Traffic on the first connection starts a waiting plan, wherever it happens.
	var online bool
	if err := handler.store.Pool.QueryRow(ctx, "SELECT online_at IS NOT NULL FROM users WHERE username='gw_quota'").Scan(&online); err != nil {
		t.Fatal(err)
	}
	if !online {
		t.Error("traffic on the peer didn't mark the user online here")
	}
}

func TestDisabledPeerGetsNoNewUsersAndOffersNoServers(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	url := createGatewayUser(t, router, token, "gw_waiting", 0)
	peer := newFakePeer(t)
	id := registerFakePeer(t, router, token, peer, false)

	handler.SyncGatewayPeers(context.Background())
	if got := peer.pushes("gw_waiting"); len(got) != 0 {
		t.Fatalf("a peer nobody switched on got %+v", got)
	}
	// It can still be looked at before anyone decides.
	if hosts, _ := peerView(t, router, token, id)["hosts"].([]interface{}); len(hosts) != 1 {
		t.Errorf("hosts of a disabled peer = %v, want them shown", hosts)
	}

	resp := doRequest(t, router, "PUT", "/api/settings/gateway/peers/"+itoa32(id), token, map[string]interface{}{
		"name": "Fake", "base_url": peer.server.URL, "secret": "peer-secret", "enabled": true,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("enable: %d %v", resp.Code, resp.Body)
	}
	handler.SyncGatewayPeers(context.Background())
	if links := decodeV2raySubscription(t, router, url); len(links) != 2 {
		t.Errorf("after enabling: %d links, want local + peer", len(links))
	}

	// Off again: its servers go, and what it holds stays current, as those configs
	// still work there for anyone who has them.
	resp = doRequest(t, router, "PUT", "/api/settings/gateway/peers/"+itoa32(id), token, map[string]interface{}{
		"name": "Fake", "base_url": peer.server.URL, "secret": "peer-secret", "enabled": false,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("disable: %d %v", resp.Code, resp.Body)
	}
	if links := decodeV2raySubscription(t, router, url); len(links) != 1 {
		t.Errorf("after disabling: %d links, want only local", len(links))
	}
	if _, err := handler.store.Pool.Exec(context.Background(), "UPDATE users SET status='disabled' WHERE username='gw_waiting'"); err != nil {
		t.Fatal(err)
	}
	createGatewayUser(t, router, token, "gw_latecomer", 0)
	handler.SyncGatewayPeers(context.Background())
	if got := peer.pushes("gw_waiting"); len(got) != 2 || got[1].Status != "disabled" {
		t.Errorf("a held user's change didn't reach a disabled peer: %+v", got)
	}
	if got := peer.pushes("gw_latecomer"); len(got) != 0 {
		t.Errorf("a disabled peer got a new user: %+v", got)
	}
}

// It would take users whose limit it can't share, and its hosts would keep the old fault.
func TestPanelTooOldToReportTrafficGetsNothing(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	url := createGatewayUser(t, router, token, "gw_old", 0)
	peer := newFakePeer(t)
	peer.protocol = 0
	id := registerFakePeer(t, router, token, peer, true)

	handler.SyncGatewayPeers(context.Background())
	if got := peer.pushes("gw_old"); len(got) != 0 {
		t.Errorf("pushed to an old panel: %+v", got)
	}
	if links := decodeV2raySubscription(t, router, url); len(links) != 1 {
		t.Errorf("%d links, want only local", len(links))
	}
	if view := peerView(t, router, token, id); !strings.Contains(view["last_error"].(string), "older version") || view["last_seen_at"] == nil {
		t.Errorf("view = %q / %v", view["last_error"], view["last_seen_at"])
	}
}

// Whether a peer can't be reached says nothing about the user, so nobody is marked
// refused, and the next pass carries on.
func TestUnreachablePeerRefusesNobody(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	createGatewayUser(t, router, token, "gw_patient", 0)
	peer := newFakePeer(t)
	id := registerFakePeer(t, router, token, peer, true)

	peer.broken = true
	handler.SyncGatewayPeers(context.Background())
	view := peerView(t, router, token, id)
	if view["last_error"] == "" || view["rejected_users"] != float64(0) || view["synced_users"] != float64(0) {
		t.Errorf("while broken: %v", view)
	}

	peer.broken = false
	handler.SyncGatewayPeers(context.Background())
	view = peerView(t, router, token, id)
	if view["last_error"] != "" || view["synced_users"] != float64(1) {
		t.Errorf("after recovering: %v", view)
	}
}

func TestRemovingAPeerTakesThisPanelsUsersOffIt(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	peer := newFakePeer(t)
	id := registerFakePeer(t, router, token, peer, false)
	settings, err := handler.ensureGatewaySettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	resp := doRequest(t, router, "DELETE", "/api/settings/gateway/peers/"+itoa32(id), token, nil)
	if resp.Code != http.StatusOK || resp.Body["copies_left"] != false {
		t.Fatalf("delete: %d %v", resp.Code, resp.Body)
	}
	if len(peer.purged) != 1 || peer.purged[0] != settings.InstanceID {
		t.Errorf("purged = %v, want this panel's id", peer.purged)
	}

	// One it can't reach goes too, and the admin hears what stayed behind.
	broken := newFakePeer(t)
	id = registerFakePeer(t, router, token, broken, false)
	broken.broken = true
	resp = doRequest(t, router, "DELETE", "/api/settings/gateway/peers/"+itoa32(id), token, nil)
	if resp.Code != http.StatusOK || resp.Body["copies_left"] != true {
		t.Fatalf("delete of an unreachable peer: %d %v", resp.Code, resp.Body)
	}
}

// --- the other panel's side of the same calls ---

func gatewaySecret(t *testing.T, router http.Handler, token string) string {
	t.Helper()
	resp := doRequest(t, router, "GET", "/api/settings/gateway", token, nil)
	secret, _ := resp.Body["secret"].(string)
	if secret == "" {
		t.Fatalf("no secret: %v", resp.Body)
	}
	return secret
}

func TestReplicaTakesTheCredentialAsStoredKeepingPlainVLESSPlain(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	secret := gatewaySecret(t, router, token)
	push := map[string]interface{}{
		"origin_id": "origin-a", "origin_panel_name": "A", "username": "gw_plain", "status": "active",
		"proxies": map[string]interface{}{"vless": map[string]string{"id": "71111111-1111-1111-1111-111111111111", "flow": ""}},
	}
	if resp := doRequest(t, router, "POST", "/api/internal/gateway/users/sync", secret, push); resp.Code != http.StatusOK {
		t.Fatalf("sync: %d %v", resp.Code, resp.Body)
	}
	got := doRequest(t, router, "GET", "/api/user/gw_plain", token, nil)
	vless, _ := got.Body["proxies"].(map[string]interface{})["vless"].(map[string]interface{})
	if flow, _ := vless["flow"].(string); flow != "" {
		t.Errorf("flow = %q, a user with none got one and would be refused by the core", flow)
	}
}

func TestReplicaBelongsToItsOrigin(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	secret := gatewaySecret(t, router, token)
	push := func(origin, status string, deleted bool) apiResponse {
		return doRequest(t, router, "POST", "/api/internal/gateway/users/sync", secret, map[string]interface{}{
			"origin_id": origin, "origin_panel_name": "same name", "username": "gw_owned", "status": status, "deleted": deleted,
			"proxies": map[string]interface{}{"vless": map[string]string{"id": "72222222-2222-2222-2222-222222222222"}},
		})
	}
	if resp := push("origin-a", "active", false); resp.Code != http.StatusOK {
		t.Fatalf("first push: %d %v", resp.Code, resp.Body)
	}
	if resp := push("origin-b", "disabled", false); resp.Code != http.StatusConflict {
		t.Errorf("another origin overwrote it: %d %v", resp.Code, resp.Body)
	}
	if resp := push("origin-b", "", true); resp.Code != http.StatusConflict {
		t.Errorf("another origin deleted it: %d %v", resp.Code, resp.Body)
	}
	if got := doRequest(t, router, "GET", "/api/user/gw_owned", token, nil); got.Body["status"] != "active" {
		t.Errorf("status = %v", got.Body["status"])
	}

	// What an admin here can't do to it either: the origin would put it back, or a
	// customer the origin still serves would be cut off.
	for _, call := range []struct{ method, path string }{
		{"DELETE", "/api/user/gw_owned"}, {"POST", "/api/user/gw_owned/reset"}, {"POST", "/api/user/gw_owned/revoke_sub"},
	} {
		if resp := doRequest(t, router, call.method, call.path, token, nil); resp.Code != http.StatusConflict {
			t.Errorf("%s %s: %d %v, want 409", call.method, call.path, resp.Code, resp.Body)
		}
	}
}

// What older versions left behind, including from a panel nobody named: the first push
// with an ID claims it, or that user could never be copied again.
func TestCopyFromBeforeOriginsHadAnIDIsAdopted(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	secret := gatewaySecret(t, router, token)
	ctx := context.Background()
	if _, err := handler.store.Pool.Exec(ctx, `INSERT INTO users (username, status, synced_from_panel_name) VALUES ('gw_old_blank', 'active', ''), ('gw_old_named', 'active', 'Named')`); err != nil {
		t.Fatal(err)
	}
	push := func(username, origin, name string) int {
		return doRequest(t, router, "POST", "/api/internal/gateway/users/sync", secret, map[string]interface{}{
			"origin_id": origin, "origin_panel_name": name, "username": username, "status": "active", "proxies": map[string]interface{}{},
		}).Code
	}
	if got := push("gw_old_named", "origin-b", "Other"); got != http.StatusConflict {
		t.Errorf("another panel took a named copy: %d", got)
	}
	if got := push("gw_old_named", "origin-a", "Named"); got != http.StatusOK {
		t.Errorf("its own panel couldn't claim it: %d", got)
	}
	if got := push("gw_old_blank", "origin-a", ""); got != http.StatusOK {
		t.Errorf("a blank copy couldn't be claimed: %d", got)
	}
	if got := push("gw_old_blank", "origin-b", ""); got != http.StatusConflict {
		t.Errorf("a second panel took it once claimed: %d", got)
	}
}

// An admin's bulk actions on the panel holding the copies: resetting them would bring a
// limited user back, and deleting one would leave its origin offering servers that
// refuse them, with nothing telling it so.
func TestBulkActionsLeaveCopiesAlone(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	secret := gatewaySecret(t, router, token)
	ctx := context.Background()
	for _, status := range []string{"limited", "expired"} {
		resp := doRequest(t, router, "POST", "/api/internal/gateway/users/sync", secret, map[string]interface{}{
			"origin_id": "origin-a", "origin_panel_name": "A", "username": "gw_copy_" + status, "status": status, "expire": 1000, "proxies": map[string]interface{}{},
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("sync: %d %v", resp.Code, resp.Body)
		}
	}
	createGatewayUser(t, router, token, "gw_mine_expired", 0)
	if _, err := handler.store.Pool.Exec(ctx, `UPDATE users SET status='expired', expire=1000 WHERE username='gw_mine_expired'`); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.store.Pool.Exec(ctx, `UPDATE users SET used_traffic=500 WHERE username LIKE 'gw_copy_%'`); err != nil {
		t.Fatal(err)
	}

	if resp := doRequest(t, router, "POST", "/api/users/reset", token, nil); resp.Code != http.StatusOK {
		t.Fatalf("reset all: %d %v", resp.Code, resp.Body)
	}
	copyState := func(username string) (status string, used int64) {
		t.Helper()
		if err := handler.store.Pool.QueryRow(ctx, "SELECT status, used_traffic FROM users WHERE username=$1", username).Scan(&status, &used); err != nil {
			t.Fatalf("%s: %v", username, err)
		}
		return status, used
	}
	if status, used := copyState("gw_copy_limited"); status != "limited" || used != 500 {
		t.Errorf("reset all changed a copy: %s, %d", status, used)
	}

	resp := doRequest(t, router, "DELETE", "/api/users/expired", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete expired: %d %s", resp.Code, resp.Raw)
	}
	if !strings.Contains(string(resp.Raw), "gw_mine_expired") {
		t.Errorf("this panel's own expired user wasn't deleted: %s", resp.Raw)
	}
	if status, _ := copyState("gw_copy_expired"); status != "expired" {
		t.Errorf("status = %s", status)
	}
}

func TestSyncCallIsHeldToWhatAnAdminsCreateWouldBe(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	secret := gatewaySecret(t, router, token)
	id := "73333333-3333-3333-3333-333333333333"
	if resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "gw_real", "proxies": map[string]interface{}{"vless": map[string]string{"id": id}},
	}); resp.Code != http.StatusOK {
		t.Fatalf("create: %d %v", resp.Code, resp.Body)
	}
	for name, body := range map[string]map[string]interface{}{
		"a bad status":        {"username": "gw_new", "status": "root", "proxies": map[string]interface{}{}},
		"a bad username":      {"username": "a b;c", "status": "active", "proxies": map[string]interface{}{}},
		"another's UUID":      {"username": "gw_thief", "status": "active", "proxies": map[string]interface{}{"vless": map[string]string{"id": id}}},
		"an unknown protocol": {"username": "gw_new", "status": "active", "proxies": map[string]interface{}{"nope": map[string]string{}}},
	} {
		body["origin_id"] = "origin-a"
		resp := doRequest(t, router, "POST", "/api/internal/gateway/users/sync", secret, body)
		if resp.Code != http.StatusConflict && resp.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %v, want it refused", name, resp.Code, resp.Body)
		}
	}
}

func TestUsageAndPurgeOnlyTouchOneOrigin(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "GW VLESS", "protocol": "vless"}})
	secret := gatewaySecret(t, router, token)
	createGatewayUser(t, router, token, "gw_local", 0)
	for i, origin := range []string{"origin-a", "origin-b"} {
		resp := doRequest(t, router, "POST", "/api/internal/gateway/users/sync", secret, map[string]interface{}{
			"origin_id": origin, "origin_panel_name": origin, "username": "gw_of_" + strings.TrimPrefix(origin, "origin-"), "status": "active",
			"proxies": map[string]interface{}{"vless": map[string]string{"id": []string{"74444444-4444-4444-4444-444444444444", "75555555-5555-5555-5555-555555555555"}[i]}},
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("sync: %d %v", resp.Code, resp.Body)
		}
	}
	if _, err := handler.store.Pool.Exec(context.Background(), "UPDATE users SET used_traffic=77 WHERE username IN ('gw_of_a','gw_of_b','gw_local')"); err != nil {
		t.Fatal(err)
	}

	resp := doRequest(t, router, "GET", "/api/internal/gateway/usage?origin=origin-a", secret, nil)
	if resp.Code != http.StatusOK || !strings.Contains(string(resp.Raw), "gw_of_a") || strings.Contains(string(resp.Raw), "gw_of_b") || strings.Contains(string(resp.Raw), "gw_local") {
		t.Errorf("usage: %d %s", resp.Code, resp.Raw)
	}
	if resp := doRequest(t, router, "GET", "/api/internal/gateway/usage?origin=origin-a", token, nil); resp.Code != http.StatusUnauthorized {
		t.Errorf("an admin token read it: %d", resp.Code)
	}

	resp = doRequest(t, router, "POST", "/api/internal/gateway/users/purge", secret, map[string]string{"origin_id": "origin-a"})
	if resp.Code != http.StatusOK || resp.Body["deleted"] != float64(1) {
		t.Errorf("purge: %d %v", resp.Code, resp.Body)
	}
	if got := doRequest(t, router, "GET", "/api/user/gw_of_a", token, nil); got.Code != http.StatusNotFound {
		t.Errorf("origin-a's copy: %d", got.Code)
	}
	for _, name := range []string{"gw_of_b", "gw_local"} {
		if got := doRequest(t, router, "GET", "/api/user/"+name, token, nil); got.Code != http.StatusOK {
			t.Errorf("%s went with it: %d", name, got.Code)
		}
	}
}

func TestPeerStatusFillsInItsOwnAddress(t *testing.T) {
	router, token := newTestRouter(t)
	secret := gatewaySecret(t, router, token)
	doRequest(t, router, "POST", "/api/inbounds", token, map[string]interface{}{"tag": "gw-tls", "protocol": "vless", "port": 2001, "security": "tls"})
	resp := doRequest(t, router, "GET", "/api/internal/gateway/status", secret, nil)
	if resp.Code != http.StatusOK || strings.Contains(string(resp.Raw), "{SERVER_IP}") || !strings.Contains(string(resp.Raw), "203.0.113.1") {
		t.Errorf("status: %d %s", resp.Code, resp.Raw)
	}
}

// Whoever switches a peer on hands it every user's credentials.
func TestOnlyTheOwnerCanChangePeers(t *testing.T) {
	router, token := newTestRouter(t)
	created := doRequest(t, router, "POST", "/api/admin", token, map[string]interface{}{"username": "gw_sudo", "password": "gw-sudo-password", "is_sudo": true})
	if created.Code != http.StatusOK {
		t.Fatalf("create admin: %d %v", created.Code, created.Body)
	}
	sudo := loginAs(t, router, "gw_sudo", "gw-sudo-password")
	resp := doRequest(t, router, "POST", "/api/settings/gateway/peers", sudo, map[string]interface{}{
		"name": "x", "base_url": "https://peer.example.test", "secret": "s",
	})
	if resp.Code != http.StatusForbidden {
		t.Errorf("a sudo admin added a peer: %d %v", resp.Code, resp.Body)
	}
}

func itoa32(id int32) string { return strconv.Itoa(int(id)) }
