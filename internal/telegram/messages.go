package telegram

import (
	"fmt"
	"html"
	"time"

	"github.com/legendary1205/rapido-go/internal/subscription"
)

// belongsToText mirrors app/telegram/handlers/report.py's
// `admin.username if admin else None` fed straight into a Python
// str.format() call - when there's no owning admin, Python's formatter
// stringifies None as the literal text "None", which is what every existing
// Telegram admin has actually been seeing. Preserved here rather than
// "fixed" to a blank/dash, since this is cosmetic and changing it would
// make a real admin's message history inconsistent with what's shown from
// here on.
func belongsToText(belongsTo *string) string {
	if belongsTo == nil {
		return "None"
	}
	return html.EscapeString(*belongsTo)
}

func formatDataLimit(limit *int64) string {
	if limit == nil || *limit == 0 {
		return "Unlimited"
	}
	return subscription.ReadableSize(*limit)
}

func formatExpire(expire *int64) string {
	if expire == nil || *expire == 0 {
		return "Never"
	}
	// Local server time, matching Python's naive datetime.fromtimestamp
	// (not forced to UTC) - admins are used to seeing local time here.
	return time.Unix(*expire, 0).Format("15:04:05 2006-01-02")
}

func joinProxies(proxies []string) string {
	out := ""
	for i, p := range proxies {
		if i > 0 {
			out += ", "
		}
		out += html.EscapeString(p)
	}
	return out
}

func NewUserMessage(username, byUsername string, dataLimit, expire *int64, proxies []string, hasNextPlan bool, resetStrategy string, belongsTo *string) string {
	return fmt.Sprintf(
		"🆕 <b>#Created</b>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Username :</b> <code>%s</code>\n"+
			"<b>Traffic Limit :</b> <code>%s</code>\n"+
			"<b>Expire Date :</b> <code>%s</code>\n"+
			"<b>Proxies :</b> <code>%s</code>\n"+
			"<b>Data Limit Reset Strategy :</b> <code>%s</code>\n"+
			"<b>Has Next Plan :</b> <code>%s</code>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Belongs To :</b> <code>%s</code>\n"+
			"<b>By :</b> <b>#%s</b>",
		html.EscapeString(username), formatDataLimit(dataLimit), formatExpire(expire),
		joinProxies(proxies), html.EscapeString(resetStrategy), boolText(hasNextPlan),
		belongsToText(belongsTo), html.EscapeString(byUsername),
	)
}

func ModifiedMessage(username, byUsername string, dataLimit, expire *int64, proxies []string, hasNextPlan bool, resetStrategy string, belongsTo *string) string {
	return fmt.Sprintf(
		"✏️ <b>#Modified</b>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Username :</b> <code>%s</code>\n"+
			"<b>Traffic Limit :</b> <code>%s</code>\n"+
			"<b>Expire Date :</b> <code>%s</code>\n"+
			"<b>Protocols :</b> <code>%s</code>\n"+
			"<b>Data Limit Reset Strategy :</b> <code>%s</code>\n"+
			"<b>Has Next Plan :</b> <code>%s</code>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Belongs To :</b> <code>%s</code>\n"+
			"<b>By :</b> <b>#%s</b>",
		html.EscapeString(username), formatDataLimit(dataLimit), formatExpire(expire),
		joinProxies(proxies), html.EscapeString(resetStrategy), boolText(hasNextPlan),
		belongsToText(belongsTo), html.EscapeString(byUsername),
	)
}

func DeletedMessage(username, byUsername string, belongsTo *string) string {
	return fmt.Sprintf(
		"🗑 <b>#Deleted</b>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Username</b> : <code>%s</code>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Belongs To :</b> <code>%s</code>\n"+
			"<b>By</b> : <b>#%s</b>",
		html.EscapeString(username), belongsToText(belongsTo), html.EscapeString(byUsername),
	)
}

// statusLabels mirrors app/telegram/handlers/report.py's `_status` map.
// Fixed vs. Python: that map has no "on_hold" entry, so a manual PUT
// /api/user setting status=on_hold raises a KeyError there that the outer
// bare `except` swallows - report.status_change silently sends nothing.
// on_hold is a reachable status_change value in both systems (an admin can
// PUT a disabled user straight to on_hold), so it needs a real entry, not
// just the automatic reviewjob on_hold->active transition.
var statusLabels = map[string]string{
	"active":   "✅ <b>#Activated</b>",
	"disabled": "❌ <b>#Disabled</b>",
	"limited":  "🪫 <b>#Limited</b>",
	"expired":  "🕔 <b>#Expired</b>",
	"on_hold":  "🔌 <b>#OnHold</b>",
}

