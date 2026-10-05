package telegram

import (
	"strings"
	"testing"
	"time"
)

// TestInfraAlertMessageFormatsExactDowntime is the literal real-world
// example this feature was built for: a recovery must say precisely how
// long the component was down (e.g. "1 دقیقه 16 ثانیه"), not just that it
// came back. The message is Persian by explicit admin request - see
// InfraAlertMessage's own doc comment.
func TestInfraAlertMessageFormatsExactDowntime(t *testing.T) {
	msg := InfraAlertMessage("WireGuard tunnel", "node4/uae~wg", "", true, "", 76*time.Second)
	if !strings.Contains(msg, "دوباره وصل شد") {
		t.Errorf("message should say it recovered: %s", msg)
	}
	if !strings.Contains(msg, "1 دقیقه 16 ثانیه") {
		t.Errorf("message should contain the exact downtime (1 دقیقه 16 ثانیه): %s", msg)
	}
}

func TestInfraAlertMessageOmitsDowntimeOnFirstEverDown(t *testing.T) {
	msg := InfraAlertMessage("WireGuard tunnel", "node4/uae~wg", "", false, "exit", 0)
	if strings.Contains(msg, "مدت قطعی") {
		t.Errorf("a down (not recovery) message must never claim a downtime duration: %s", msg)
	}
}

// TestInfraAlertMessageNamesTheFaultDomain is the other half of what this
// feature exists for: an admin reading the alert must be able to tell
// whether to fix this host, its own tunnel config, or wait on a
// third-party exit (e.g. Mullvad) - three different actions.
func TestInfraAlertMessageNamesTheFaultDomain(t *testing.T) {
	cases := []struct {
		domain string
		want   string
	}{
		{"tunnel", "کانفیگ تانل روی همین سرور"},
		{"exit", "طرف مقابل تانل"},
		{"node", "هیچ اینترنت/DNS سالمی ندارد"},
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
	if strings.Contains(msg, "علت احتمالی") {
		t.Errorf("a relay alert (no domain) must not render a cause line: %s", msg)
	}
	if !strings.Contains(msg, "dial timeout") {
		t.Errorf("message should still carry the raw error: %s", msg)
	}
	if !strings.Contains(msg, "رلهٔ تانل") {
		t.Errorf("message should label the kind in Persian: %s", msg)
	}
}

func TestInfraAlertMessageHashtagHasNoSpaces(t *testing.T) {
	// Telegram hashtags break on whitespace - the Persian kind noun (which
	// does have spaces, e.g. "تانل وایرگارد") must never leak into the
	// hashtag itself, only into the separate "نوع" field.
	for _, kind := range []string{"WireGuard tunnel", "Relay"} {
		msg := InfraAlertMessage(kind, "x", "", true, "", 0)
		start := strings.Index(msg, "#")
		end := strings.Index(msg[start:], "<")
		hashtag := msg[start : start+end]
		if strings.Contains(hashtag, " ") {
			t.Errorf("kind=%q: hashtag %q contains a space", kind, hashtag)
		}
	}
}
