package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/legendary1205/rapido-go/internal/subscription"
)

// The profile Happ and INCY get, as internal/subscription's own test pins it;
// repeated here so the header is checked end to end, not only the encoder.
const wantHappProfileJSON = `{"Name":"Iran direct","GlobalProxy":"true","DomainStrategy":"IPIfNonMatch",` +
	`"Geoipurl":"https://raw.githubusercontent.com/Chocolate4U/Iran-v2ray-rules/release/geoip-lite.dat",` +
	`"Geositeurl":"https://raw.githubusercontent.com/Chocolate4U/Iran-v2ray-rules/release/geosite-lite.dat",` +
	`"DirectSites":["domain:ir","geosite:category-ir"],"DirectIp":["geoip:ir","geoip:private"],` +
	`"ProxySites":[],"ProxyIp":[],"BlockSites":[],"BlockIp":[]}`

func TestRoutingProfileHeaderGoesOnlyToHappAndIncyShareLinks(t *testing.T) {
	link := subscription.HappIranDirectRouting()
	for _, tc := range []struct {
		name, userAgent, format string
		on                      bool
		want                    string
	}{
		{"Happ on share links", "Happ/3.1.2/ios", "v2ray", true, link},
		{"Happ with the setting off", "Happ/3.1.2/ios", "v2ray", false, ""},
		{"Happ on a JSON subscription", "Happ/3.1.2/Android", "v2ray-json", true, ""},
		{"Happ asking for sing-box", "Happ/3.1.2/Android", "sing-box", true, ""},
		{"Happ asking for outline", "Happ/3.1.2/Android", "outline", true, ""},
		{"Happ asking for clash", "Happ/3.1.2/Android", "clash", true, link},
		{"INCY on share links", "INCY/1.4.0/android", "v2ray", true, link},
		{"v2rayNG", "v2rayNG/1.9.0", "v2ray", true, ""},
		{"V2Box", "V2Box 9.6.2", "v2ray", true, ""},
		{"v2RayTun", "v2raytun/android", "v2ray", true, ""},
		{"Streisand", "Streisand", "v2ray", true, ""},
		{"no User-Agent", "", "v2ray", true, ""},
		{"Happ's name further along", "Mozilla/5.0 Happ/3.1.2", "v2ray", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := routingProfileHeader(tc.userAgent, tc.format, tc.on); got != tc.want {
				t.Errorf("routingProfileHeader(%q, %q, %v) = %q, want %q", tc.userAgent, tc.format, tc.on, got, tc.want)
			}
		})
	}
}

// iranDirectFixture is one user with one VLESS host, and the panel's handler
// so a test can change its environment defaults and templates.
type iranDirectFixture struct {
	router   http.Handler
	token    string
	h        *Handler
	subToken string
}

func newIranDirectFixture(t *testing.T) iranDirectFixture {
	t.Helper()
	router, token, h := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{
		{"tag": "VLESS TCP", "protocol": "vless", "network": "tcp", "security": "tls"},
	})
	doRequest(t, router, "PUT", "/api/hosts", token, map[string]interface{}{
		"VLESS TCP": []map[string]interface{}{{"remark": "Node", "address": "1.2.3.4", "port": 443, "sni": "example.com", "security": "tls"}},
	})
	if resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "iran_direct_user", "proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	}); resp.Code != 200 {
		t.Fatalf("create user: %d %v", resp.Code, resp.Body)
	}
	return iranDirectFixture{router: router, token: token, h: h, subToken: subscription.CreateToken("iran_direct_user", []byte(testSubSecret))}
}

// setIranDirect stores the dashboard's choice: true, false, or nil for
// "use SUBSCRIPTION_IRAN_DIRECT".
func (fx iranDirectFixture) setIranDirect(t *testing.T, v *bool) {
	t.Helper()
	resp := doRequest(t, fx.router, "PUT", "/api/settings/integrations", fx.token, map[string]interface{}{"subscription_iran_direct": v})
	if resp.Code != 200 {
		t.Fatalf("PUT subscription_iran_direct: %d %s", resp.Code, resp.Raw)
	}
	want := fx.h.envDefaults.SubscriptionIranDirect
	if v != nil {
		want = *v
	}
	if resp.Body["subscription_iran_direct"] != want {
		t.Fatalf("subscription_iran_direct = %v after saving %v, want %v", resp.Body["subscription_iran_direct"], v, want)
	}
}

