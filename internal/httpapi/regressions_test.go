package httpapi

import (
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// ---------------------------------------------------------------------------
// Client detection

func TestEveryClientOnTheMetaCoreGetsClashMeta(t *testing.T) {
	for _, userAgent := range []string{
		"FlClash/v0.9.2 clash-verge Platform/android",
		"FlClash/v0.8.95 clash-verge Platform/windows",
		"clash-verge/v2.2.3",
		"ClashMetaForAndroid/2.11.14.Meta",
		"ClashMetaForAndroid/2.11.14.Meta-Alpha",
		"ClashMeta/1.18",
		"clash-meta",
		"clash.meta",
		"mihomo/1.19.33",
		"mihomo.party/v1.8.0 (clash.meta)",
		"clash.party/v1.8.0 (clash.meta)",
		"clash-nyanpasu/v1.6.1",
		"ClashX Meta/1.4.0",
		"ClashMi/1.0",
		"koala-clash/1.0",
	} {
		if got := detectSubscriptionFormat(userAgent, SubscriptionFormatFlags{}); got != "clash-meta" {
			t.Errorf("%s: got %s, want clash-meta", userAgent, got)
		}
	}
}

func TestOtherClientsKeepTheFormatTheyHad(t *testing.T) {
	for userAgent, want := range map[string]string{
		"ClashX/1.118.0":          "clash",
		"ClashforWindows/0.20.39": "clash",
		"Clash/v1.18.0":           "clash",
		"Stash/2.7.0 Clash/1.11":  "clash",
		"SFA/1.11.0":              "sing-box",
		"HiddifyNext/2.5.7":       "sing-box",
		"Outline/1.3":             "outline",
		"v2rayNG/1.10.0":          "v2ray",
		"Happ/3.0.0":              "v2ray",
	} {
		if got := detectSubscriptionFormat(userAgent, SubscriptionFormatFlags{}); got != want {
			t.Errorf("%s: got %s, want %s", userAgent, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Core config

func TestACoreConfigThatWouldBlockEverythingIsRefused(t *testing.T) {
	router, token := newTestRouter(t)
	put := func(body map[string]interface{}) apiResponse {
		return doRequest(t, router, "PUT", "/api/settings/core-config", token, body)
	}

	if resp := put(map[string]interface{}{
		"routing_rules": []map[string]interface{}{{"outbound_tag": "block"}},
	}); resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("no condition to the built-in block: %d %v", resp.Code, resp.Body)
	}
	if resp := put(map[string]interface{}{
		"outbounds":     []map[string]interface{}{{"tag": "BLOCK", "type": "block"}},
		"routing_rules": []map[string]interface{}{{"outbound_tag": "BLOCK"}},
	}); resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("no condition to a custom block outbound: %d %v", resp.Code, resp.Body)
	}

	// Both of these are ordinary: a default exit, and a block with a condition.
	if resp := put(map[string]interface{}{
		"outbounds": []map[string]interface{}{{"tag": "exit", "type": "socks", "server": "1.1.1.1", "server_port": 1080}},
		"routing_rules": []map[string]interface{}{
			{"ip_is_private": true, "outbound_tag": "block"},
			{"outbound_tag": "exit"},
		},
	}); resp.Code != http.StatusOK {
		t.Errorf("a block with a condition and an exit without one: %d %v", resp.Code, resp.Body)
	}
}

func TestImportingTheStockXrayConfigLeavesTrafficFlowing(t *testing.T) {
	router, token := newTestRouter(t)
	resp := doRequest(t, router, "POST", "/api/inbounds/import-xray", token, map[string]interface{}{
		"confirm": true,
		"config": `{
		  "inbounds": [{"tag": "VLESS TCP", "port": 2053, "protocol": "vless", "settings": {"clients": []}, "streamSettings": {"network": "tcp"}}],
		  "outbounds": [{"protocol": "freedom", "tag": "DIRECT"}, {"protocol": "blackhole", "tag": "BLOCK"}],
		  "routing": {"rules": [{"ip": ["geoip:private"], "outboundTag": "BLOCK", "type": "field"}]}
		}`,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("import: %d %v", resp.Code, resp.Body)
	}
	core := doRequest(t, router, "GET", "/api/settings/core-config", token, nil)
	rules, _ := core.Body["routing_rules"].([]interface{})
	if len(rules) != 1 {
		t.Fatalf("rules = %v", core.Body["routing_rules"])
	}
	rule := rules[0].(map[string]interface{})
	if rule["ip_is_private"] != true || rule["outbound_tag"] != "BLOCK" {
		t.Errorf("rule = %v, want private addresses to BLOCK and nothing else", rule)
	}
}

// ---------------------------------------------------------------------------
// REALITY

const testRealityKey = "SGVsbG9Xb3JsZEhlbGxvV29ybGRIZWxsb1dvcmxkMTI"

func TestARealityInboundANodeCouldNotStartIsRefused(t *testing.T) {
	valid := func() map[string]interface{} {
		return map[string]interface{}{
			"tag": "r", "protocol": "vless", "network": "tcp", "security": "reality",
			"reality_private_key": testRealityKey, "reality_short_ids": []string{"ab12"},
			"reality_server_name": "www.example.com", "reality_server_port": 443,
		}
	}
	router, token := newTestRouter(t)
	if resp := doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{valid()}); resp.Code != http.StatusOK {
		t.Fatalf("a valid one: %d %v", resp.Code, resp.Body)
	}
	for name, mutate := range map[string]func(map[string]interface{}){
		"a key that isn't one":   func(e map[string]interface{}) { e["reality_private_key"] = "priv-key-value" },
		"no key":                 func(e map[string]interface{}) { delete(e, "reality_private_key") },
		"no site to borrow from": func(e map[string]interface{}) { delete(e, "reality_server_name") },
		// sing-box panics on this one rather than refusing it.
		"a short id over 16 digits": func(e map[string]interface{}) { e["reality_short_ids"] = []string{"0123456789abcdef00"} },
		"a short id in half pairs":  func(e map[string]interface{}) { e["reality_short_ids"] = []string{"abc"} },
		"a port out of range":       func(e map[string]interface{}) { e["reality_server_port"] = 70000 },
	} {
		entry := valid()
		mutate(entry)
		if resp := doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{entry}); resp.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %v", name, resp.Code, resp.Body)
		}
	}
}

func TestShadowTLSOnlyGetsAHandshakeServerItCanDial(t *testing.T) {
	for sni, want := range map[string]string{
		"www.example.com":  "www.example.com",
		"":                 "",
		"{SERVER_IP}":      "",
		"a.com,b.com":      "",
		"*.example.com":    "",
		"1.2.3.4":          "",
		"localhost":        "",
		"example.com:8443": "",
		"a b.com":          "",
	} {
		if got := plainHostName(sni); got != want {
			t.Errorf("plainHostName(%q) = %q, want %q", sni, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// On-hold plans and durations

func TestDurationFitsIsAboutTheColumnAndNotAFixedSpan(t *testing.T) {
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	const year = 365 * 24 * 60 * 60
	for seconds, want := range map[int64]bool{
		0: true, 30 * 24 * 60 * 60: true, 11 * year: true,
		// Allowed by the old 20-year bound, and past the column today.
		15 * year: false, 20 * year: false, 1785000000: false, -1: false,
	} {
		if got := durationFits(seconds, now); got != want {
			t.Errorf("durationFits(%d) = %v, want %v", seconds, got, want)
		}
	}
	end := expireAfter(100*year, now)
	if end.Int32 != 2147483647 {
		t.Errorf("expireAfter past the column = %d, want it held to the column's limit", end.Int32)
	}
}

func TestPlanBulkEnablePutsAWaitingPlanBackOnHold(t *testing.T) {
	now := time.Now()
	held := generated.User{Status: statusDisabled, OnHoldExpireDuration: pgtype.Int8{Int64: 2592000, Valid: true}}
	if got := planBulk(held, bulkEnable, 0, now); got.NewStatus != statusOnHold {
		t.Errorf("a disabled user who never started: %+v", got)
	}
	started := generated.User{Status: statusDisabled, Expire: pgtype.Int4{Int32: int32(now.Unix() + 1000), Valid: true}}
	if got := planBulk(started, bulkEnable, 0, now); got.NewStatus != statusActive {
		t.Errorf("a disabled user with an expiry: %+v", got)
	}
	unlimited := generated.User{Status: statusDisabled}
	if got := planBulk(unlimited, bulkEnable, 0, now); got.NewStatus != statusActive {
		t.Errorf("a disabled user with no plan at all: %+v", got)
	}
}

func createHeldUser(t *testing.T, router http.Handler, token, name string) {
	t.Helper()
	resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": name, "status": "on_hold", "on_hold_expire_duration": 2592000,
		"proxies": map[string]interface{}{"vmess": map[string]interface{}{}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create %s: %d %v", name, resp.Code, resp.Body)
	}
}

func TestDisablingAndEnablingAnOnHoldUserKeepsTheirPlanWaiting(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "Main", "protocol": "vmess"}})

	const name = "held_edit"
	createHeldUser(t, router, token, name)
	for _, status := range []string{"disabled", "active"} {
		if resp := doRequest(t, router, "PUT", "/api/user/"+name, token, map[string]interface{}{"status": status}); resp.Code != http.StatusOK {
			t.Fatalf("set %s: %d %v", status, resp.Code, resp.Body)
		}
	}
	got := doRequest(t, router, "GET", "/api/user/"+name, token, nil)
	if got.Body["status"] != "on_hold" {
		t.Errorf("status = %v, want on_hold: active here is a 30-day plan with no end", got.Body["status"])
	}
	if got.Body["on_hold_expire_duration"] != float64(2592000) {
		t.Errorf("on_hold_expire_duration = %v, want the plan kept", got.Body["on_hold_expire_duration"])
	}
}

func TestActivatingAnOnHoldUserByHandStartsTheirPlan(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "Main", "protocol": "vmess"}})

	const name = "held_start"
	createHeldUser(t, router, token, name)
	before := time.Now().Unix()
	resp := doRequest(t, router, "PUT", "/api/user/"+name, token, map[string]interface{}{"status": "active"})
	if resp.Code != http.StatusOK {
		t.Fatalf("activate: %d %v", resp.Code, resp.Body)
	}
	if resp.Body["status"] != "active" {
		t.Errorf("status = %v", resp.Body["status"])
	}
	expire, _ := resp.Body["expire"].(float64)
	if int64(expire) < before+2592000-5 || int64(expire) > before+2592000+60 {
		t.Errorf("expire = %v, want 30 days from now", resp.Body["expire"])
	}
	if resp.Body["on_hold_expire_duration"] != nil {
		t.Errorf("on_hold_expire_duration = %v, want it spent", resp.Body["on_hold_expire_duration"])
	}
}

