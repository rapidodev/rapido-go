package discord

import (
	"fmt"
	"strings"
	"time"

	"github.com/legendary1205/rapido-go/internal/subscription"
)

// belongsToText mirrors app/discord/handlers/report.py's
// `admin.username if admin else None` fed into an f-string - Python
// stringifies None as the literal text "None" there, preserved here for
// the same reason as telegram.belongsToText.
func belongsToText(belongsTo *string) string {
	if belongsTo == nil {
		return "None"
	}
	return *belongsTo
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
	// Local server time, matching Python's naive datetime.fromtimestamp.
	return time.Unix(*expire, 0).Format("15:04:05 2006-01-02")
}

func joinProxies(proxies []string) string {
	return strings.Join(proxies, ", ")
}

var statusLabels = map[string]string{
	"active":   "**:white_check_mark: Activated**",
	"disabled": "**:x: Disabled**",
	"limited":  "**:low_battery: #Limited**",
	"expired":  "**:clock5: #Expired**",
	// Fixed vs. Python: no on_hold entry there either (same KeyError gap as
	// telegram.statusLabels) - see that package's comment for detail.
	"on_hold": "**:electric_plug: #OnHold**",
}

var statusColors = map[string]int{
	"active":   0x9ae6b4,
	"disabled": 0x424b59,
	"limited":  0xf8a7a8,
	"expired":  0xfbd38d,
	"on_hold":  0x90cdf4,
}

func StatusChangePayload(username, status string, belongsTo *string) EmbedPayload {
	label, ok := statusLabels[status]
	if !ok {
		label = "**#" + status + "**"
	}
	color, ok := statusColors[status]
	if !ok {
		color = 0x424b59
	}
	return EmbedPayload{Embeds: []Embed{{
		Description: fmt.Sprintf("%s\n----------------------\n**Username:** %s", label, username),
		Color:       color,
		Footer:      &Footer{Text: "Belongs To: " + belongsToText(belongsTo)},
	}}}
}

func NewUserPayload(username, byUsername string, dataLimit, expire *int64, proxies []string, hasNextPlan bool, resetStrategy string, belongsTo *string) EmbedPayload {
	return EmbedPayload{Embeds: []Embed{{
		Title: ":new: Created",
		Description: fmt.Sprintf(
			"\n**Username:** %s\n**Traffic Limit:** %s\n**Expire Date:** %s\n**Proxies:** %s\n**Data Limit Reset Strategy:**%s\n**Has Next Plan:**%t",
			username, formatDataLimit(dataLimit), formatExpire(expire), joinProxies(proxies), resetStrategy, hasNextPlan,
		),
		Footer: &Footer{Text: fmt.Sprintf("Belongs To: %s\nBy: %s", belongsToText(belongsTo), byUsername)},
		Color:  0x00ff00,
	}}}
}

func ModifiedPayload(username, byUsername string, dataLimit, expire *int64, proxies []string, hasNextPlan bool, resetStrategy string, belongsTo *string) EmbedPayload {
	return EmbedPayload{Embeds: []Embed{{
		Title: ":pencil2: Modified",
		Description: fmt.Sprintf(
			"\n**Username:** %s\n**Traffic Limit:** %s\n**Expire Date:** %s\n**Proxies:** %s\n**Data Limit Reset Strategy:**%s\n**Has Next Plan:**%t",
			username, formatDataLimit(dataLimit), formatExpire(expire), joinProxies(proxies), resetStrategy, hasNextPlan,
		),
		Footer: &Footer{Text: fmt.Sprintf("Belongs To: %s\nBy: %s", belongsToText(belongsTo), byUsername)},
		Color:  0x00ffff,
	}}}
}

func DeletedPayload(username, byUsername string, belongsTo *string) EmbedPayload {
	return EmbedPayload{Embeds: []Embed{{
		Title:       ":wastebasket: Deleted",
		Description: "**Username: **" + username,
		Footer:      &Footer{Text: fmt.Sprintf("Belongs To: %s\nBy: %s", belongsToText(belongsTo), byUsername)},
		Color:       0xff0000,
	}}}
}

func UsageResetPayload(username, byUsername string, belongsTo *string) EmbedPayload {
	return EmbedPayload{Embeds: []Embed{{
		Title:       ":repeat: Reset",
		Description: "**Username:** " + username,
		Footer:      &Footer{Text: fmt.Sprintf("Belongs To: %s\nBy: %s", belongsToText(belongsTo), byUsername)},
		Color:       0x00ffff,
	}}}
}