func (fx iranDirectFixture) fetch(t *testing.T, path, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("User-Agent", userAgent)
	rec := httptest.NewRecorder()
	fx.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s as %q: %d %s", path, userAgent, rec.Code, rec.Body.String())
	}
	return rec
}

func ptrBool(v bool) *bool { return &v }

func TestHappGetsTheIranDirectProfileWhileTheSettingIsOn(t *testing.T) {
	fx := newIranDirectFixture(t)
	fx.setIranDirect(t, ptrBool(true))

	rec := fx.fetch(t, "/sub/"+fx.subToken, "Happ/3.1.2/ios")
	header := rec.Header().Get("routing")
	encoded, ok := strings.CutPrefix(header, "happ://routing/onadd/")
	if !ok {
		t.Fatalf("routing header = %q, want a happ://routing/onadd/ link", header)
	}
	profile, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("routing header is not base64: %v", err)
	}
	if string(profile) != wantHappProfileJSON {
		t.Errorf("profile =\n%s\nwant\n%s", profile, wantHappProfileJSON)
	}
	// The body is still the plain share links Happ was getting.
	links, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil || !strings.HasPrefix(string(links), "vless://") {
		t.Errorf("body is no longer plain share links: %q (%v)", rec.Body.String(), err)
	}

	// INCY reads the same header.
	if got := fx.fetch(t, "/sub/"+fx.subToken, "INCY/1.4.0/android").Header().Get("routing"); got != header {
		t.Errorf("INCY routing header = %q, want %q", got, header)
	}
	// Other apps never see it.
	for _, ua := range []string{"v2rayNG/1.9.0", "V2Box 9.6.2", "SFA/1.12.0", "ClashMetaForAndroid/2.10"} {
		if got := fx.fetch(t, "/sub/"+fx.subToken, ua).Header().Get("routing"); got != "" {
			t.Errorf("%s got a routing header: %q", ua, got)
		}
	}
	// Nor does Happ on a JSON subscription, which ignores routing profiles.
	if got := fx.fetch(t, "/sub/"+fx.subToken+"/v2ray-json", "Happ/3.1.2/ios").Header().Get("routing"); got != "" {
		t.Errorf("Happ on v2ray-json got a routing header: %q", got)
	}
}

func TestHappGetsNoProfileWhileTheSettingIsOff(t *testing.T) {
	fx := newIranDirectFixture(t)
	fx.h.envDefaults.SubscriptionIranDirect = true // the dashboard's "off" must win
	fx.setIranDirect(t, ptrBool(false))

	if got := fx.fetch(t, "/sub/"+fx.subToken, "Happ/3.1.2/ios").Header().Get("routing"); got != "" {
		t.Errorf("routing header = %q with the setting off", got)
	}
}

// TestIranDirectFollowsTheEnvironmentUntilTheDashboardChoosesForIt: a NULL
// column means SUBSCRIPTION_IRAN_DIRECT decides, which is on by default.
func TestIranDirectFollowsTheEnvironmentUntilTheDashboardChoosesForIt(t *testing.T) {
	fx := newIranDirectFixture(t)
	fx.h.envDefaults.SubscriptionIranDirect = true

	resp := doRequest(t, fx.router, "GET", "/api/settings/integrations", fx.token, nil)
	if resp.Body["subscription_iran_direct"] != true {
		t.Errorf("GET subscription_iran_direct = %v, want the environment's true", resp.Body["subscription_iran_direct"])
	}
	if fx.fetch(t, "/sub/"+fx.subToken, "Happ/3.1.2/ios").Header().Get("routing") == "" {
		t.Error("no routing header while SUBSCRIPTION_IRAN_DIRECT is on and the dashboard never chose")
	}

	fx.setIranDirect(t, ptrBool(false))
	fx.setIranDirect(t, nil) // reset to .env
	if fx.fetch(t, "/sub/"+fx.subToken, "Happ/3.1.2/ios").Header().Get("routing") == "" {
		t.Error("no routing header after resetting the dashboard's choice to the environment's (on)")
	}

	if resp := doRequest(t, fx.router, "PUT", "/api/settings/integrations", fx.token, map[string]interface{}{"subscription_iran_direct": "yes"}); resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("a non-boolean was accepted: %d %s", resp.Code, resp.Raw)
	}
}

