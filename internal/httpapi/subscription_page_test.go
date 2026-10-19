package httpapi

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/legendary1205/rapido-go/internal/subscription"
)

// TestSubscriptionPageServedForHTMLAccept proves the real content
// negotiation branch (Accept-header based, not User-Agent) actually wires
// through to a real rendered page - not just that the embedded template
// parses (already proven at package init by every other test in this
// binary even running at all).
func TestSubscriptionPageServedForHTMLAccept(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{
		{"tag": "VLESS TCP", "protocol": "vless", "network": "tcp", "security": "tls"},
	})
	doRequest(t, router, "PUT", "/api/hosts", token, map[string]interface{}{
		"VLESS TCP": []map[string]interface{}{{"remark": "Node", "address": "1.2.3.4", "port": 443, "sni": "example.com", "security": "tls"}},
	})
	resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "html_page_test_user", "proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	if resp.Code != 200 {
		t.Fatalf("create user: %d %v", resp.Code, resp.Body)
	}

	subToken := subscription.CreateToken("html_page_test_user", []byte(testSubSecret))
	req := httptest.NewRequest(http.MethodGet, "/sub/"+subToken, nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an HTML Accept header, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("missing/wrong CSP header: %q", csp)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", rec.Header().Get("X-Frame-Options"))
	}

	body := rec.Body.String()
	if !strings.Contains(body, "html_page_test_user") {
		t.Error("rendered page does not contain the username anywhere")
	}
	if !strings.Contains(body, "window.__RAPIDO__") {
		t.Error("rendered page is missing the window.__RAPIDO__ data blob")
	}
	if !strings.Contains(body, `"status":"active"`) {
		t.Errorf("rendered page's data blob missing expected status field: looking in body of length %d", len(body))
	}
	if !strings.Contains(body, "1.2.3.4") {
		t.Error("rendered page's embedded links don't contain the configured host address - buildUserLinks wiring broken")
	}
}

// TestSubscriptionRawConfigStillServedForNonHTMLAccept proves the new
// Accept-header branch didn't break the existing raw-config delivery path
// real VPN client apps depend on.
func TestSubscriptionRawConfigStillServedForNonHTMLAccept(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{{"tag": "VLESS TCP", "protocol": "vless"}})
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "raw_config_still_works_user", "proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	subToken := subscription.CreateToken("raw_config_still_works_user", []byte(testSubSecret))

	req := httptest.NewRequest(http.MethodGet, "/sub/"+subToken, nil)
	req.Header.Set("Accept", "*/*")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a non-HTML Accept header, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "window.__RAPIDO__") {
		t.Error("a non-HTML Accept request got the HTML page instead of raw subscription content")
	}
}

// TestSubscriptionPageDoesNotUpdateSubUserAgent mirrors the Python system's
// own behavior: loading the browser page must not bump
// sub_updated_at/sub_last_user_agent (those only track real VPN client
// fetches), unlike GET /sub/:token's raw-config branch. Verified directly
// against the column, not just "the request didn't crash".
func TestSubscriptionPageDoesNotUpdateSubUserAgent(t *testing.T) {
	router, token := newTestRouter(t)
	pool := testPool(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]interface{}{{"tag": "VLESS TCP", "protocol": "vless"}})
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "html_no_useragent_bump_user", "proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	subToken := subscription.CreateToken("html_no_useragent_bump_user", []byte(testSubSecret))

	req := httptest.NewRequest(http.MethodGet, "/sub/"+subToken, nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "some-real-browser/1.0")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get html page: %d", rec.Code)
	}

	var lastUserAgent *string
	if err := pool.QueryRow(context.Background(),
		"SELECT sub_last_user_agent FROM users WHERE username = $1", "html_no_useragent_bump_user",
	).Scan(&lastUserAgent); err != nil {
		t.Fatalf("query sub_last_user_agent: %v", err)
	}
	if lastUserAgent != nil {
		t.Errorf("sub_last_user_agent = %q, want NULL - loading the HTML page must not record it (see recordSubUserAgent's callers)", *lastUserAgent)
	}

	// Sanity check the opposite: a real raw-config hit (any non-HTML
	// Accept) DOES record it, proving this isn't just a broken/no-op write
	// path in general.
	req = httptest.NewRequest(http.MethodGet, "/sub/"+subToken, nil)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "v2rayNG/1.8.0")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get raw config: %d", rec.Code)
	}
	if err := pool.QueryRow(context.Background(),
		"SELECT sub_last_user_agent FROM users WHERE username = $1", "html_no_useragent_bump_user",
	).Scan(&lastUserAgent); err != nil {
		t.Fatalf("query sub_last_user_agent: %v", err)
	}
	if lastUserAgent == nil || *lastUserAgent != "v2rayNG/1.8.0" {
		t.Errorf("sub_last_user_agent = %v, want \"v2rayNG/1.8.0\" after a real raw-config fetch", lastUserAgent)
	}
}

