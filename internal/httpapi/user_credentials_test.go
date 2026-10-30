package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

func vlessID(t *testing.T, body map[string]interface{}) string {
	t.Helper()
	proxies, _ := body["proxies"].(map[string]interface{})
	vless, _ := proxies["vless"].(map[string]interface{})
	id, _ := vless["id"].(string)
	if id == "" {
		t.Fatalf("no vless id in %v", body)
	}
	return id
}

func setupVLESSUser(t *testing.T, router http.Handler, token, username string) string {
	t.Helper()
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "VLESS TCP", "protocol": "vless"}})
	resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": username, "proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create %s: %d %v", username, resp.Code, resp.Body)
	}
	return vlessID(t, resp.Body)
}

// Every save of the dashboard's edit form sent proxies: {vless: {}}, which
// generated a new credential each time and cut off the user's clients.
func TestEditingAUserKeepsItsCredential(t *testing.T) {
	router, token := newTestRouter(t)
	id := setupVLESSUser(t, router, token, "keep_cred")

	bodies := map[string]interface{}{
		"empty object": map[string]interface{}{"vless": map[string]interface{}{}},
		"empty array":  map[string]interface{}{"vless": []interface{}{}},
		"empty id":     map[string]interface{}{"vless": map[string]interface{}{"id": ""}},
	}
	for name, proxies := range bodies {
		resp := doRequest(t, router, "PUT", "/api/user/keep_cred", token, map[string]interface{}{"note": name, "proxies": proxies})
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: %d %v", name, resp.Code, resp.Body)
		}
		if got := vlessID(t, resp.Body); got != id {
			t.Errorf("%s: the credential changed from %s to %s", name, id, got)
		}
	}
}