func TestAnOnHoldUserCantLoseTheirDuration(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "Main", "protocol": "vmess"}})

	const name = "held_zero"
	createHeldUser(t, router, token, name)
	resp := doRequest(t, router, "PUT", "/api/user/"+name, token, map[string]interface{}{"on_hold_expire_duration": 0})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("%d %v: starting with no duration would mean no expiry", resp.Code, resp.Body)
	}
}

func TestADurationPastTheColumnIsRefused(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "Main", "protocol": "vmess"}})

	const fifteenYears = 15 * 365 * 24 * 60 * 60
	resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "long_plan", "proxies": map[string]interface{}{"vmess": map[string]interface{}{}},
		"next_plan": map[string]interface{}{"data_limit": 1000, "expire": fifteenYears},
	})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("next_plan: %d %v", resp.Code, resp.Body)
	}
	resp = doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "long_hold", "status": "on_hold", "on_hold_expire_duration": fifteenYears,
		"proxies": map[string]interface{}{"vmess": map[string]interface{}{}},
	})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("on_hold: %d %v", resp.Code, resp.Body)
	}
}

// ---------------------------------------------------------------------------
// Telegram

func TestOnlyAnOwnerDecidesWhoCommandsTheTelegramConsole(t *testing.T) {
	router, ownerToken := newTestRouter(t)
	sudoToken := createSudoAdmin(t, router, ownerToken, "plain_sudo", "pw123456")

	for field, value := range map[string]interface{}{
		"telegram_admin_ids": []int64{555},
		"telegram_api_token": "123:attacker",
		"telegram_proxy_url": "http://attacker.example:8080",
	} {
		resp := doRequest(t, router, "PUT", "/api/settings/integrations", sudoToken, map[string]interface{}{field: value})
		if resp.Code != http.StatusForbidden {
			t.Errorf("%s as a sudo admin: %d %v", field, resp.Code, resp.Body)
		}
		// In the same request as something they may change: all of it is refused.
		resp = doRequest(t, router, "PUT", "/api/settings/integrations", sudoToken, map[string]interface{}{field: value, "telegram_logger_channel_id": -1001})
		if resp.Code != http.StatusForbidden {
			t.Errorf("%s next to an allowed field: %d %v", field, resp.Code, resp.Body)
		}
	}
	after := doRequest(t, router, "GET", "/api/settings/integrations", ownerToken, nil)
	if ids, _ := after.Body["telegram_admin_ids"].([]interface{}); len(ids) != 0 {
		t.Errorf("telegram_admin_ids = %v after the refusals", ids)
	}

	if resp := doRequest(t, router, "PUT", "/api/settings/integrations", sudoToken, map[string]interface{}{"telegram_logger_channel_id": -1001}); resp.Code != http.StatusOK {
		t.Errorf("a sudo admin on everything else: %d %v", resp.Code, resp.Body)
	}
	if resp := doRequest(t, router, "PUT", "/api/settings/integrations", ownerToken, map[string]interface{}{"telegram_admin_ids": []int64{555}}); resp.Code != http.StatusOK {
		t.Errorf("an owner: %d %v", resp.Code, resp.Body)
	}
}

