package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

func TestGetXrayConfigCombinesCoreConfigAndInbounds(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{
		{"tag": "Xray VLESS", "protocol": "vless"},
	})

	resp := doRequest(t, router, "GET", "/api/settings/xray-config", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("get xray config: %d %v", resp.Code, resp.Body)
	}
	if resp.Body["log_level"] != "warn" {
		t.Errorf("log_level = %v, want warn", resp.Body["log_level"])
	}
	inbounds, ok := resp.Body["inbounds"].([]interface{})
	if !ok || len(inbounds) != 1 {
		t.Fatalf("inbounds = %v, want exactly the 1 synced inbound", resp.Body["inbounds"])
	}
	in := inbounds[0].(map[string]interface{})
	if in["tag"] != "Xray VLESS" || in["protocol"] != "vless" {
		t.Errorf("inbound = %v, want tag=Xray VLESS protocol=vless", in)
	}
}

// TestUpdateXrayConfigAcceptsANewInboundAndARuleTargetingItInOneRequest is
// the whole point of this endpoint existing rather than two separate PUTs:
// the OLD /api/settings/core-config on its own rejects a routing rule that
// targets an inbound tag not already in the database (see
// TestUpdateCoreConfigRejectsRuleTargetingUnknownInbound) - an admin editing
// one combined JSON document naturally wants to add a brand-new inbound and
// a rule that routes its traffic in the same edit. This must succeed.
func TestUpdateXrayConfigAcceptsANewInboundAndARuleTargetingItInOneRequest(t *testing.T) {
	router, token := newTestRouter(t)

	resp := doRequest(t, router, "PUT", "/api/settings/xray-config", token, map[string]interface{}{
		"log_level":     "warn",
		"sniff_enabled": true,
		"inbounds": []map[string]interface{}{
			{"tag": "Brand New VLESS", "protocol": "vless"},
		},
		"routing_rules": []map[string]interface{}{
			{"inbound": []string{"Brand New VLESS"}, "outbound_tag": "block"},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("put xray config: %d %v", resp.Code, resp.Body)
	}

	inbounds := resp.Body["inbounds"].([]interface{})
	if len(inbounds) != 1 || inbounds[0].(map[string]interface{})["tag"] != "Brand New VLESS" {
		t.Errorf("inbounds = %v, want exactly Brand New VLESS", inbounds)
	}
	rules := resp.Body["routing_rules"].([]interface{})
	if len(rules) != 1 {
		t.Fatalf("routing_rules = %v, want 1 entry", rules)
	}

	// The inbound must actually exist for real afterward, not just echoed
	// back in this one response - confirm via the plain inbounds list.
	listResp := doRequest(t, router, "GET", "/api/inbounds", token, nil)
	found := false
	for _, tag := range inboundTagsOf(t, listResp, "vless") {
		if tag == "Brand New VLESS" {
			found = true
		}
	}
	if !found {
		t.Errorf("Brand New VLESS not found in GET /api/inbounds: %v", listResp.Body)
	}
}

// TestUpdateXrayConfigRejectsBadShapeBeforeTouchingInbounds proves the
// cheap pre-check: a request with an inbound AND an invalid core-config
// value (bad log_level) must be rejected without creating the inbound at
// all - a plain typo in log_level shouldn't have the side effect of a new
// inbound (and its auto-created default host) silently appearing.
func TestUpdateXrayConfigRejectsBadShapeBeforeTouchingInbounds(t *testing.T) {
	router, token := newTestRouter(t)

	resp := doRequest(t, router, "PUT", "/api/settings/xray-config", token, map[string]interface{}{
		"log_level": "not-a-real-level",
		"inbounds": []map[string]interface{}{
			{"tag": "Should Not Exist", "protocol": "vless"},
		},
	})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid log_level: %d, want 422: %v", resp.Code, resp.Body)
	}

	listResp := doRequest(t, router, "GET", "/api/inbounds", token, nil)
	if vlessTags, ok := listResp.Body["vless"]; ok && len(vlessTags.([]interface{})) > 0 {
		t.Errorf("inbound was created despite the request being rejected: %v", listResp.Body)
	}
}

// TestUpdateXrayConfigDeletesInboundsOmittedFromThePayload is the real
// regression test for the full-replace behavior handleUpdateXrayConfig
// deliberately has and POST /api/inbounds/sync deliberately doesn't (see
// that handler's own doc comment) - once the dashboard only has this one
// JSON editor for inbounds, omitting a tag from a PUT has to be how an
// admin removes it, since the old per-row Delete button is gone.
func TestUpdateXrayConfigDeletesInboundsOmittedFromThePayload(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{
		{"tag": "Keep Me", "protocol": "vless"},
		{"tag": "Remove Me", "protocol": "trojan"},
	})
	doRequest(t, router, "PUT", "/api/hosts", token, map[string]interface{}{
		"Keep Me":   []map[string]interface{}{{"remark": "k", "address": "1.2.3.4", "port": 443, "security": "none"}},
		"Remove Me": []map[string]interface{}{{"remark": "r", "address": "1.2.3.4", "port": 444, "security": "none"}},
	})
	// A user holding both protocols - the trojan side should be pruned once
	// "Remove Me" (trojan's only inbound) disappears from this Apply, the
	// same real gap the migration itself hit (see PruneOrphanedProxies).
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "both-proto-user",
		"proxies":  map[string]interface{}{"vless": map[string]interface{}{}, "trojan": map[string]interface{}{}},
	})

	resp := doRequest(t, router, "PUT", "/api/settings/xray-config?confirm_drop_credentials=true", token, map[string]interface{}{
		"inbounds": []map[string]interface{}{{"tag": "Keep Me", "protocol": "vless"}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("put xray config: %d %v", resp.Code, resp.Body)
	}
	inbounds := resp.Body["inbounds"].([]interface{})
	if len(inbounds) != 1 || inbounds[0].(map[string]interface{})["tag"] != "Keep Me" {
		t.Fatalf("inbounds = %v, want only Keep Me left", inbounds)
	}

	// Confirmed gone for real, not just absent from this one response, and
	// its host cascaded away with it (DeleteInboundByTag's own cascade).
	listResp := doRequest(t, router, "GET", "/api/inbounds", token, nil)
	trojanTags, _ := listResp.Body["trojan"].([]interface{})
	if len(trojanTags) != 0 {
		t.Errorf("Remove Me's protocol still has tags: %v", trojanTags)
	}
	hostsResp := doRequest(t, router, "GET", "/api/hosts", token, nil)
	if _, stillThere := hostsResp.Body["Remove Me"]; stillThere {
		t.Errorf("Remove Me's host survived the inbound deletion: %v", hostsResp.Body)
	}
	if keepHosts, ok := hostsResp.Body["Keep Me"].([]interface{}); !ok || len(keepHosts) != 1 {
		t.Errorf("Keep Me's own host was disturbed: %v", hostsResp.Body["Keep Me"])
	}

	userResp := doRequest(t, router, "GET", "/api/user/both-proto-user", token, nil)
	proxies, _ := userResp.Body["proxies"].(map[string]interface{})
	if _, stillHasTrojan := proxies["trojan"]; stillHasTrojan {
		t.Errorf("trojan proxy should have been pruned once its only inbound was removed via Apply, got %v", proxies)
	}
	if _, stillHasVless := proxies["vless"]; !stillHasVless {
		t.Errorf("vless proxy should be untouched, got %v", proxies)
	}
}

func TestUpdateXrayConfigRejectsUnknownProtocol(t *testing.T) {
	router, token := newTestRouter(t)
	resp := doRequest(t, router, "PUT", "/api/settings/xray-config", token, map[string]interface{}{
		"inbounds": []map[string]interface{}{{"tag": "Bad Protocol", "protocol": "not-a-real-protocol"}},
	})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown protocol: %d, want 422: %v", resp.Code, resp.Body)
	}
}

