package telegram

import (
	"strings"
	"testing"
	"time"
)

// TestInfraAlertMessageFormatsExactDowntime is the literal real-world
// example this feature was built for: a recovery must say precisely how
// long the component was down (e.g. "1m16s"), not just that it came back.
func TestInfraAlertMessageFormatsExactDowntime(t *testing.T) {
	msg := InfraAlertMessage("WireGuard tunnel", "node4/uae~wg", "", true, "", 76*time.Second)
	if !strings.Contains(msg, "Recovered") {
		t.Errorf("message should say Recovered: %s", msg)
	}
	if !strings.Contains(msg, "1m16s") {
		t.Errorf("message should contain the exact downtime 1m16s: %s", msg)
	}
}

func TestInfraAlertMessageOmitsDowntimeOnFirstEverDown(t *testing.T) {
	msg := InfraAlertMessage("WireGuard tunnel", "node4/uae~wg", "", false, "exit", 0)
	if strings.Contains(msg, "Was down") {
		t.Errorf("a down (not recovery) message must never claim a downtime duration: %s", msg)
	}
}

// TestInfraAlertMessageNamesTheFaultDomain is the other half of what this
// feature exists for: an admin reading the alert must be able to tell
// whether to fix this host, its tunnel, or wait on a third-party exit -
// three different actions.
func TestInfraAlertMessageNamesTheFaultDomain(t *testing.T) {
	cases := []struct {
		domain string
		want   string
	}{
		{"tunnel", "tunnel interface/config is missing"},
		{"exit", "remote exit"},
		{"node", "no working internet"},
	}
	for _, c := range cases {
		msg := InfraAlertMessage("WireGuard tunnel", "node4/uae~wg", "", false, c.domain, 0)
		if !strings.Contains(msg, c.want) {
			t.Errorf("domain %q: message = %q, want it to mention %q", c.domain, msg, c.want)
		}
	}
}

func TestInfraAlertMessageRelayHasNoDomainReason(t *testing.T) {
	// relayhealth never sets a domain (it has no exit/node distinction to
	// make) - an empty domain must add nothing extra to the message.
	msg := InfraAlertMessage("Relay", "node1-relay (1.2.3.4:5000)", "dial timeout", false, "", 0)
	if strings.Contains(msg, "Likely cause") {
		t.Errorf("a relay alert (no domain) must not render a cause line: %s", msg)
	}
	if !strings.Contains(msg, "dial timeout") {
		t.Errorf("message should still carry the raw error: %s", msg)
	}
}
