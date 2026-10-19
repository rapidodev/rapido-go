package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// DB-backed tests of the user tools that rely on new SQL: searching by a
// config UID, filtering by reseller, the reseller overview, bulk actions and
// the CSV export. They run against a real Postgres (TEST_DATABASE_URL) like
// the rest of this package and skip without it.

func createVlessUser(t *testing.T, router http.Handler, token, username, uuid string) {
	t.Helper()
	resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": username,
		"proxies":  map[string]interface{}{"vless": map[string]interface{}{"id": uuid}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create user %s: %d %v", username, resp.Code, resp.Body)
	}
}

func usernamesOf(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	users, _ := body["users"].([]interface{})
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.(map[string]interface{})["username"].(string))
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestSearchFindsAUserByAConfigUUIDWithOrWithoutDashes(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{{"tag": "UID VLESS", "protocol": "vless"}})
	createVlessUser(t, router, token, "uid_alice", "83ead7ac-ec19-64f4-9bbb-b4f51d8f050b")
	createVlessUser(t, router, token, "uid_bob", "11111111-2222-4333-8444-555555555555")

	cases := map[string]string{
		"full UUID":              "83ead7ac-ec19-64f4-9bbb-b4f51d8f050b",
		"first block":            "83ead7ac",
		"UUID without dashes":    "83ead7acec1964f49bbbb4f51d8f050b",
		"fragment across dashes": "ec19-64f4",
		"upper case":             "83EAD7AC-EC19",
	}
	for name, q := range cases {
		resp := doRequest(t, router, "GET", "/api/users?search="+q, token, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: %d %v", name, resp.Code, resp.Body)
		}
		if got := usernamesOf(t, resp.Body); !sameSet(got, []string{"uid_alice"}) {
			t.Errorf("%s (%q): got %v, want only uid_alice", name, q, got)
		}
		if total, _ := resp.Body["total"].(float64); int(total) != 1 {
			t.Errorf("%s: total = %v, want 1 (CountUsers must apply the same clause)", name, resp.Body["total"])
		}
	}

	// A query shorter than 4 characters must not match through the UUID clause
	// (it would hit half the table), but still matches usernames as before.
	resp := doRequest(t, router, "GET", "/api/users?search=83e", token, nil)
	if got := usernamesOf(t, resp.Body); len(got) != 0 {
		t.Errorf("a 3-character search matched through a UUID: %v", got)
	}
	resp = doRequest(t, router, "GET", "/api/users?search=uid_", token, nil)
	if got := usernamesOf(t, resp.Body); !sameSet(got, []string{"uid_alice", "uid_bob"}) {
		t.Errorf("username search broke: %v", got)
	}
}

func TestSudoCanFilterTheUserListToOneResellersConfigs(t *testing.T) {
	router, sudoToken := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", sudoToken, []map[string]interface{}{{"tag": "Filter VLESS", "protocol": "vless"}})
	doRequest(t, router, "POST", "/api/admin", sudoToken, map[string]interface{}{"username": "shop-a", "password": "pw12345", "is_sudo": false})
	doRequest(t, router, "POST", "/api/admin", sudoToken, map[string]interface{}{"username": "shop-b", "password": "pw12345", "is_sudo": false})
	tokA := loginAs(t, router, "shop-a", "pw12345")
	tokB := loginAs(t, router, "shop-b", "pw12345")
	for i := 0; i < 3; i++ {
		createVlessUser(t, router, tokA, fmt.Sprintf("a_user_%d", i), fmt.Sprintf("aaaaaaaa-0000-4000-8000-00000000000%d", i))
	}
	createVlessUser(t, router, tokB, "b_user_0", "bbbbbbbb-0000-4000-8000-000000000000")
	createVlessUser(t, router, sudoToken, "own_user", "cccccccc-0000-4000-8000-000000000000")

	resp := doRequest(t, router, "GET", "/api/users?admin=shop-a", sudoToken, nil)
	if got := usernamesOf(t, resp.Body); !sameSet(got, []string{"a_user_0", "a_user_1", "a_user_2"}) {
		t.Errorf("?admin=shop-a returned %v", got)
	}
	if total, _ := resp.Body["total"].(float64); int(total) != 3 {
		t.Errorf("total = %v, want 3", resp.Body["total"])
	}
	// An unknown reseller matches nothing - never "everyone".
	resp = doRequest(t, router, "GET", "/api/users?admin=nobody", sudoToken, nil)
	if got := usernamesOf(t, resp.Body); len(got) != 0 {
		t.Errorf("unknown admin returned %v", got)
	}
	// Combined with a UID search.
	resp = doRequest(t, router, "GET", "/api/users?admin=shop-a&search=aaaaaaaa-0000-4000-8000-000000000001", sudoToken, nil)
	if got := usernamesOf(t, resp.Body); !sameSet(got, []string{"a_user_1"}) {
		t.Errorf("admin+uid returned %v", got)
	}
	// A reseller cannot use the filter to look at somebody else's configs.
	resp = doRequest(t, router, "GET", "/api/users?admin=shop-b", tokA, nil)
	for _, u := range usernamesOf(t, resp.Body) {
		if strings.HasPrefix(u, "b_") {
			t.Errorf("reseller shop-a saw shop-b's config %s through ?admin=", u)
		}
	}
}