func TestEditingAUserKeepsAStoredFlowThatWasNotNamed(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	id := setupVLESSUser(t, router, token, "keep_flow")
	// A migrated panel's plain VLESS user: no flow at all.
	if _, err := h.store.Pool.Exec(context.Background(), `UPDATE proxies SET settings = jsonb_set(settings, '{flow}', '""') WHERE settings->>'id' = $1`, id); err != nil {
		t.Fatal(err)
	}
	resp := doRequest(t, router, "PUT", "/api/user/keep_flow", token, map[string]interface{}{
		"proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	vless := resp.Body["proxies"].(map[string]interface{})["vless"].(map[string]interface{})
	if vless["flow"] != "" {
		t.Errorf("flow = %v, want the stored empty flow left alone", vless["flow"])
	}
}

func TestEditingAUserCanStillChangeItsCredentialOnPurpose(t *testing.T) {
	router, token := newTestRouter(t)
	setupVLESSUser(t, router, token, "change_cred")
	want := "11111111-2222-4333-8444-555555555555"
	resp := doRequest(t, router, "PUT", "/api/user/change_cred", token, map[string]interface{}{
		"proxies": map[string]interface{}{"vless": map[string]interface{}{"id": want}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	if got := vlessID(t, resp.Body); got != want {
		t.Errorf("id = %s, want %s", got, want)
	}
}

func TestRevokeSubscriptionStillRotatesTheCredential(t *testing.T) {
	router, token := newTestRouter(t)
	id := setupVLESSUser(t, router, token, "revoke_cred")
	resp := doRequest(t, router, "POST", "/api/user/revoke_cred/revoke_sub", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	if vlessID(t, resp.Body) == id {
		t.Error("revoke_sub kept the credential")
	}
}

func TestACredentialNoCoreCanServeIsRefused(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{
		{"tag": "VLESS TCP", "protocol": "vless"}, {"tag": "SS", "protocol": "shadowsocks"}, {"tag": "TUIC", "protocol": "tuic"},
	})
	cases := []struct {
		name    string
		proxies map[string]interface{}
	}{
		{"id that is no uuid", map[string]interface{}{"vless": map[string]interface{}{"id": "not-a-uuid"}}},
		{"unknown flow", map[string]interface{}{"vless": map[string]interface{}{"flow": "xtls-rprx-direct"}}},
		{"tuic id that is no uuid", map[string]interface{}{"tuic": map[string]interface{}{"id": "nope"}}},
		{"2022 cipher", map[string]interface{}{"shadowsocks": map[string]interface{}{"method": "2022-blake3-aes-128-gcm"}}},
		{"oversized password", map[string]interface{}{"shadowsocks": map[string]interface{}{"password": strings.Repeat("x", 257)}}},
		{"control character", map[string]interface{}{"shadowsocks": map[string]interface{}{"password": "a\nb"}}},
	}
	for i, tc := range cases {
		resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{"username": fmt.Sprintf("bad_cred_%d", i), "proxies": tc.proxies})
		if resp.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %v, want 422", tc.name, resp.Code, resp.Body)
		}
	}
}

func TestAnUppercaseUUIDIsStoredInTheOneFormThePanelCompares(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "VLESS TCP", "protocol": "vless"}})
	resp := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "upper_cred", "proxies": map[string]interface{}{"vless": map[string]interface{}{"id": "AAAAAAAA-0000-4000-8000-00000000000A"}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	if got := vlessID(t, resp.Body); got != "aaaaaaaa-0000-4000-8000-00000000000a" {
		t.Errorf("id = %s", got)
	}
}

func TestACredentialAnotherUserHoldsIsRefused(t *testing.T) {
	router, token := newTestRouter(t)
	first := setupVLESSUser(t, router, token, "holder")
	setupVLESSUser(t, router, token, "other")
	vless := func(id string) map[string]interface{} {
		return map[string]interface{}{"vless": map[string]interface{}{"id": id}}
	}

	create := doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{"username": "thief", "proxies": vless(first)})
	if create.Code != http.StatusConflict {
		t.Errorf("create with another user's id: %d %v, want 409", create.Code, create.Body)
	}
	if strings.Contains(string(create.Raw), "holder") {
		t.Errorf("the refusal names who holds it: %s", create.Raw)
	}
	update := doRequest(t, router, "PUT", "/api/user/other", token, map[string]interface{}{"proxies": vless(first)})
	if update.Code != http.StatusConflict {
		t.Errorf("update to another user's id: %d %v, want 409", update.Code, update.Body)
	}
	upper := doRequest(t, router, "PUT", "/api/user/other", token, map[string]interface{}{"proxies": vless(strings.ToUpper(first))})
	if upper.Code != http.StatusConflict {
		t.Errorf("same id in capitals: %d %v, want 409", upper.Code, upper.Body)
	}

	// Sending back what the user already has is no conflict with anybody.
	own := doRequest(t, router, "PUT", "/api/user/holder", token, map[string]interface{}{"proxies": vless(first)})
	if own.Code != http.StatusOK {
		t.Errorf("own id: %d %v, want 200", own.Code, own.Body)
	}

	// A disabled holder can be enabled again, so it still holds it.
	if resp := doRequest(t, router, "PUT", "/api/user/holder", token, map[string]interface{}{"status": "disabled"}); resp.Code != http.StatusOK {
		t.Fatalf("disable: %d %v", resp.Code, resp.Body)
	}
	if resp := doRequest(t, router, "PUT", "/api/user/other", token, map[string]interface{}{"proxies": vless(first)}); resp.Code != http.StatusConflict {
		t.Errorf("a disabled user's id: %d %v, want 409", resp.Code, resp.Body)
	}
}

// A user already stuck with a shared credential can still be edited.
func TestAUserWithAnExistingDuplicateCanStillBeEdited(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	first := setupVLESSUser(t, router, token, "dup_a")
	second := setupVLESSUser(t, router, token, "dup_b")
	if _, err := h.store.Pool.Exec(context.Background(), `UPDATE proxies SET settings = jsonb_set(settings, '{id}', to_jsonb($1::text)) WHERE settings->>'id' = $2`, first, second); err != nil {
		t.Fatal(err)
	}
	resp := doRequest(t, router, "PUT", "/api/user/dup_b", token, map[string]interface{}{
		"note": "still editable", "proxies": map[string]interface{}{"vless": map[string]interface{}{"flow": "xtls-rprx-vision"}},
	})
	if resp.Code != http.StatusOK {
		t.Errorf("%d %v", resp.Code, resp.Body)
	}
}

