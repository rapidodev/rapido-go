package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStaticFilesAreServedButDirectoriesAreNotListed(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"index.html":            "the dashboard",
		"assets/page.js":        "the script",
		"statics/fonts/a.woff2": "the font",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	MountDashboardStatic(r, dir)

	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/dashboard/", http.StatusOK, "the dashboard"},
		{"/dashboard/assets/page.js", http.StatusOK, "the script"},
		{"/statics/fonts/a.woff2", http.StatusOK, "the font"},
		{"/dashboard/assets/", http.StatusNotFound, ""},
		{"/statics/", http.StatusNotFound, ""},
		{"/statics/fonts/", http.StatusNotFound, ""},
		{"/statics/missing.woff2", http.StatusNotFound, ""},
	} {
		out := httptest.NewRecorder()
		r.ServeHTTP(out, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if out.Code != tc.code || !strings.Contains(out.Body.String(), tc.body) {
			t.Errorf("%s: %d %q, want %d with %q", tc.path, out.Code, out.Body.String(), tc.code, tc.body)
		}
		if tc.code == http.StatusNotFound && strings.Contains(out.Body.String(), "href") {
			t.Errorf("%s lists a directory: %q", tc.path, out.Body.String())
		}
	}
}

func TestEveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	router, token := newTestRouter(t)
	for _, tc := range []struct{ path, token string }{
		{"/health", ""},
		{"/api/users", ""},
		{"/api/users", token},
		{"/no/such/path", ""},
	} {
		out := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		router.ServeHTTP(out, req)
		for header, want := range map[string]string{
			"X-Frame-Options":        "DENY",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "same-origin",
		} {
			if got := out.Header().Get(header); got != want {
				t.Errorf("%s (%d): %s = %q, want %q", tc.path, out.Code, header, got, want)
			}
		}
	}

	// Its own policy on the dashboard. The subscription page sets a stricter one.
	out := httptest.NewRecorder()
	router.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	if got := out.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self'") || strings.Contains(got, "script-src 'self' 'unsafe") {
		t.Errorf("dashboard policy = %q", got)
	}
}