func StatusChangeMessage(username, status string, belongsTo *string) string {
	label, ok := statusLabels[status]
	if !ok {
		label = "<b>#" + html.EscapeString(status) + "</b>"
	}
	return fmt.Sprintf(
		"%s\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Username</b> : <code>%s</code>\n"+
			"<b>Belongs To :</b> <code>%s</code>",
		label, html.EscapeString(username), belongsToText(belongsTo),
	)
}

func UsageResetMessage(username, byUsername string, belongsTo *string) string {
	return fmt.Sprintf(
		"🔁 <b>#Reset</b>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Username</b> : <code>%s</code>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Belongs To :</b> <code>%s</code>\n"+
			"<b>By</b> : <b>#%s</b>",
		html.EscapeString(username), belongsToText(belongsTo), html.EscapeString(byUsername),
	)
}

func DataResetByNextMessage(username string, dataLimit, expire *int64) string {
	return fmt.Sprintf(
		"🔁 <b>#AutoReset</b>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Username :</b> <code>%s</code>\n"+
			"<b>Traffic Limit :</b> <code>%s</code>\n"+
			"<b>Expire Date :</b> <code>%s</code>\n"+
			"➖➖➖➖➖➖➖➖➖",
		html.EscapeString(username), formatDataLimit(dataLimit), formatExpire(expire),
	)
}

func SubscriptionRevokedMessage(username, byUsername string, belongsTo *string) string {
	return fmt.Sprintf(
		"🔁 <b>#Revoked</b>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Username</b> : <code>%s</code>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Belongs To :</b> <code>%s</code>\n"+
			"<b>By</b> : <b>#%s</b>",
		html.EscapeString(username), belongsToText(belongsTo), html.EscapeString(byUsername),
	)
}

// LoginMessage deliberately has no password parameter at all - the current
// Python system's report_login sends the admin's plaintext attempted
// password to this exact message (permanently, into Telegram's chat
// history); the user explicitly decided this rewrite must not replicate
// that, so only username/client_ip/status are reported.
// InfraAlertMessage reports an infrastructure component outside the normal
// user lifecycle - a WireGuard tunnel or an external relay - changing
// availability. up is the new state. Has no owning-admin concept, like
// LoginMessage: this is fleet-wide, not tied to one reseller's users.
//
// domain narrows down WHERE a down component's fault most likely sits
// (see tunnelhealth.DialProbe's own doc comment for how a WireGuard
// tunnel's probe tells these apart; relayhealth never sets it, since a
// relay probe has no "exit vs. node" distinction to make) - rendered as a
// short, actionable sentence via domainReason so an admin does not have
// to guess whether to go fix this host, its tunnel, or wait on a
// third-party provider. downFor is the exact time the component was down,
// only meaningful (non-zero) on a recovery.
func InfraAlertMessage(kind, name, detail string, up bool, domain string, downFor time.Duration) string {
	icon, label := "🔴", "Down"
	if up {
		icon, label = "🟢", "Recovered"
	}
	msg := fmt.Sprintf(
		"%s <b>#Infra%s</b>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>%s</b> : <code>%s</code>",
		icon, label, html.EscapeString(kind), html.EscapeString(name),
	)
	if up {
		if downFor > 0 {
			msg += fmt.Sprintf("\n<b>Was down</b> : <code>%s</code>", downFor.Round(time.Second).String())
		}
		return msg
	}
	if reason := domainReason(domain); reason != "" {
		msg += fmt.Sprintf("\n<b>Likely cause</b> : <code>%s</code>", html.EscapeString(reason))
	}
	if detail != "" {
		msg += fmt.Sprintf("\n<b>Error</b> : <code>%s</code>", html.EscapeString(detail))
	}
	return msg
}

// domainReason turns a tunnelhealth fault domain into a short, actionable
// sentence - see InfraAlertMessage's own doc comment. An empty or
// unrecognized domain (relayhealth's own alerts, which have no such
// concept) renders nothing extra.
func domainReason(domain string) string {
	switch domain {
	case "tunnel":
		return "This host's own tunnel interface/config is missing - fix it here, not the remote exit"
	case "exit":
		return "This host's own internet is fine - the tunnel's remote exit (e.g. Mullvad) is not responding or not forwarding traffic"
	case "node":
		return "This host has no working internet/DNS at all right now - every tunnel on it will show the same fault"
	default:
		return ""
	}
}

func LoginMessage(username, clientIP, status string) string {
	return fmt.Sprintf(
		"🔐 <b>#Login</b>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>Username</b> : <code>%s</code>\n"+
			"<b>Client ip</b> : <code>%s</code>\n"+
			"➖➖➖➖➖➖➖➖➖\n"+
			"<b>login status</b> : <code>%s</code>",
		html.EscapeString(username), html.EscapeString(clientIP), html.EscapeString(status),
	)
}

func boolText(b bool) string {
	if b {
		return "True"
	}
	return "False"
}