// renderSubscriptionPageForTest executes the real embedded template the way
// handleSubscriptionPage does, without needing a database.
func renderSubscriptionPageForTest(t *testing.T, username, dataJSON string) string {
	t.Helper()
	var buf bytes.Buffer
	err := subscriptionPageTemplate.Execute(&buf, struct {
		Username       string
		RapidoDataJSON template.JS
	}{Username: username, RapidoDataJSON: template.JS(dataJSON)})
	if err != nil {
		t.Fatalf("execute subscription page template: %v", err)
	}
	return buf.String()
}

// TestSubscriptionPageTemplateRendersSelfContained proves, without a
// database, that the page renders, escapes the username, carries the data
// blob and stays inside the strict CSP: no external script, stylesheet or
// image, and fonts only from the same origin.
func TestSubscriptionPageTemplateRendersSelfContained(t *testing.T) {
	body := renderSubscriptionPageForTest(t, `</title><script>alert(1)</script>`,
		`{"username":"x","status":"active","used":1,"lifetime":1,"limit":null,"reset":"no_reset","expire":null,"onlineAt":null,"emergencyUsed":false,"links":[],"subUrl":"/sub/x"}`)

	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("username was not escaped in the rendered page")
	}
	if !strings.Contains(body, `window.__RAPIDO__ = {"username":"x"`) {
		t.Error("data blob missing from the rendered page")
	}
	// The instrument cluster's hooks: the script looks these up by id at boot,
	// and a missing one sends the customer to the no-JavaScript fallback.
	for _, id := range []string{
		"heroBox", "expBox", "uBig", "eBig", "barUsed", "barLeft", "rdUsed", "rdLeft", "rdLimit", "rdLife",
		"subQr", "emgBtn", "emgLabel", "emgWhy", "emgDesc", "emgMsg", "emgZone", "statusPill", "seenPill",
		"fxBtn", "world", "plate", "platSeg", "appRows", "srvRows", "ticketList", "toast", "overlay",
	} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("rendered page is missing the #%s element the script boots with", id)
		}
	}

	for _, banned := range []string{"<link ", "@import", `<script src`, `<img `, "<iframe"} {
		if strings.Contains(body, banned) {
			t.Errorf("rendered page contains %q, which the CSP does not allow", banned)
		}
	}
	for _, m := range regexp.MustCompile(`url\(\s*["']?([^"')\s]+)`).FindAllStringSubmatch(body, -1) {
		if m[1] != "#" && !strings.HasPrefix(m[1], "/statics/fonts/") && !strings.HasPrefix(m[1], "#") {
			t.Errorf("CSS url(%s) is not a same-origin font", m[1])
		}
	}
}

// TestSubscriptionPageDictionariesStayInStep guards the bilingual page:
// every key one language defines must exist in the other, or a Persian
// customer silently gets English (the lookup falls back to it).
func TestSubscriptionPageDictionariesStayInStep(t *testing.T) {
	src := subscriptionPageSource
	start := strings.Index(src, "window.__I18N_DICT__")
	faAt := strings.Index(src[start:], "\n            fa: {")
	if start < 0 || faAt < 0 {
		t.Fatal("could not locate the en/fa dictionaries in the template")
	}
	enPart, faPart := src[start:start+faAt], src[start+faAt:]
	// The Persian block ends at the dictionary's closing brace; later script
	// holds other quoted maps that are not translations.
	if end := strings.Index(faPart, "\n        };"); end > 0 {
		faPart = faPart[:end]
	}
	keyRe := regexp.MustCompile(`(?m)^\s*'([a-zA-Z0-9_.]+)':`)
	keys := func(part string) map[string]bool {
		out := map[string]bool{}
		for _, m := range keyRe.FindAllStringSubmatch(part, -1) {
			out[m[1]] = true
		}
		return out
	}
	en, fa := keys(enPart), keys(faPart)
	if len(en) < 150 {
		t.Fatalf("only %d English keys found - the dictionary parser is broken", len(en))
	}
	for k := range en {
		if !fa[k] {
			t.Errorf("key %q is missing from the Persian dictionary", k)
		}
	}
	for k := range fa {
		if !en[k] {
			t.Errorf("key %q is missing from the English dictionary", k)
		}
	}
}