// One bad row must not reach a node: the agent refuses a whole list over it, and
// from then on no add, revoke or limit reaches that node.
func TestNodeConfigLeavesOutRowsNoCoreCanServe(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	_, secret := createTestNode(t, router, token, "unservable-node")
	doRequest(t, router, "PUT", "/api/hosts", token, map[string]interface{}{})
	keep := setupVLESSUser(t, router, token, "served_first")
	dup := setupVLESSUser(t, router, token, "served_dup")
	bad := setupVLESSUser(t, router, token, "served_bad")
	setupVLESSUser(t, router, token, "served_last")
	doRequest(t, router, "PUT", "/api/hosts", token, map[string]interface{}{
		"VLESS TCP": []map[string]interface{}{{"remark": "n", "address": "1.2.3.4", "port": 8443}},
	})
	ctx := context.Background()
	if _, err := h.store.Pool.Exec(ctx, `UPDATE proxies SET settings = jsonb_set(settings, '{id}', to_jsonb($1::text)) WHERE settings->>'id' = $2`, strings.ToUpper(keep), dup); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Pool.Exec(ctx, `UPDATE proxies SET settings = jsonb_set(settings, '{id}', '"not-a-uuid"') WHERE settings->>'id' = $1`, bad); err != nil {
		t.Fatal(err)
	}

	resp := doRequest(t, router, "GET", "/api/internal/node-config", secret, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	raw := string(resp.Raw)
	for _, want := range []string{"served_first", "served_last"} {
		if !strings.Contains(raw, want) {
			t.Errorf("%s is missing from the node's list", want)
		}
	}
	for _, unwanted := range []string{"served_dup", "served_bad"} {
		if strings.Contains(raw, unwanted) {
			t.Errorf("%s reached the node's list", unwanted)
		}
	}
}

// A 401 tells a node it was removed from this panel, so it must mean exactly
// that, and never "the database hiccupped".
func TestNodeSecretLookupFailureIsNotARefusal(t *testing.T) {
	router, token := newTestRouter(t)
	_, secret := createTestNode(t, router, token, "lookup-node")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/api/internal/node-config", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a failed lookup answered %d, want 503", rec.Code)
	}
}

func TestNextPlanSurvivesAnEditAndNullRemovesIt(t *testing.T) {
	router, token := newTestRouter(t)
	setupVLESSUser(t, router, token, "plan_keep")
	set := doRequest(t, router, "PUT", "/api/user/plan_keep", token, map[string]interface{}{
		"next_plan": map[string]interface{}{"data_limit": 5000, "expire": 30},
	})
	if set.Body["next_plan"] == nil {
		t.Fatalf("next_plan was not stored: %d %v", set.Code, set.Body)
	}
	kept := doRequest(t, router, "PUT", "/api/user/plan_keep", token, map[string]interface{}{"note": "unrelated"})
	if kept.Body["next_plan"] == nil {
		t.Errorf("an unrelated edit discarded the plan: %d %v", kept.Code, kept.Body)
	}
	removed := doRequest(t, router, "PUT", "/api/user/plan_keep", token, map[string]interface{}{"next_plan": nil})
	if removed.Body["next_plan"] != nil {
		t.Errorf("an explicit null kept the plan: %d %v", removed.Code, removed.Body)
	}
}

func TestDurationsThatWouldBreakTheReviewJobAreRefused(t *testing.T) {
	router, token := newTestRouter(t)
	setupVLESSUser(t, router, token, "durations")
	timestamp := time.Now().Unix() + 86400*30
	cases := []map[string]interface{}{
		{"next_plan": map[string]interface{}{"data_limit": 1, "expire": timestamp * 1000}},
		{"next_plan": map[string]interface{}{"data_limit": 1, "expire": -1}},
		{"next_plan": map[string]interface{}{"data_limit": -1}},
		{"status": "on_hold", "expire": 0, "on_hold_expire_duration": int64(1) << 40},
	}
	for _, body := range cases {
		if resp := doRequest(t, router, "PUT", "/api/user/durations", token, body); resp.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d %v, want 422", body, resp.Code, resp.Body)
		}
	}
}

// Auto-delete counts from last_status_change. A user who just expired by an edit
// must not be deleted on the strength of a clock from months ago.
func TestAStatusSetByAnEditRestartsTheAutoDeleteClock(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	setupVLESSUser(t, router, token, "clock")
	ctx := context.Background()
	old := time.Now().Add(-1000 * time.Hour)
	if _, err := h.store.Pool.Exec(ctx, `UPDATE users SET last_status_change = $1 WHERE username = 'clock'`, old); err != nil {
		t.Fatal(err)
	}
	stamp := func() time.Time {
		var at time.Time
		if err := h.store.Pool.QueryRow(ctx, `SELECT last_status_change FROM users WHERE username = 'clock'`).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}

	doRequest(t, router, "PUT", "/api/user/clock", token, map[string]interface{}{"note": "same status"})
	if got := stamp(); !got.Before(time.Now().Add(-999 * time.Hour)) {
		t.Errorf("an edit that left the status alone moved the clock to %s", got)
	}
	doRequest(t, router, "PUT", "/api/user/clock", token, map[string]interface{}{"expire": time.Now().Add(-time.Hour).Unix()})
	if got := stamp(); got.Before(time.Now().Add(-time.Minute)) {
		t.Errorf("the user expired by an edit and the clock stayed at %s", got)
	}
}

