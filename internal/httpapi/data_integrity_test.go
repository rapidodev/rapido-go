package httpapi

import (
	"context"
	"net/http"
	"testing"
)

func deleteInbound(t *testing.T, router http.Handler, token, tag string, confirm bool) apiResponse {
	t.Helper()
	path := "/api/inbounds/" + tag
	if confirm {
		path += "?confirm_drop_credentials=true"
	}
	return doRequest(t, router, "DELETE", path, token, nil)
}

// Deleting a protocol's last inbound deletes those users' credentials, which no
// snapshot can bring back and which their clients are using: it has to be asked
// for, with the number of users on the table.
func TestDroppingAProtocolsLastInboundNeedsAConfirmation(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{
		{"tag": "VLESS-1", "protocol": "vless"}, {"tag": "VMess-only", "protocol": "vmess"},
	})
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "two_protocols", "proxies": map[string]interface{}{"vless": map[string]interface{}{}, "vmess": map[string]interface{}{}},
	})

	refused := deleteInbound(t, router, token, "VMess-only", false)
	if refused.Code != http.StatusConflict {
		t.Fatalf("no confirmation: %d %v, want 409", refused.Code, refused.Body)
	}
	dropped, _ := refused.Body["dropped_credentials"].([]interface{})
	if len(dropped) != 1 {
		t.Fatalf("dropped_credentials = %v", refused.Body["dropped_credentials"])
	}
	entry := dropped[0].(map[string]interface{})
	if entry["protocol"] != "vmess" || entry["users"] != float64(1) {
		t.Errorf("dropped = %v", entry)
	}
	if !userProxyProtocols(t, router, token, "two_protocols")["vmess"] {
		t.Fatal("the refused request deleted the credential anyway")
	}

	if resp := deleteInbound(t, router, token, "VMess-only", true); resp.Code != http.StatusOK {
		t.Fatalf("confirmed: %d %v", resp.Code, resp.Body)
	}
	proxies := userProxyProtocols(t, router, token, "two_protocols")
	if proxies["vmess"] || !proxies["vless"] {
		t.Errorf("proxies after the confirmed delete = %v", proxies)
	}
}

func TestDeletingAnInboundWithNoCredentialsAtStakeNeedsNoConfirmation(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{
		{"tag": "A", "protocol": "vless"}, {"tag": "B", "protocol": "vless"}, {"tag": "C", "protocol": "vmess"},
	})
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "on_vless", "proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	// Another inbound of the protocol remains.
	if resp := deleteInbound(t, router, token, "A", false); resp.Code != http.StatusOK {
		t.Errorf("A: %d %v", resp.Code, resp.Body)
	}
	// The protocol goes, but no user has it.
	if resp := deleteInbound(t, router, token, "C", false); resp.Code != http.StatusOK {
		t.Errorf("C: %d %v", resp.Code, resp.Body)
	}
}

func TestApplyingAConfigWithoutAProtocolNeedsAConfirmation(t *testing.T) {
	router, token := newTestRouter(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{
		{"tag": "Keep", "protocol": "vless"}, {"tag": "Drop", "protocol": "trojan"},
	})
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "apply_two", "proxies": map[string]interface{}{"vless": map[string]interface{}{}, "trojan": map[string]interface{}{}},
	})
	body := map[string]interface{}{"inbounds": []map[string]interface{}{{"tag": "Keep", "protocol": "vless"}}}

	refused := doRequest(t, router, "PUT", "/api/settings/xray-config", token, body)
	if refused.Code != http.StatusConflict {
		t.Fatalf("no confirmation: %d %v, want 409", refused.Code, refused.Body)
	}
	if refused.Body["confirm_required"] != "drop_credentials" {
		t.Errorf("confirm_required = %v", refused.Body["confirm_required"])
	}
	current := doRequest(t, router, "GET", "/api/settings/xray-config", token, nil)
	if inbounds, _ := current.Body["inbounds"].([]interface{}); len(inbounds) != 2 {
		t.Errorf("the refused apply changed the inbounds: %v", current.Body["inbounds"])
	}

	done := doRequest(t, router, "PUT", "/api/settings/xray-config?confirm_drop_credentials=true", token, body)
	if done.Code != http.StatusOK {
		t.Fatalf("confirmed: %d %v", done.Code, done.Body)
	}
	if userProxyProtocols(t, router, token, "apply_two")["trojan"] {
		t.Error("the confirmed apply kept the credential")
	}
}