func TestAdminsOverviewCountsPerResellerAndScopesAReseller(t *testing.T) {
	router, sudoToken := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", sudoToken, []map[string]interface{}{{"tag": "Overview VLESS", "protocol": "vless"}})
	doRequest(t, router, "POST", "/api/admin", sudoToken, map[string]interface{}{"username": "ov-shop", "password": "pw12345", "is_sudo": false})
	tok := loginAs(t, router, "ov-shop", "pw12345")
	for i := 0; i < 4; i++ {
		createVlessUser(t, router, tok, fmt.Sprintf("ov_user_%d", i), fmt.Sprintf("dddddddd-0000-4000-8000-00000000000%d", i))
	}
	doRequest(t, router, "PUT", "/api/user/ov_user_3", tok, map[string]interface{}{"status": "disabled"})

	resp := doRequest(t, router, "GET", "/api/admins/overview", sudoToken, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("overview as sudo: %d %v", resp.Code, resp.Body)
	}
	rows, _ := resp.Body["admins"].([]interface{})
	if len(rows) == 0 {
		t.Fatalf("overview has no rows: %v", resp.Body)
	}
	var mine map[string]interface{}
	for _, r := range rows {
		if m := r.(map[string]interface{}); m["username"] == "ov-shop" {
			mine = m
		}
	}
	if mine == nil {
		t.Fatalf("no row for ov-shop in %v", rows)
	}
	counts, _ := mine["configs"].(map[string]interface{})
	if counts == nil || int(counts["active"].(float64)) != 3 || int(counts["disabled"].(float64)) != 1 {
		t.Errorf("status counts = %v, want 3 active and 1 disabled", mine["configs"])
	}

	// A reseller gets only their own row.
	resp = doRequest(t, router, "GET", "/api/admins/overview", tok, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("overview as reseller: %d %v", resp.Code, resp.Body)
	}
	if rows, _ := resp.Body["admins"].([]interface{}); len(rows) != 1 || rows[0].(map[string]interface{})["username"] != "ov-shop" {
		t.Errorf("a reseller must see exactly their own row, got %v", resp.Body["admins"])
	}
}