// ---------------------------------------------------------------------------
// Tunnels

func tunnelRow(id, node int32, relay, status, method string, port int32) generated.Tunnel {
	return generated.Tunnel{ID: id, NodeID: node, RelayHost: relay, Status: status, Method: method, FrpControlPort: port}
}

func TestTeardownKeepsAnFRPConfigAnotherTunnelIsUsing(t *testing.T) {
	failed := tunnelRow(2, 1, "r2", "failed", "frp_direct", 27000)
	live := tunnelRow(1, 1, "r1", "active", "frp_direct", 27000)

	if node, relay := sharesFRPConfig(failed, []generated.Tunnel{live, failed}); !node || relay {
		t.Errorf("the row that lost the collision: node=%v relay=%v, want to keep the node's config", node, relay)
	}
	// It owns its files, and the failed row is no reason to leave them behind.
	if node, relay := sharesFRPConfig(live, []generated.Tunnel{live, failed}); node || relay {
		t.Errorf("the live tunnel when it is deleted: node=%v relay=%v", node, relay)
	}

	sameRelay := tunnelRow(3, 2, "r2", "active", "gre_frp", 27000)
	if node, relay := sharesFRPConfig(failed, []generated.Tunnel{sameRelay}); node || !relay {
		t.Errorf("a shared relay: node=%v relay=%v", node, relay)
	}
	otherPort := tunnelRow(4, 1, "r2", "active", "frp_direct", 27001)
	if node, relay := sharesFRPConfig(failed, []generated.Tunnel{otherPort}); node || relay {
		t.Errorf("another port: node=%v relay=%v", node, relay)
	}
	noFRP := tunnelRow(5, 1, "r2", "active", "gre_dnat", 27000)
	if node, relay := sharesFRPConfig(failed, []generated.Tunnel{noFRP}); node || relay {
		t.Errorf("a method with no frp config: node=%v relay=%v", node, relay)
	}
}

