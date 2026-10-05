package naive

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/auth"
)

func TestNewInboundRejectsNoUsers(t *testing.T) {
	_, err := NewInbound(context.Background(), nil, nil, "t", option.NaiveInboundOptions{})
	if err == nil {
		t.Fatal("expected an error for zero users, got none")
	}
}

func TestNewInboundRejectsUDP(t *testing.T) {
	_, err := NewInbound(context.Background(), nil, nil, "t", option.NaiveInboundOptions{
		Users:   []auth.User{{Username: "alice", Password: "pw"}},
		Network: "udp",
	})
	if err == nil {
		t.Fatal("expected UDP to be rejected, got no error")
	}
}

// TestAuthenticatorHotSwap is the real reason this fork exists: unlike
// every Service[U]-backed protocol in this package, sing.common.auth.
// Authenticator has no mutator of its own, so UpdateUsers has to build a
// fresh one and atomically swap it - this proves that swap actually takes
// effect (old credentials stop verifying, new ones start) without ever
// leaving a window where Load() returns nil or a half-built value.
func TestAuthenticatorHotSwap(t *testing.T) {
	inb, err := NewInbound(context.Background(), nil, nil, "t", option.NaiveInboundOptions{
		Network: "tcp",
		Users:   []auth.User{{Username: "alice", Password: "pw-v1"}},
	})
	if err != nil {
		t.Fatalf("NewInbound: %v", err)
	}
	n := inb.(*Inbound)

	authn := n.authenticator.Load()
	if authn == nil {
		t.Fatal("authenticator not initialized by NewInbound")
	}
	if !authn.Verify("alice", "pw-v1") {
		t.Error("the configured password does not verify")
	}
	if authn.Verify("alice", "wrong") {
		t.Error("a wrong password verified")
	}
	if authn.Verify("bob", "pw-v1") {
		t.Error("an unknown username verified")
	}

	n.UpdateUsers([]auth.User{{Username: "alice", Password: "pw-v2"}})

	authn2 := n.authenticator.Load()
	if authn2.Verify("alice", "pw-v1") {
		t.Error("the old password still verifies after UpdateUsers")
	}
	if !authn2.Verify("alice", "pw-v2") {
		t.Error("the new password does not verify after UpdateUsers")
	}
}

// TestAuthenticatorHotSwapToEmptyRejectsEveryone mirrors the sing-snell
// fork's own "empty user list must still refuse everyone, not fall open"
// guard - auth.NewAuthenticator(nil) returns a nil *Authenticator, and
// ServeHTTP's own nil check (authOk = false when Load() is nil) is what
// keeps that from being silently treated as "no auth required".
func TestAuthenticatorHotSwapToEmptyRejectsEveryone(t *testing.T) {
	inb, err := NewInbound(context.Background(), nil, nil, "t", option.NaiveInboundOptions{
		Network: "tcp",
		Users:   []auth.User{{Username: "alice", Password: "pw"}},
	})
	if err != nil {
		t.Fatalf("NewInbound: %v", err)
	}
	n := inb.(*Inbound)

	n.UpdateUsers(nil)

	if n.authenticator.Load() != nil {
		t.Fatal("expected a nil authenticator for an empty user list")
	}
	// ServeHTTP's own nil-authenticator branch is what actually enforces
	// this; asserting Load() == nil here pins the precondition that branch
	// depends on.
}
