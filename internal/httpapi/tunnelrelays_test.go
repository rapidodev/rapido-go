package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// tunnelRelayList decodes the bare JSON array GET /api/tunnel-relays
// returns - doRequest's apiResponse.Body only handles an object response,
// so a list endpoint's own shape must be read from .Raw directly, the same
// way TestCreateNodeCapacityAcceptsNullAbsentOrAValue reads GET /api/nodes.
func tunnelRelayList(t *testing.T, raw []byte) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode tunnel relay list: %v: %s", err, raw)
	}
	return out
}

func TestTunnelRelaysCRUD(t *testing.T) {
	router, token := newTestRouter(t)

	// Starts empty.
	list := doRequest(t, router, "GET", "/api/tunnel-relays", token, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("GET (empty) = %d %s", list.Code, list.Raw)
	}
	if got := tunnelRelayList(t, list.Raw); len(got) != 0 {
		t.Fatalf("GET (empty) = %v, want none", got)
	}

	create := doRequest(t, router, "POST", "/api/tunnel-relays", token, map[string]interface{}{
		"name": "node1-relay", "host": "5.202.4.97", "port": 20004,
	})
	if create.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", create.Code, create.Raw)
	}
	if create.Body["name"] != "node1-relay" || create.Body["host"] != "5.202.4.97" || create.Body["port"] != float64(20004) {
		t.Errorf("created relay = %v, want name/host/port echoed back", create.Body)
	}
	id, ok := create.Body["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("created relay has no usable id: %v", create.Body)
	}

	// Required fields are enforced.
	for _, bad := range []map[string]interface{}{
		{"host": "1.2.3.4", "port": 1},
		{"name": "x", "port": 1},
		{"name": "x", "host": "1.2.3.4"},
	} {
		resp := doRequest(t, router, "POST", "/api/tunnel-relays", token, bad)
		if resp.Code != http.StatusUnprocessableEntity {
			t.Errorf("POST %v = %d %s, want 422", bad, resp.Code, resp.Raw)
		}
	}

	idPath := "/api/tunnel-relays/" + strconv.Itoa(int(id))
	del := doRequest(t, router, "DELETE", idPath, token, nil)
	if del.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", del.Code, del.Raw)
	}

	after := doRequest(t, router, "GET", "/api/tunnel-relays", token, nil)
	if after.Code != http.StatusOK {
		t.Fatalf("GET (after delete) = %d %s", after.Code, after.Raw)
	}
	if got := tunnelRelayList(t, after.Raw); len(got) != 0 {
		t.Errorf("GET (after delete) = %v, want none left", got)
	}
}

func TestTunnelRelaysRequireSudo(t *testing.T) {
	router, sudoToken := newTestRouter(t)
	doRequest(t, router, "POST", "/api/admin", sudoToken, map[string]interface{}{
		"username": "relay-probe-reseller", "password": "pw12345", "is_sudo": false,
	})
	nonSudoToken := loginAs(t, router, "relay-probe-reseller", "pw12345")

	for _, req := range []struct{ method, path string }{
		{"GET", "/api/tunnel-relays"},
		{"POST", "/api/tunnel-relays"},
		{"DELETE", "/api/tunnel-relays/1"},
	} {
		resp := doRequest(t, router, req.method, req.path, nonSudoToken, map[string]interface{}{"name": "x", "host": "1.2.3.4", "port": 1})
		if resp.Code != http.StatusForbidden {
			t.Errorf("%s %s with a non-sudo token = %d %s, want 403", req.method, req.path, resp.Code, resp.Raw)
		}
	}
}
