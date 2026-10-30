package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/legendary1205/rapido-go/internal/auth"
)

func TestStaticRequestsDoNotReadSigningIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	loads := 0
	h := &Handler{issuer: auth.NewTokenIssuer([]byte("test-only-old-signing-key"), 0)}
	h.WithSigningKeyLoader(func(context.Context) (SigningKeys, error) {
		loads++
		return SigningKeys{Subscription: []byte("test-only-new-subscription-key"), Admin: []byte("test-only-new-admin-key")}, nil
	})
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
	h.WithSigningKeyLoader(func(context.Context) (SigningKeys, error) {
		return SigningKeys{}, errors.New("test-only database failure")
	})
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

// Holding the key that signs subscription links, which a legacy panel's dump
// hands over on import, must not be enough to sign in as an admin.
func TestAdminTokensAndSubscriptionLinksUseDifferentKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	issuer := auth.NewTokenIssuer([]byte("test-only-boot-key"), 0)
	h := &Handler{issuer: issuer}
	subscriptionKey, adminKey := []byte("test-only-subscription-key"), []byte("test-only-admin-key")
	h.WithSigningKeyLoader(func(context.Context) (SigningKeys, error) {
		return SigningKeys{Subscription: subscriptionKey, Admin: adminKey, AdminSince: time.Now()}, nil
	})
	r := gin.New()
	r.Use(h.refreshSigningIdentity())
	r.GET("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/admin", nil))

	if string(h.signingSecret()) != string(subscriptionKey) {
		t.Errorf("subscription links are signed with %q", h.signingSecret())
	}
	if string(issuer.Secret()) != string(adminKey) {
		t.Errorf("admin tokens are signed with %q", issuer.Secret())
	}
	time.Sleep(2 * time.Millisecond)
	token, err := auth.NewTokenIssuer(subscriptionKey, 0).Issue("anyone", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(token); err == nil {
		t.Error("a token signed with the subscription key was accepted")
	}
}