func TestNextPlanFormula(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	ctx := context.Background()
	setupVLESSUser(t, router, token, "plan_math")
	var userID int32
	if err := h.store.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'plan_math'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	future := int32(time.Now().Add(240 * time.Hour).Unix())
	cases := []struct {
		name                string
		limit, used         any
		expire              any
		planLimit, planExp  int64
		wantLimit           any
		wantExpireFromNow   bool
		wantExpireUnchanged bool
	}{
		{name: "remainder carries over", limit: int64(1000), used: int64(400), planLimit: 100, planExp: 60, wantLimit: int64(700), wantExpireFromNow: true},
		{name: "past the limit starts with no debt", limit: int64(1000), used: int64(5000), planLimit: 100, planExp: 60, wantLimit: int64(100), wantExpireFromNow: true},
		{name: "unlimited user gets the plan's limit", limit: nil, used: int64(5000), planLimit: 100, planExp: 60, wantLimit: int64(100), wantExpireFromNow: true},
		{name: "no limit in the plan keeps the limit", limit: int64(1000), used: int64(400), planLimit: 0, planExp: 60, wantLimit: int64(1000), wantExpireFromNow: true},
		{name: "no expiry in the plan keeps the expiry", limit: int64(1000), used: int64(400), expire: future, planLimit: 100, planExp: 0, wantLimit: int64(700), wantExpireUnchanged: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.store.Pool.Exec(ctx, `UPDATE users SET data_limit = $2, used_traffic = $3, expire = $4 WHERE id = $1`, userID, tc.limit, tc.used, tc.expire); err != nil {
				t.Fatal(err)
			}
			if _, err := h.store.Queries.UpsertNextPlan(ctx, generated.UpsertNextPlanParams{UserID: userID, DataLimit: tc.planLimit, Expire: pgInt4FromInt(int(tc.planExp))}); err != nil {
				t.Fatal(err)
			}
			updated, err := h.store.Queries.ResetUserByNextPlan(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			if !updated.DataLimit.Valid || updated.DataLimit.Int64 != tc.wantLimit.(int64) {
				t.Errorf("data_limit = %+v, want %v", updated.DataLimit, tc.wantLimit)
			}
			if updated.UsedTraffic != 0 {
				t.Errorf("used_traffic = %d", updated.UsedTraffic)
			}
			if tc.wantExpireFromNow && (!updated.Expire.Valid || int64(updated.Expire.Int32) < time.Now().Unix()) {
				t.Errorf("expire = %+v, want now + the plan's", updated.Expire)
			}
			if tc.wantExpireUnchanged && updated.Expire.Int32 != future {
				t.Errorf("expire = %+v, want it left at %d", updated.Expire, future)
			}
		})
	}
}

func TestNodeReportKeepsWhatTheAgentRunsAndWhyItCannot(t *testing.T) {
	router, token := newTestRouter(t)
	id, secret := createTestNode(t, router, token, "agent-state-node")
	node := func() map[string]interface{} {
		resp := doRequest(t, router, "GET", fmt.Sprintf("/api/node/%d", id), token, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("%d %v", resp.Code, resp.Body)
		}
		return resp.Body
	}
	report := func(fields map[string]interface{}) {
		body := nodeReportPayload(nil)
		for k, v := range fields {
			body[k] = v
		}
		if resp := doRequest(t, router, "POST", "/api/internal/node-report", secret, body); resp.Code != http.StatusOK {
			t.Fatalf("%d %v", resp.Code, resp.Body)
		}
	}

	report(map[string]interface{}{"agent_version": "1.2.3", "config_error": "port 443 is taken"})
	got := node()
	if got["xray_version"] != "1.2.3" || got["message"] != "port 443 is taken" {
		t.Errorf("node = %v", got)
	}

	report(map[string]interface{}{"agent_version": "1.2.3"})
	if got := node(); got["message"] != nil {
		t.Errorf("message = %v after the problem went away", got["message"])
	}

	// An agent older than these fields says nothing about them.
	report(map[string]interface{}{"agent_version": "1.2.3", "config_error": "again"})
	report(nil)
	if got := node(); got["message"] != "again" {
		t.Errorf("message = %v after a report from an older agent", got["message"])
	}
}
