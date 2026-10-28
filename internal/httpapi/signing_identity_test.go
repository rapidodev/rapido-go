package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/legendary1205/rapido-go/internal/auth"
)

func TestStaticRequestsDoNotReadSigningIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	loads := 0
	h := &Handler{issuer: auth.NewTokenIssuer([]byte("test-only-old-signing-key"), 0)}
	h.WithSigningKeyLoader(func(context.Context) ([]byte, error) { loads++; return []byte("test-only-new-signing-key"), nil })
	r := gin.New()
	r.Use(h.refreshSigningIdentity())
	r.GET("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, path := range []string{"/health", "/dashboard/", "/dashboard/assets/page.js", "/statics/font.woff2"} {
		out := httptest.NewRecorder()
		r.ServeHTTP(out, httptest.NewRequest("GET", path, nil))
		if out.Code != http.StatusNoContent {
			t.Fatal("static request failed")
		}
	}
	if loads != 0 {
		t.Fatal("static requests performed signing-key database reads")
	}
	for _, path := range []string{"/api/admin", "/sub/test-only-placeholder"} {
		out := httptest.NewRecorder()
		r.ServeHTTP(out, httptest.NewRequest("GET", path, nil))
		if out.Code != http.StatusNoContent {
			t.Fatal("protected request failed")
		}
	}
	if loads != 2 {
		t.Fatal("protected requests did not refresh the signing identity")
	}
}

func TestSigningIdentityFailureBlocksProtectedRequestsOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{issuer: auth.NewTokenIssuer([]byte("test-only-signing-key"), 0)}
	h.WithSigningKeyLoader(func(context.Context) ([]byte, error) { return nil, errors.New("test-only database failure") })
	r := gin.New()
	r.Use(h.refreshSigningIdentity())
	r.GET("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for path, want := range map[string]int{"/dashboard/": 204, "/api/admin": 503, "/sub/test-only-placeholder": 503} {
		out := httptest.NewRecorder()
		r.ServeHTTP(out, httptest.NewRequest("GET", path, nil))
		if out.Code != want {
			t.Errorf("%s returned %d; want %d", path, out.Code, want)
		}
	}
}
