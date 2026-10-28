package httpapi

import (
	"context"
	"encoding/json"

	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/legendary1205/rapido-go/internal/auth"
)

type auditResolver struct{ calls int }

func (r *auditResolver) ResolveAdmin(ctx context.Context, username string) (int32, bool, bool, *time.Time, bool, error) {
	r.calls++
	return 99, true, false, nil, true, nil
}
func TestDatabaseTokenCannotAssumeBootstrapIdentity(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	issuer := auth.NewTokenIssuer([]byte("audit-test-secret"), time.Hour)
	token, err := issuer.Issue("bootstrap", true)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &auditResolver{}
	router := gin.New()
	router.GET("/audit", auth.RequireSudo(issuer, resolver, "bootstrap"), func(c *gin.Context) { c.JSON(200, gin.H{"owner": auth.CurrentIdentity(c).IsOwner}) })
	req := httptest.NewRequest(http.MethodGet, "/audit", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	var result map[string]bool
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["owner"] || resolver.calls != 1 {
		t.Fatalf("unexpected behavior: owner=%v DB_calls=%d", result["owner"], resolver.calls)
	}
}
func TestOutboundDependencyCycleIsRejected(t *testing.T) {
	dto := coreConfigDTO{LogLevel: "warn", Outbounds: []outboundDTO{{Tag: "loop", Type: "selector", Outbounds: []string{"loop"}}}}
	if result := validateCoreConfig(dto); result == "" {
		t.Fatalf("cycle was incorrectly accepted: %s", result)
	}
}
