package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func doFormLogin(t *testing.T, router http.Handler, username, password string) int {
	t.Helper()
	form := url.Values{"username": {username}, "password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code
}

func subTokenOf(t *testing.T, body map[string]interface{}) string {
	t.Helper()
	link, _ := body["subscription_url"].(string)
	i := strings.Index(link, "/sub/")
	if i < 0 {
		t.Fatalf("no subscription url in %v", body)
	}
	return link[i+len("/sub/"):]
}

// Whoever holds a subscription link can read this endpoint, and that's the
// customer, not the admin: what the admin wrote about them, and how to reach the
// admin, are none of their business.
func TestSubscriptionInfoKeepsTheAdminsThingsPrivate(t *testing.T) {
	router, sudo := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", sudo, []map[string]string{{"tag": "V", "protocol": "vless"}})
	doRequest(t, router, "POST", "/api/admin", sudo, map[string]interface{}{
		"username": "shop", "password": "pw123456", "is_sudo": false,
		"telegram_id": 987654321, "discord_webhook": "https://discord.com/api/webhooks/secret-hook",
	})
	shop := loginAs(t, router, "shop", "pw123456")
	created := doRequest(t, router, "POST", "/api/user", shop, map[string]interface{}{
		"username": "customer", "note": "pays late, do not trust",
		"proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	if created.Code != http.StatusOK {
		t.Fatalf("%d %v", created.Code, created.Body)
	}
	token := subTokenOf(t, created.Body)

	resp := doRequest(t, router, "GET", "/sub/"+token+"/info", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %s", resp.Code, resp.Raw)
	}
	raw := string(resp.Raw)
	for _, secret := range []string{"pays late", "987654321", "secret-hook"} {
		if strings.Contains(raw, secret) {
			t.Errorf("/info gives away %q: %s", secret, raw)
		}
	}
	if resp.Body["admin"] != nil || resp.Body["note"] != nil {
		t.Errorf("admin = %v, note = %v", resp.Body["admin"], resp.Body["note"])
	}
	// What the customer needs is still all there.
	if resp.Body["username"] != "customer" || resp.Body["status"] != "active" {
		t.Errorf("body = %v", resp.Body)
	}
}

// A token minted on the strength of another one could carry a later timestamp
// than a revocation a moment afterwards, and so survive it.
func TestSubscriptionInfoHandsBackTheLinkItWasAskedWith(t *testing.T) {
	router, sudo := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", sudo, []map[string]string{{"tag": "V", "protocol": "vless"}})
	created := doRequest(t, router, "POST", "/api/user", sudo, map[string]interface{}{
		"username": "linkholder", "proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	token := subTokenOf(t, created.Body)
	time.Sleep(1100 * time.Millisecond)

	resp := doRequest(t, router, "GET", "/sub/"+token+"/info", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %s", resp.Code, resp.Raw)
	}
	if got := subTokenOf(t, resp.Body); got != token {
		t.Errorf("minted a new token: %s", got)
	}
	var urls struct {
		SubURL string `json:"subUrl"`
	}
	if err := json.Unmarshal(resp.Raw, &urls); err == nil && urls.SubURL != "" && !strings.HasSuffix(urls.SubURL, token) {
		t.Errorf("subUrl = %s", urls.SubURL)
	}
}

// Any one working login used to reset the count, so whoever had one could keep
// guessing at every other admin's password for as long as they liked.
func TestALoginThatWorksDoesNotResetTheFailureCount(t *testing.T) {
	router, sudo := newTestRouter(t)
	doRequest(t, router, "POST", "/api/admin", sudo, map[string]interface{}{"username": "real_reseller", "password": "right-password", "is_sudo": false})
	login := func(user, password string) int {
		return doFormLogin(t, router, user, password)
	}
	for i := 0; i < loginRateLimitMax-1; i++ {
		if code := login("victim", "guess"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code := login("real_reseller", "right-password"); code != http.StatusOK {
		t.Fatalf("the working login: %d", code)
	}
	login("victim", "guess")
	if code := login("victim", "guess"); code != http.StatusTooManyRequests {
		t.Errorf("attempt past the limit, with a success in between: %d, want 429", code)
	}
}

func TestAnEmptyBootstrapPasswordLogsNobodyIn(t *testing.T) {
	router, _, h := newTestRouterAndHandler(t)
	h.sudoPassword = ""
	if code := doFormLogin(t, router, h.sudoUsername, ""); code == http.StatusOK {
		t.Error("an empty password logged in as the bootstrap owner")
	}
}

// resellerAPIStub answers the configs lookup with allowed and the limit lookup with limit.
func resellerAPIStub(t *testing.T, allowed string, limit *atomic.Int64) string {
	t.Helper()
	server := newCaptureServer(t, func(w http.ResponseWriter, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if len(body) > 0 {
			_, _ = w.Write([]byte(allowed))
			return
		}
		fmt.Fprintf(w, `{"limit": %d, "users_limit": %d}`, limit.Load(), limit.Load())
	})
	return server.URL
}

// Hiding an inbound from a reseller's picker keeps nobody off it who sends its tag.
func TestAResellerCannotPutUsersOnInboundsTheyAreNotOffered(t *testing.T) {
	router, sudo := newTestRouter(t)
	limit := &atomic.Int64{}
	limit.Store(1000)
	api := resellerAPIStub(t, `{"vmess": ["Allowed"]}`, limit)
	doRequest(t, router, "PUT", "/api/settings/integrations", sudo, map[string]interface{}{"reseller_api_secret": "s", "reseller_api_url": api})
	doRequest(t, router, "POST", "/api/inbounds/sync", sudo, []map[string]string{
		{"tag": "Allowed", "protocol": "vmess"}, {"tag": "Premium", "protocol": "vmess"}, {"tag": "Trojan", "protocol": "trojan"},
	})
	doRequest(t, router, "POST", "/api/admin", sudo, map[string]interface{}{"username": "scoped", "password": "pw123456", "is_sudo": false})
	shop := loginAs(t, router, "scoped", "pw123456")
	vmess := map[string]interface{}{"vmess": map[string]interface{}{}}

	named := doRequest(t, router, "POST", "/api/user", shop, map[string]interface{}{
		"username": "sneaky", "proxies": vmess, "inbounds": map[string][]string{"vmess": {"Premium"}},
	})
	if named.Code != http.StatusForbidden {
		t.Errorf("naming a hidden inbound: %d %v, want 403", named.Code, named.Body)
	}
	other := doRequest(t, router, "POST", "/api/user", shop, map[string]interface{}{
		"username": "sneaky2", "proxies": map[string]interface{}{"trojan": map[string]interface{}{}},
	})
	if other.Code != http.StatusForbidden {
		t.Errorf("a protocol not offered: %d %v, want 403", other.Code, other.Body)
	}
	// A protocol this panel has no inbound of has nothing to protect: a bot whose
	// plan still names one keeps working, as it always has.
	orphan := doRequest(t, router, "POST", "/api/user", shop, map[string]interface{}{
		"username": "with_orphan", "proxies": map[string]interface{}{"vmess": map[string]interface{}{}, "vless": map[string]interface{}{}},
	})
	if orphan.Code != http.StatusOK {
		t.Errorf("a protocol with no inbound at all: %d %v, want 200", orphan.Code, orphan.Body)
	}

	// Naming none must mean what they're offered, not everything.
	plain := doRequest(t, router, "POST", "/api/user", shop, map[string]interface{}{"username": "plain", "proxies": vmess})
	if plain.Code != http.StatusOK {
		t.Fatalf("%d %v", plain.Code, plain.Body)
	}
	inbounds := plain.Body["inbounds"].(map[string]interface{})["vmess"].([]interface{})
	if len(inbounds) != 1 || inbounds[0] != "Allowed" {
		t.Errorf("inbounds = %v, want only the offered one", inbounds)
	}

	edit := doRequest(t, router, "PUT", "/api/user/plain", shop, map[string]interface{}{"inbounds": map[string][]string{"vmess": {"Premium"}}})
	if edit.Code != http.StatusForbidden {
		t.Errorf("editing onto a hidden inbound: %d %v, want 403", edit.Code, edit.Body)
	}
	if resp := doRequest(t, router, "PUT", "/api/user/plain", shop, map[string]interface{}{"note": "fine"}); resp.Code != http.StatusOK {
		t.Errorf("an edit naming no inbounds: %d %v", resp.Code, resp.Body)
	}

	// Sudo is not limited by any of this.
	if resp := doRequest(t, router, "POST", "/api/user", sudo, map[string]interface{}{
		"username": "by_sudo", "proxies": vmess, "inbounds": map[string][]string{"vmess": {"Premium"}},
	}); resp.Code != http.StatusOK {
		t.Errorf("sudo: %d %v", resp.Code, resp.Body)
	}
}

func TestResettingAnOnHoldUserLeavesThemOnHold(t *testing.T) {
	router, sudo := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", sudo, []map[string]string{{"tag": "V", "protocol": "vmess"}})
	created := doRequest(t, router, "POST", "/api/user", sudo, map[string]interface{}{
		"username": "held", "status": "on_hold", "on_hold_expire_duration": 86400,
		"proxies": map[string]interface{}{"vmess": map[string]interface{}{}},
	})
	if created.Code != http.StatusOK {
		t.Fatalf("%d %v", created.Code, created.Body)
	}
	resp := doRequest(t, router, "POST", "/api/user/held/reset", sudo, nil)
	if resp.Code != http.StatusOK || resp.Body["status"] != "on_hold" {
		t.Errorf("%d status = %v, want on_hold: active here would mean no expiry, ever", resp.Code, resp.Body["status"])
	}
}

func TestAResellerSeesOnlyTheirOwnNumbers(t *testing.T) {
	router, sudo, h := newTestRouterAndHandler(t)
	if _, err := h.store.Pool.Exec(t.Context(), `UPDATE system SET uplink = 111, downlink = 222`); err != nil {
		t.Fatal(err)
	}
	doRequest(t, router, "POST", "/api/admin", sudo, map[string]interface{}{"username": "own_numbers", "password": "pw123456", "is_sudo": false})
	shop := loginAs(t, router, "own_numbers", "pw123456")

	stats := doRequest(t, router, "GET", "/api/system", shop, nil)
	if stats.Code != http.StatusOK {
		t.Fatalf("%d %v", stats.Code, stats.Body)
	}
	if stats.Body["incoming_bandwidth"] != float64(0) || stats.Body["outgoing_bandwidth"] != float64(0) {
		t.Errorf("a reseller sees the panel's traffic: %v", stats.Body)
	}
	if own := doRequest(t, router, "GET", "/api/system", sudo, nil); own.Body["incoming_bandwidth"] != float64(111) {
		t.Errorf("sudo lost the panel's traffic: %v", own.Body)
	}
}

// These go into shell commands and unit files on a remote root account.
func TestAddressesThatWouldReachAShellAreRefused(t *testing.T) {
	router, sudo := newTestRouter(t)
	evil := []string{"1.2.3.4; reboot", "$(reboot)", "a b", "fe80::1%x;reboot", "`id`", "-oProxyCommand=x", ""}
	for _, address := range evil {
		resp := doRequest(t, router, "POST", "/api/node", sudo, map[string]interface{}{
			"name": "evil-" + address, "address": address, "port": 443, "api_port": 62051,
		})
		if resp.Code == http.StatusOK {
			t.Errorf("node address %q was accepted", address)
		}
	}
	good := doRequest(t, router, "POST", "/api/node", sudo, map[string]interface{}{
		"name": "fine", "address": "node-1.example.com", "port": 443, "api_port": 62051,
	})
	if good.Code != http.StatusOK {
		t.Fatalf("a host name was refused: %d %v", good.Code, good.Body)
	}
	id := int(good.Body["node"].(map[string]interface{})["id"].(float64))
	if resp := doRequest(t, router, "PUT", fmt.Sprintf("/api/node/%d", id), sudo, map[string]interface{}{"address": "1.2.3.4;reboot"}); resp.Code == http.StatusOK {
		t.Error("an edit changed the address to a shell command")
	}
}

func TestATorOutboundCannotRunWhatThePanelNames(t *testing.T) {
	router, sudo := newTestRouter(t)
	resp := doRequest(t, router, "PUT", "/api/settings/core-config", sudo, map[string]interface{}{
		"log_level": "warn", "outbounds": []map[string]interface{}{{"tag": "t", "type": "tor", "executable_path": "/bin/sh"}},
	})
	if resp.Code == http.StatusOK {
		t.Error("a sudo admin could make every node run a program as root")
	}
}

// A token that may well be fine, and a database that hiccupped: a 401 would log
// every admin out of the dashboard at once.
func TestAFailedAdminLookupIsNotALogout(t *testing.T) {
	router, sudo := newTestRouter(t)
	doRequest(t, router, "POST", "/api/admin", sudo, map[string]interface{}{"username": "lookup_admin", "password": "pw123456", "is_sudo": false})
	token := loginAs(t, router, "lookup_admin", "pw123456")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/api/users", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a failed lookup answered %d, want 503", rec.Code)
	}
	if resp := doRequest(t, router, "GET", "/api/users", token, nil); resp.Code != http.StatusOK {
		t.Errorf("once the database answers: %d %v", resp.Code, resp.Body)
	}
}

func TestDeletingAnAdminWhoStillOwnsUsersSaysSo(t *testing.T) {
	router, sudo, h := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", sudo, []map[string]string{{"tag": "V", "protocol": "vmess"}})
	doRequest(t, router, "POST", "/api/admin", sudo, map[string]interface{}{"username": "with_users", "password": "pw123456", "is_sudo": false})
	shop := loginAs(t, router, "with_users", "pw123456")
	doRequest(t, router, "POST", "/api/user", shop, map[string]interface{}{"username": "owned_one", "proxies": map[string]interface{}{"vmess": map[string]interface{}{}}})

	resp := doRequest(t, router, "DELETE", "/api/admin/with_users", sudo, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("%d %v, want 409", resp.Code, resp.Body)
	}
	if detail, _ := resp.Body["detail"].(string); !strings.Contains(detail, "1 users") {
		t.Errorf("detail = %q", detail)
	}

	// With no users, and even with a usage reset in its past, it can go.
	doRequest(t, router, "DELETE", "/api/user/owned_one", sudo, nil)
	if _, err := h.store.Pool.Exec(t.Context(), `INSERT INTO admin_usage_logs (admin_id, used_traffic_at_reset) SELECT id, 1 FROM admins WHERE username = 'with_users'`); err != nil {
		t.Fatal(err)
	}
	if resp := doRequest(t, router, "DELETE", "/api/admin/with_users", sudo, nil); resp.Code != http.StatusOK {
		t.Errorf("%d %v", resp.Code, resp.Body)
	}
}

// The reseller API is advisory. A bot that answers nothing useful, about inbounds
// that exist, must not stop any reseller from creating users.
func TestAResellerAPIAnswerNamingNothingRestrictsNothing(t *testing.T) {
	for name, answer := range map[string]string{"empty": `{}`, "only unknown tags": `{"vmess": ["no-such-inbound"]}`} {
		t.Run(name, func(t *testing.T) {
			router, sudo := newTestRouter(t)
			limit := &atomic.Int64{}
			limit.Store(1000)
			api := resellerAPIStub(t, answer, limit)
			doRequest(t, router, "PUT", "/api/settings/integrations", sudo, map[string]interface{}{"reseller_api_secret": "s", "reseller_api_url": api})
			doRequest(t, router, "POST", "/api/inbounds/sync", sudo, []map[string]string{{"tag": "Main", "protocol": "vmess"}, {"tag": "Second", "protocol": "vmess"}})
			doRequest(t, router, "POST", "/api/admin", sudo, map[string]interface{}{"username": "advisory", "password": "pw123456", "is_sudo": false})
			shop := loginAs(t, router, "advisory", "pw123456")
			resp := doRequest(t, router, "POST", "/api/user", shop, map[string]interface{}{
				"username": "advised_user", "proxies": map[string]interface{}{"vmess": map[string]interface{}{}}, "inbounds": map[string][]string{"vmess": {"Main"}},
			})
			if resp.Code != http.StatusOK {
				t.Errorf("%d %v, want 200", resp.Code, resp.Body)
			}
		})
	}
}

// One bot's stale password, on a server several resellers' bots share, must not
// lock the others out.
func TestOneAccountsWrongPasswordsDoNotLockOthersOnTheSameAddress(t *testing.T) {
	router, sudo := newTestRouter(t)
	doRequest(t, router, "POST", "/api/admin", sudo, map[string]interface{}{"username": "other_bot", "password": "right-password", "is_sudo": false})
	for i := 0; i < loginRateLimitMax+1; i++ {
		doFormLogin(t, router, "stale_bot", "wrong")
	}
	if code := doFormLogin(t, router, "stale_bot", "wrong"); code != http.StatusTooManyRequests {
		t.Errorf("the account being guessed at: %d, want 429", code)
	}
	if code := doFormLogin(t, router, "other_bot", "right-password"); code != http.StatusOK {
		t.Errorf("another account on the same address: %d, want 200", code)
	}
}

func TestGuessingAcrossManyAccountsIsLimitedByAddress(t *testing.T) {
	router, _, h := newTestRouterAndHandler(t)
	// Directly, not by logging in: each failed login costs a bcrypt, and with the race
	// detector on that many would outlast the limit's one-minute window.
	for i := 0; i < loginRateLimitAddress; i++ {
		h.recordFailedLogin(t.Context(), "192.0.2.1", fmt.Sprintf("spray_%d", i))
	}
	if code := doFormLogin(t, router, "spray_new", "wrong"); code != http.StatusTooManyRequests {
		t.Errorf("after %d failures across accounts: %d, want 429", loginRateLimitAddress, code)
	}
}