func TestBulkActionsApplyToTheChosenUsersOnlyAndRespectOwnership(t *testing.T) {
	router, sudoToken := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", sudoToken, []map[string]interface{}{{"tag": "Bulk VLESS", "protocol": "vless"}})
	doRequest(t, router, "POST", "/api/admin", sudoToken, map[string]interface{}{"username": "bulk-a", "password": "pw12345", "is_sudo": false})
	tokA := loginAs(t, router, "bulk-a", "pw12345")
	for i := 0; i < 3; i++ {
		createVlessUser(t, router, tokA, fmt.Sprintf("bulk_%d", i), fmt.Sprintf("eeeeeeee-0000-4000-8000-00000000000%d", i))
	}
	createVlessUser(t, router, sudoToken, "bulk_not_mine", "ffffffff-0000-4000-8000-000000000000")

	resp := doRequest(t, router, "POST", "/api/users/bulk", tokA, map[string]interface{}{
		"usernames": []string{"bulk_0", "bulk_1", "bulk_not_mine"}, "action": "disable",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("bulk disable: %d %v", resp.Code, resp.Body)
	}
	status := func(name string) string {
		r := doRequest(t, router, "GET", "/api/user/"+name, sudoToken, nil)
		s, _ := r.Body["status"].(string)
		return s
	}
	if status("bulk_0") != "disabled" || status("bulk_1") != "disabled" {
		t.Errorf("chosen users were not disabled: %s %s", status("bulk_0"), status("bulk_1"))
	}
	if status("bulk_2") != "active" {
		t.Errorf("a user outside the selection changed: %s", status("bulk_2"))
	}
	if status("bulk_not_mine") != "active" {
		t.Errorf("a reseller disabled someone else's user through bulk")
	}
}

func TestExportStreamsACSVOfTheFilteredUsers(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{{"tag": "Export VLESS", "protocol": "vless"}})
	createVlessUser(t, router, token, "csv_user_1", "99999999-0000-4000-8000-000000000001")
	createVlessUser(t, router, token, "csv_user_2", "99999999-0000-4000-8000-000000000002")

	req := httptest.NewRequest(http.MethodGet, "/api/users/export?search=csv_user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "csv") {
		t.Errorf("content type = %q, want csv", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "username") || !strings.Contains(body, "csv_user_1") || !strings.Contains(body, "csv_user_2") {
		t.Errorf("csv is missing the header or rows:\n%s", body)
	}
}

// A tunnel with a few minutes of samples and events, built straight through
// the queries so the endpoints' SQL (date_bin bucketing, the event listings,
// the cross-tunnel timeline) runs against a real database.
func TestTunnelSamplesEventsAndTimelineEndpoints(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	ctx := context.Background()
	resp := doRequest(t, router, "POST", "/api/node", token, map[string]interface{}{"name": "ts-node", "address": "10.0.0.9", "port": 62050, "api_port": 62051})
	if resp.Code != http.StatusOK {
		t.Fatalf("create node: %d %v", resp.Code, resp.Body)
	}
	nodeID := int32(resp.Body["id"].(float64))
	tun, err := handler.store.Queries.CreateTunnel(ctx, generated.CreateTunnelParams{
		Name: "ts-tunnel", Method: "frp_direct", NodeID: nodeID, NodeSshPort: 22, NodeSshUser: "root", NodeSshPassword: "x",
		RelayHost: "203.0.113.5", RelaySshPort: 22, RelaySshUser: "root", RelaySshPassword: "x", Ports: []int32{20001},
		InterfaceName: "tts001", TunnelSubnet: netip.MustParsePrefix("0.0.0.0/32"), FrpControlPort: 7000, FrpToken: "",
		Status: "active", Managed: false, Details: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 30; i++ {
		at := now.Add(-time.Duration(30-i) * time.Minute)
		if err := handler.store.Queries.InsertTunnelSample(ctx, generated.InsertTunnelSampleParams{
			TunnelID: tun.ID, At: pgtype.Timestamptz{Time: at, Valid: true},
			RelayRxBps: pgtype.Float8{Float64: 2.5e8 + float64(i)*1e6, Valid: true}, RelayTxBps: pgtype.Float8{Float64: 2.6e8, Valid: true},
			Connections: pgtype.Int4{Int32: int32(1000 + i), Valid: true}, Cpu: pgtype.Float4{Float32: 30, Valid: true},
			V6RttMs: pgtype.Float4{Float32: 86, Valid: true}, V6Loss: pgtype.Float4{Float32: 0, Valid: true},
			HaActive: pgtype.Text{String: "relay_v6", Valid: true},
		}); err != nil {
			t.Fatalf("insert sample: %v", err)
		}
	}
	if _, err := handler.store.Queries.InsertTunnelEvent(ctx, generated.InsertTunnelEventParams{
		TunnelID: tun.ID, At: pgtype.Timestamptz{Time: now.Add(-10 * time.Minute), Valid: true},
		Kind: "failover", Severity: "warning", Detail: "relay_v6 -> relay_v4", DurationSeconds: pgtype.Int4{Int32: 7, Valid: true},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	path := fmt.Sprintf("/api/tunnels/%d", tun.ID)
	resp = doRequest(t, router, "GET", path+"/samples?hours=1", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("samples: %d %v", resp.Code, resp.Body)
	}
	body := fmt.Sprint(resp.Body)
	if !strings.Contains(body, "relay_rx_bps") && !strings.Contains(body, "rx") {
		t.Errorf("samples response carries no bandwidth series: %.300s", body)
	}
	if n := countJSONPoints(resp.Body); n == 0 || n > 300 {
		t.Errorf("samples returned %d points, want 1..300", n)
	}
	resp = doRequest(t, router, "GET", path+"/events", token, nil)
	if resp.Code != http.StatusOK || !strings.Contains(fmt.Sprint(resp.Body), "failover") {
		t.Errorf("events: %d %v", resp.Code, resp.Body)
	}
	resp = doRequest(t, router, "GET", "/api/tunnels/timeline?hours=1", token, nil)
	if resp.Code != http.StatusOK || !strings.Contains(fmt.Sprint(resp.Body), "failover") {
		t.Errorf("timeline: %d %v", resp.Code, resp.Body)
	}
}

// countJSONPoints finds the longest array in a decoded response - the series.
func countJSONPoints(v interface{}) int {
	best := 0
	var walk func(x interface{})
	walk = func(x interface{}) {
		switch t := x.(type) {
		case []interface{}:
			if len(t) > best {
				best = len(t)
			}
			for _, e := range t {
				walk(e)
			}
		case map[string]interface{}:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return best
}
