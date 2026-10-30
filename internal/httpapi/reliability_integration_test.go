package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapUsernameIsReserved(t *testing.T) {
	r, token := newTestRouter(t)
	response := doRequest(t, r, "POST", "/api/admin", token, map[string]any{"username": testSudoUsername, "password": "shadow-password", "is_sudo": true})
	if response.Code != http.StatusConflict {
		t.Fatalf("reserved username: %d", response.Code)
	}
}
func TestNodeUsageRetriesCommitExactlyOnce(t *testing.T) {
	r, token, h := newTestRouterAndHandler(t)
	_, secret := createTestNode(t, r, token, "receipt-node")
	created := doRequest(t, r, "POST", "/api/user", token, map[string]any{"username": "receipt_user", "proxies": map[string]any{"vless": map[string]any{}}})
	if created.Code != 200 {
		t.Fatal(created.Code)
	}
	body := `{"report_id":"e20d902a-9b54-436a-aaef-c967ad17eb81","users":[{"username":"receipt_user","uplink":10,"downlink":20}]}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/api/internal/node-report", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		if out.Code != 200 {
			t.Fatalf("report %d: %d %s", i, out.Code, out.Body)
		}
	}
	user, err := h.store.Queries.GetUserByUsername(context.Background(), "receipt_user")
	if err != nil {
		t.Fatal(err)
	}
	if user.UsedTraffic != 30 {
		t.Fatalf("billed %d, want 30", user.UsedTraffic)
	}
}
func TestNodeCompactConfigEnforcesExcludedInbound(t *testing.T) {
	r, token := newTestRouter(t)
	_, secret := createTestNode(t, r, token, "restricted-node")
	doRequest(t, r, "POST", "/api/inbounds/sync", token, []map[string]any{{"tag": "allowed", "protocol": "vless"}, {"tag": "denied", "protocol": "vless"}})
	doRequest(t, r, "PUT", "/api/hosts", token, map[string]any{"allowed": []map[string]any{{"remark": "allowed", "address": "127.0.0.1", "port": 11001}}, "denied": []map[string]any{{"remark": "denied", "address": "127.0.0.1", "port": 11002}}})
	created := doRequest(t, r, "POST", "/api/user", token, map[string]any{"username": "restricted_user", "proxies": map[string]any{"vless": map[string]any{}}, "inbounds": map[string]any{"vless": []string{"allowed"}}})
	if created.Code != 200 {
		t.Fatalf("user: %d %s", created.Code, created.Raw)
	}
	req := httptest.NewRequest("GET", "/api/internal/node-config?schema=2", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	out := httptest.NewRecorder()
	r.ServeHTTP(out, req)
	var wire struct {
		Inbounds []struct {
			Tag string `json:"tag"`
			Ref string `json:"users_ref"`
		}
		Sets map[string][]nodeConfigUserSpec `json:"user_sets"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	for _, in := range wire.Inbounds {
		for _, u := range wire.Sets[in.Ref] {
			if in.Tag == "denied" && u.Name == "restricted_user" {
				t.Fatal("excluded user leaked to node")
			}
		}
	}
	if len(wire.Sets) == 0 {
		t.Fatal("compact response has no shared credential sets")
	}
}
func TestUserCreateRollsBackWhenProxyInsertFails(t *testing.T) {
	r, token, h := newTestRouterAndHandler(t)
	ctx := context.Background()
	_, err := h.store.Pool.Exec(ctx, `CREATE OR REPLACE FUNCTION reject_regression_proxy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected proxy failure'; END $$;CREATE TRIGGER reject_regression_proxy BEFORE INSERT ON proxies FOR EACH ROW EXECUTE FUNCTION reject_regression_proxy();`)
	if err != nil {
		t.Fatal(err)
	}
	defer h.store.Pool.Exec(ctx, "DROP TRIGGER reject_regression_proxy ON proxies;DROP FUNCTION reject_regression_proxy()")
	response := doRequest(t, r, "POST", "/api/user", token, map[string]any{"username": "rollback_user", "proxies": map[string]any{"vless": map[string]any{}}})
	if response.Code < 400 {
		t.Fatal("injected failure returned success")
	}
	if _, err := h.store.Queries.GetUserByUsername(ctx, "rollback_user"); err == nil {
		t.Fatal("partially created user survived rollback")
	}
}
func TestUntrustedForwardingCannotChangeClientIP(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	// Test the concrete Gin address resolver without any database.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.GET("/", func(c *gin.Context) { c.String(200, clientIP(c)) })
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.8:1234"
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	out := httptest.NewRecorder()
	r.ServeHTTP(out, req)
	if out.Body.String() != "203.0.113.8" {
		t.Fatal("untrusted IP header was accepted")
	}
}

func TestCoreSnapshotCanBePreviewedAndRestored(t *testing.T) {
	r, token := newTestRouter(t)
	updated := doRequest(t, r, "PUT", "/api/settings/core-config", token, map[string]any{"log_level": "error", "outbounds": []any{}, "routing_rules": []any{}, "dns_servers": []any{}})
	if updated.Code != 200 {
		t.Fatalf("save: %d", updated.Code)
	}
	req := httptest.NewRequest("GET", "/api/core/config/backups", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	r.ServeHTTP(out, req)
	var snapshots []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &snapshots); err != nil || len(snapshots) == 0 {
		t.Fatal("snapshot was not saved")
	}
	id := fmt.Sprint(snapshots[0].ID)
	preview := doRequest(t, r, "GET", "/api/core/config/backups/"+id, token, nil)
	if preview.Code != 200 {
		t.Fatal("preview failed")
	}
	if _, readable := preview.Body["config"].(map[string]any); !readable {
		t.Fatalf("preview config is not readable JSON: %T", preview.Body["config"])
	}
	refused := doRequest(t, r, "POST", "/api/core/config/backups/"+id+"/restore", token, map[string]any{})
	if refused.Code != 400 {
		t.Fatal("restore lacked confirmation")
	}
	restored := doRequest(t, r, "POST", "/api/core/config/backups/"+id+"/restore", token, map[string]any{"confirm": true})
	if restored.Code != 200 || restored.Body["log_level"] != "warn" {
		t.Fatalf("restore failed: %d", restored.Code)
	}
}