func exclusionsOf(t *testing.T, h *Handler, username string) []string {
	t.Helper()
	rows, err := h.store.Pool.Query(context.Background(), `
		SELECT e.inbound_tag FROM exclude_inbounds_association e
		JOIN proxies p ON p.id = e.proxy_id JOIN users u ON u.id = p.user_id
		WHERE u.username = $1 ORDER BY 1`, username)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			t.Fatal(err)
		}
		out = append(out, tag)
	}
	return out
}

func latestSnapshotID(t *testing.T, h *Handler) string {
	t.Helper()
	var id string
	if err := h.store.Pool.QueryRow(context.Background(), `SELECT id::text FROM core_config_snapshots ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Restoring the fleet's config must not touch who may use what: a user kept off
// an inbound after the snapshot was taken stays off it.
func TestFleetRestoreKeepsUserAssignmentsMadeSinceTheSnapshot(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{
		{"tag": "Stays", "protocol": "vless"}, {"tag": "Returns", "protocol": "vless"},
	})
	allowed := func(tags ...string) map[string]interface{} {
		return map[string]interface{}{"vless": tags}
	}
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "before_snapshot", "proxies": map[string]interface{}{"vless": map[string]interface{}{}}, "inbounds": allowed("Stays"),
	})
	if got := exclusionsOf(t, h, "before_snapshot"); len(got) != 1 || got[0] != "Returns" {
		t.Fatalf("exclusions before = %v", got)
	}

	// This write takes the snapshot of the state above.
	if resp := doRequest(t, router, "PUT", "/api/settings/xray-config", token, map[string]interface{}{
		"inbounds": []map[string]interface{}{{"tag": "Stays", "protocol": "vless"}},
	}); resp.Code != http.StatusOK {
		t.Fatalf("apply: %d %v", resp.Code, resp.Body)
	}
	id := latestSnapshotID(t, h)
	if got := exclusionsOf(t, h, "before_snapshot"); len(got) != 0 {
		t.Fatalf("deleting the inbound should have taken its exclusion: %v", got)
	}

	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "Later", "protocol": "vless"}})
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "after_snapshot", "proxies": map[string]interface{}{"vless": map[string]interface{}{}}, "inbounds": allowed("Later"),
	})
	if got := exclusionsOf(t, h, "after_snapshot"); len(got) != 1 || got[0] != "Stays" {
		t.Fatalf("exclusions after = %v", got)
	}

	if resp := doRequest(t, router, "POST", "/api/core/config/backups/"+id+"/restore", token, map[string]interface{}{"confirm": true}); resp.Code != http.StatusOK {
		t.Fatalf("restore: %d %v", resp.Code, resp.Body)
	}

	if got := exclusionsOf(t, h, "after_snapshot"); len(got) != 1 || got[0] != "Stays" {
		t.Errorf("a user made since the snapshot lost their restriction: %v", got)
	}
	if got := exclusionsOf(t, h, "before_snapshot"); len(got) != 1 || got[0] != "Returns" {
		t.Errorf("the inbound came back without its exclusion: %v", got)
	}
}

// A panel can hold credentials of a protocol that lost its last inbound long ago.
// That must not stand in the way of every later apply, which removes nothing.
func TestAnApplyThatRemovesNothingNeverAsksAboutOldCredentials(t *testing.T) {
	router, token, h := newTestRouterAndHandler(t)
	doRequest(t, router, "POST", "/api/inbounds/sync", token, []map[string]string{{"tag": "Keep", "protocol": "vless"}})
	doRequest(t, router, "POST", "/api/user", token, map[string]interface{}{
		"username": "old_vmess", "proxies": map[string]interface{}{"vless": map[string]interface{}{}},
	})
	if _, err := h.store.Pool.Exec(context.Background(), `
		INSERT INTO proxies (user_id, type, settings)
		SELECT id, 'vmess', '{"id":"8f8a4c1e-1e2a-4b8a-9b1a-000000000099"}' FROM users WHERE username = 'old_vmess'`); err != nil {
		t.Fatal(err)
	}
	resp := doRequest(t, router, "PUT", "/api/settings/xray-config", token, map[string]interface{}{
		"inbounds": []map[string]interface{}{{"tag": "Keep", "protocol": "vless"}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	if !userProxyProtocols(t, router, token, "old_vmess")["vmess"] {
		t.Error("an apply that removed nothing deleted a credential")
	}
}