func TestSingBoxSubscriptionSendsIranianTrafficDirect(t *testing.T) {
	fx := newIranDirectFixture(t)
	route := func() map[string]any {
		var doc map[string]any
		if err := json.Unmarshal(fx.fetch(t, "/sub/"+fx.subToken, "SFA/1.12.0").Body.Bytes(), &doc); err != nil {
			t.Fatalf("sing-box profile is not JSON: %v", err)
		}
		return doc["route"].(map[string]any)
	}

	fx.setIranDirect(t, ptrBool(true))
	ruleSets, _ := route()["rule_set"].([]any)
	if len(ruleSets) != 2 {
		t.Fatalf("route.rule_set = %v, want geosite-ir and geoip-ir", ruleSets)
	}

	fx.setIranDirect(t, ptrBool(false))
	if rs, present := route()["rule_set"]; present {
		t.Errorf("route.rule_set = %v with the setting off", rs)
	}
}

func TestV2rayJSONSubscriptionRoutesIranDirectOnlyWithoutATemplate(t *testing.T) {
	fx := newIranDirectFixture(t)
	fx.setIranDirect(t, ptrBool(true))
	profile := func() map[string]any {
		var configs []map[string]any
		if err := json.Unmarshal(fx.fetch(t, "/sub/"+fx.subToken+"/v2ray-json", "v2rayNG/1.9.0").Body.Bytes(), &configs); err != nil || len(configs) != 1 {
			t.Fatalf("v2ray-json body: %v (%d configs)", err, len(configs))
		}
		return configs[0]
	}

	routing, ok := profile()["routing"].(map[string]any)
	if !ok || routing["domainStrategy"] != "AsIs" || len(routing["rules"].([]any)) != 2 {
		t.Errorf("built-in profile routing = %v, want the two Iran direct rules", profile()["routing"])
	}

	// An operator's template comes out as written, routing included.
	path := filepath.Join(t.TempDir(), "v2ray.json")
	template := `{"routing":{"domainStrategy":"IPIfNonMatch","rules":[{"type":"field","outboundTag":"block","domain":["geosite:category-ads-all"]}]},` +
		`"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
	if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.h.v2rayTemplatePath = path
	want := map[string]any{"domainStrategy": "IPIfNonMatch", "rules": []any{
		map[string]any{"type": "field", "outboundTag": "block", "domain": []any{"geosite:category-ads-all"}},
	}}
	if got := profile()["routing"]; !reflect.DeepEqual(got, want) {
		t.Errorf("templated profile routing = %v, want the template's own %v", got, want)
	}
}

// TestClashSubscriptionWorksWithIranDirectOff: Clash keeps its template's
// own rules either way; the setting must not break it.
func TestClashSubscriptionWorksWithIranDirectOff(t *testing.T) {
	fx := newIranDirectFixture(t)
	fx.setIranDirect(t, ptrBool(false))
	body := fx.fetch(t, "/sub/"+fx.subToken, "ClashMetaForAndroid/2.10").Body.String()
	if !strings.Contains(body, "proxies:") || !strings.Contains(body, "RULE-SET,ir,DIRECT") {
		t.Errorf("Clash Meta profile lost its proxies or its built-in Iran rules:\n%s", body)
	}
}

// TestSavingOneIntegrationSettingKeepsTelegramTopics: every save used to
// clear the forum topic ids, whatever it was for - toggling Iran direct
// included.
func TestSavingOneIntegrationSettingKeepsTelegramTopics(t *testing.T) {
	fx := newIranDirectFixture(t)
	pool := testPool(t)
	if _, err := pool.Exec(t.Context(), `UPDATE integration_settings SET telegram_logger_channel_id = -1001, telegram_topic_ids = '{"login": 7}'`); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.store.InvalidateIntegrationSettings(t.Context()); err != nil {
		t.Fatal(err)
	}

	fx.setIranDirect(t, ptrBool(false))
	resp := doRequest(t, fx.router, "GET", "/api/settings/integrations", fx.token, nil)
	if topics, _ := resp.Body["telegram_topic_ids"].(map[string]any); topics["login"] != float64(7) {
		t.Errorf("telegram_topic_ids = %v after saving an unrelated setting, want {login: 7}", resp.Body["telegram_topic_ids"])
	}

	// Moving the logger to another group leaves the old group's topics behind.
	doRequest(t, fx.router, "PUT", "/api/settings/integrations", fx.token, map[string]interface{}{"telegram_logger_channel_id": -1002})
	resp = doRequest(t, fx.router, "GET", "/api/settings/integrations", fx.token, nil)
	if resp.Body["telegram_topic_ids"] != nil {
		t.Errorf("telegram_topic_ids = %v after the logger group changed, want none", resp.Body["telegram_topic_ids"])
	}
}