// Every setting the document shows has to survive a round trip through Apply.
// Reflection, so an inbound setting added later fails here until it is carried
// over, instead of being wiped silently by the next Apply.
func TestInboundSyncEntryFromDetailCarriesEverySetting(t *testing.T) {
	var in inboundDetailDTO
	v := reflect.ValueOf(&in).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(v.Type().Field(i).Name)
		case reflect.Int32:
			f.SetInt(int64(i + 1))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{v.Type().Field(i).Name}))
		default:
			t.Fatalf("field %s: add a test value for kind %s", v.Type().Field(i).Name, f.Kind())
		}
	}

	out := reflect.ValueOf(inboundSyncEntryFromDetail(in))
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		got := out.FieldByName(name)
		if !got.IsValid() {
			t.Errorf("inboundSyncEntry has no field for the document's %s", name)
			continue
		}
		if !reflect.DeepEqual(got.Interface(), v.Field(i).Interface()) {
			t.Errorf("%s = %v, want %v", name, got.Interface(), v.Field(i).Interface())
		}
	}
}

// A panel holding one of these inbounds had every Apply refused (snell's PSK,
// shadowtls's inner key and hysteria's bandwidth arrived empty) or silently
// reset (hysteria2's obfuscation password, tuic's congestion control).
func TestUpdateXrayConfigKeepsEachProtocolsOwnSettings(t *testing.T) {
	router, token := newTestRouter(t)
	inner := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	put := func(inbounds []interface{}) map[string]interface{} {
		t.Helper()
		resp := doRequest(t, router, "PUT", "/api/settings/xray-config", token, map[string]interface{}{"inbounds": inbounds})
		if resp.Code != http.StatusOK {
			t.Fatalf("put xray config: %d %v", resp.Code, resp.Body)
		}
		return resp.Body
	}

	first := put([]interface{}{
		map[string]interface{}{"tag": "S", "protocol": "snell", "snell_psk": "a-long-enough-psk", "snell_v6_mode": "unshaped"},
		map[string]interface{}{"tag": "ST", "protocol": "shadowtls", "shadowtls_inner_method": "2022-blake3-aes-256-gcm", "shadowtls_inner_password": inner},
		map[string]interface{}{"tag": "H1", "protocol": "hysteria", "security": "tls", "up_mbps": 100, "down_mbps": 200, "hysteria_obfs_password": "obfs1"},
		map[string]interface{}{"tag": "H2", "protocol": "hysteria2", "security": "tls", "hysteria2_obfs_password": "obfs2"},
		map[string]interface{}{"tag": "T", "protocol": "tuic", "security": "tls", "congestion_control": "bbr", "zero_rtt_handshake": true},
	})

	// The same document straight back, which is what Apply sends after any
	// unrelated edit elsewhere in it.
	second := put(first["inbounds"].([]interface{}))

	byTag := map[string]map[string]interface{}{}
	for _, raw := range second["inbounds"].([]interface{}) {
		in := raw.(map[string]interface{})
		byTag[in["tag"].(string)] = in
	}
	want := map[string]map[string]interface{}{
		"S":  {"snell_psk": "a-long-enough-psk", "snell_v6_mode": "unshaped"},
		"ST": {"shadowtls_inner_method": "2022-blake3-aes-256-gcm", "shadowtls_inner_password": inner},
		"H1": {"up_mbps": float64(100), "down_mbps": float64(200), "hysteria_obfs_password": "obfs1"},
		"H2": {"hysteria2_obfs_password": "obfs2"},
		"T":  {"congestion_control": "bbr", "zero_rtt_handshake": true},
	}
	for tag, fields := range want {
		for field, value := range fields {
			if byTag[tag][field] != value {
				t.Errorf("%s.%s = %v, want %v", tag, field, byTag[tag][field], value)
			}
		}
	}
}

func TestCoreSnapshotPreviewSendsConfigAsJSON(t *testing.T) {
	raw, err := json.Marshal(coreSnapshotPreview(generated.CoreConfigSnapshot{ID: 7, Reason: "PUT /api/settings/xray-config", Config: []byte(`{"snapshot_version":1}`)}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID     int64          `json:"id"`
		Reason string         `json:"reason"`
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("config did not arrive as an object: %v (%s)", err, raw)
	}
	if got.ID != 7 || got.Reason != "PUT /api/settings/xray-config" || got.Config["snapshot_version"] != float64(1) {
		t.Fatalf("unexpected preview: %s", raw)
	}
}
