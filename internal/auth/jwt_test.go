package auth

import (
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	issuer := NewTokenIssuer([]byte("test-secret"), time.Hour)

	token, err := issuer.Issue("alice", false)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Username != "alice" {
		t.Errorf("Username = %q, want %q", claims.Username, "alice")
	}
	if claims.IsSudo() {
		t.Error("IsSudo() = true, want false")
	}
}

func TestIssueSudoAccessClaim(t *testing.T) {
	issuer := NewTokenIssuer([]byte("test-secret"), time.Hour)

	token, err := issuer.Issue("root", true)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !claims.IsSudo() {
		t.Error("IsSudo() = false, want true")
	}
	if claims.Access != AccessSudo {
		t.Errorf("Access = %q, want %q", claims.Access, AccessSudo)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	issuer := NewTokenIssuer([]byte("secret-a"), time.Hour)
	other := NewTokenIssuer([]byte("secret-b"), time.Hour)

	token, err := issuer.Issue("alice", false)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := other.Verify(token); err == nil {
		t.Error("Verify with wrong secret succeeded, want error")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	issuer := NewTokenIssuer([]byte("test-secret"), time.Millisecond)

	token, err := issuer.Issue("alice", false)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := issuer.Verify(token); err == nil {
		t.Error("Verify of expired token succeeded, want error")
	}
}

func TestIssueNoExpiryWhenTTLNonPositive(t *testing.T) {
	// Mirrors JWT_ACCESS_TOKEN_EXPIRE_MINUTES <= 0 in the current Python
	// implementation: the token carries no "exp" claim at all and never
	// expires on its own.
	issuer := NewTokenIssuer([]byte("test-secret"), 0)

	token, err := issuer.Issue("alice", false)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil", claims.ExpiresAt)
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	issuer := NewTokenIssuer([]byte("test-secret"), time.Hour)
	if _, err := issuer.Verify("not-a-jwt"); err == nil {
		t.Error("Verify of garbage token succeeded, want error")
	}
}

func TestTokensFromBeforeAKeyChangeWorkUntilTheyWouldHaveExpired(t *testing.T) {
	oldKey, newKey := []byte("test-only-old-key"), []byte("test-only-new-key")
	ttl := time.Hour
	old := NewTokenIssuer(oldKey, ttl)
	issued, err := old.Issue("alice", false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)

	issuer := NewTokenIssuer(newKey, ttl)
	if _, err := issuer.Verify(issued); err == nil {
		t.Fatal("a token signed with another key passed with no retired key set")
	}

	issuer.AcceptRetiredKey(oldKey, time.Now())
	claims, err := issuer.Verify(issued)
	if err != nil || claims.Username != "alice" {
		t.Errorf("a token from before the change: %v, %v", claims, err)
	}

	// Signed with the retired key after it was retired: that's a forgery.
	time.Sleep(2 * time.Millisecond)
	forged, err := old.Issue("mallory", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(forged); err == nil {
		t.Error("a token issued after the change, with the retired key, was accepted")
	}

	// A token lifetime later the old key is simply no longer accepted.
	issuer.AcceptRetiredKey(oldKey, time.Now().Add(-ttl-time.Minute))
	if _, err := issuer.Verify(issued); err == nil {
		t.Error("the retired key still works after a whole token lifetime")
	}

	fresh, err := issuer.Issue("bob", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(fresh); err != nil {
		t.Errorf("a token from the current key: %v", err)
	}
}