func TestAFRPPortIsFreeOnBothEndsOfANewTunnel(t *testing.T) {
	_, _, h := newTestRouterAndHandler(t)
	ctx := t.Context()
	node, err := h.store.Queries.CreateNode(ctx, generated.CreateNodeParams{Name: "n1", Address: "10.0.0.1", Port: 62050, ApiPort: 62051, UsageCoefficient: 1, CoreOverrides: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	first, err := h.store.Queries.CreateTunnel(ctx, generated.CreateTunnelParams{
		Name: "first", Method: "frp_direct", NodeID: node.ID, NodeSshPort: 22, NodeSshUser: "root", NodeSshPassword: "x",
		RelayHost: "r1", RelaySshPort: 22, RelaySshUser: "root", RelaySshPassword: "x",
		Ports: []int32{443}, InterfaceName: "tn1", TunnelSubnet: netip.MustParsePrefix("10.255.0.0/30"), FrpControlPort: 27000, FrpToken: "t",
		Managed: true, Details: []byte("{}"), Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	used, err := h.store.Queries.ListUsedFRPControlPorts(ctx, generated.ListUsedFRPControlPortsParams{RelayHost: "brand-new-relay", NodeID: first.NodeID})
	if err != nil {
		t.Fatal(err)
	}
	if got := allocateFRPControlPort(used); got == first.FrpControlPort {
		t.Errorf("a second tunnel from the same node to a new relay got port %d, which holds the first tunnel's config on that node", got)
	}
	used, err = h.store.Queries.ListUsedFRPControlPorts(ctx, generated.ListUsedFRPControlPortsParams{RelayHost: "r1", NodeID: first.NodeID + 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := allocateFRPControlPort(used); got == first.FrpControlPort {
		t.Errorf("another node's tunnel to the same relay got port %d", got)
	}
}