func DataResetByNextPayload(username string, dataLimit, expire *int64) EmbedPayload {
	return EmbedPayload{Embeds: []Embed{{
		Title: ":repeat: AutoReset",
		Description: fmt.Sprintf(
			"\n**Username:** %s\n**Traffic Limit:** %s\n**Expire Date:** %s",
			username, formatDataLimit(dataLimit), formatExpire(expire),
		),
		Color: 0x00ffff,
	}}}
}

func SubscriptionRevokedPayload(username, byUsername string, belongsTo *string) EmbedPayload {
	return EmbedPayload{Embeds: []Embed{{
		Title:       ":repeat: Revoked",
		Description: "**Username:** " + username,
		Footer:      &Footer{Text: fmt.Sprintf("Belongs To: %s\nBy: %s", belongsToText(belongsTo), byUsername)},
		Color:       0xff0000,
	}}}
}

// InfraAlertPayload mirrors telegram.InfraAlertMessage's own fields and,
// by the same explicit admin request, its Persian wording - see that
// function's own doc comment for what domain/downFor mean and why this is
// the one message in either package not in English.
func InfraAlertPayload(kind, name, detail string, up bool, domain string, downFor time.Duration) EmbedPayload {
	kindFA := infraKindFA(kind)
	title, color := ":red_circle: "+kindFA+" قطع شد", 0xff0000
	if up {
		title, color = ":green_circle: "+kindFA+" دوباره وصل شد", 0x00ff00
	}
	desc := "**" + kindFA + ":** " + name
	if up {
		if downFor > 0 {
			desc += "\n**مدت قطعی:** " + infraPersianDuration(downFor)
		}
		return EmbedPayload{Embeds: []Embed{{Title: title, Description: desc, Color: color}}}
	}
	if reason := infraDomainReasonFA(domain); reason != "" {
		desc += "\n**علت احتمالی:** " + reason
	}
	if detail != "" {
		desc += "\n**خطای خام:** " + detail
	}
	return EmbedPayload{Embeds: []Embed{{Title: title, Description: desc, Color: color}}}
}

// infraKindFA mirrors telegram.infraKindFA - kept as a separate copy
// rather than shared, matching this package's existing standalone
// (import-free of internal/telegram) message-building style.
func infraKindFA(kind string) string {
	switch kind {
	case "WireGuard tunnel":
		return "تانل وایرگارد"
	case "Relay":
		return "رلهٔ تانل"
	default:
		return kind
	}
}

// infraDomainReasonFA mirrors telegram.domainReasonFA.
func infraDomainReasonFA(domain string) string {
	switch domain {
	case "tunnel":
		return "رابط/کانفیگ تانل روی همین سرور وجود ندارد یا بالا نیامده - مشکل از سمت ملوداد/اکسیت نیست، باید همین‌جا بررسی شود"
	case "exit":
		return "اینترنت خود این سرور سالم است، ولی طرف مقابل تانل (معمولاً Mullvad) پاسخ نمی‌دهد یا ترافیک را فوروارد نمی‌کند"
	case "node":
		return "این سرور در حال حاضر هیچ اینترنت/DNS سالمی ندارد - این یک مشکل کلی سرور است، نه مربوط به یک تانل خاص"
	default:
		return ""
	}
}

// infraPersianDuration mirrors telegram.persianDuration.
func infraPersianDuration(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	h, m, sec := s/3600, (s%3600)/60, s%60
	switch {
	case h > 0:
		return fmt.Sprintf("%d ساعت %d دقیقه %d ثانیه", h, m, sec)
	case m > 0:
		return fmt.Sprintf("%d دقیقه %d ثانیه", m, sec)
	default:
		return fmt.Sprintf("%d ثانیه", sec)
	}
}

// LoginPayload deliberately has no password parameter - see
// telegram.LoginMessage's comment; the same security decision applies to
// both integrations.
func LoginPayload(username, clientIP, status string) EmbedPayload {
	return EmbedPayload{Embeds: []Embed{{
		Title:       ":repeat: Login",
		Description: fmt.Sprintf("\n**Username:** %s\n**Client ip**: %s", username, clientIP),
		Footer:      &Footer{Text: "login status: " + status},
		Color:       0xff0000,
	}}}
}
