package httpapi

import (
	"log/slog"
	"testing"
)

// TestRouterRegistersEveryRouteWithoutConflict builds the full route table; a
// wildcard clash (for example a static /tunnels/timeline next to
// /tunnels/:id/...) panics at registration, which this catches without a
// database.
func TestRouterRegistersEveryRouteWithoutConflict(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("route registration panicked: %v", r)
		}
	}()
	h := &Handler{store: &Store{}, logger: slog.Default()}
	engine := NewRouter(h, slog.Default(), nil)
	want := map[string]bool{
		"GET /api/tunnels/timeline": false, "GET /api/tunnels/:id/events": false, "GET /api/tunnels/:id/samples": false,
		"POST /api/tunnels/:id/diagnose": false, "GET /api/tunnels/:id/diagnose": false, "GET /api/tunnels/:id/diagnose/report": false,
		"GET /api/admins/overview":                  false, "POST /api/users/bulk": false, "GET /api/users/export": false,
		"POST /api/node/:id/restart": false,
	}
	for _, r := range engine.Routes() {
		key := r.Method + " " + r.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("route %s is not registered", k)
		}
	}
}
