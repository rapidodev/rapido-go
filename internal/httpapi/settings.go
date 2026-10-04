package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/integrationsettings"
	"github.com/legendary1205/rapido-go/internal/report"
)

// telegramTopicPlan is the fixed, ordered set of forum topics
// handleSetupTelegramTopics creates - one per report.Category*, with a
// Persian label since that's what this operator's admins read. Order is
// deliberate (most noise-prone/high-frequency categories first) only in
// the sense that it is what the admin sees topics appear in, not a
// functional requirement.
var telegramTopicPlan = []report.TopicRequest{
	{Category: report.CategoryLogin, Name: "🔐 ورود"},
	{Category: report.CategoryUserCreated, Name: "✅ ساخت کاربر"},
	{Category: report.CategoryUserUpdated, Name: "✏️ ویرایش کاربر"},
	{Category: report.CategoryUserDeleted, Name: "🗑 حذف کاربر"},
	{Category: report.CategoryUserDataUsedReset, Name: "📊 ریست حجم"},
	{Category: report.CategoryUserSubRevoked, Name: "🔁 لغو اشتراک"},
	{Category: report.CategoryStatusChange, Name: "🔄 تغییر وضعیت"},
	{Category: report.CategoryInfraAlert, Name: "🛰 هشدار زیرساخت"},
}

// handleSetupTelegramTopics implements POST
// /api/settings/integrations/telegram-topics (sudo only): creates one forum
// topic per notification category in the given group and saves the
// mapping, so each category's own Report call (see internal/report's
// resolve) routes to its own thread instead of every category sharing one
// feed. The target chat must already be a supergroup with Topics (forum
// mode) turned on and this bot added as an admin with "Manage Topics" -
// Telegram's own error for a plain group/channel ("chat is not a forum")
// comes back verbatim in errors below so the admin knows exactly what to
// fix. Whatever categories do succeed are saved even if others fail (a
// transient per-call rate limit should not throw away the ones that
// worked); re-running the same request only retries what's missing since
// handleUpdateIntegrationSettings's topic ids merge, not replace, is reused
// here via a direct DB round-trip.
func (h *Handler) handleSetupTelegramTopics(c *gin.Context) {
	var body struct {
		ChatID int64 `json:"chat_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}

	ctx := c.Request.Context()
	vals, _, err := h.resolveIntegrationSettings(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read integration settings"})
		return
	}
	if vals.TelegramAPIToken == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "Set telegram_api_token first"})
		return
	}

	created, errs := h.reports.CreateTopics(ctx, body.ChatID, telegramTopicPlan)

	current, err := h.store.Queries.GetIntegrationSettings(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read integration settings"})
		return
	}
	merged := integrationsettings.Resolve(current, h.envDefaults).TelegramTopicIDs
	if merged == nil {
		merged = make(map[string]int64, len(created))
	}
	for category, id := range created {
		merged[category] = id
	}
	encodedTopics, err := json.Marshal(merged)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not encode topic ids"})
		return
	}

	params := generated.UpdateIntegrationSettingsParams{
		ResellerApiSecret: current.ResellerApiSecret, ResellerApiUrl: current.ResellerApiUrl, ResellerApiLicense: current.ResellerApiLicense,
		TelegramApiToken: current.TelegramApiToken, TelegramAdminIds: current.TelegramAdminIds, TelegramProxyUrl: current.TelegramProxyUrl,
		TelegramLoggerChannelID: int8FromPtr(&body.ChatID), TelegramLoggerTopicID: current.TelegramLoggerTopicID,
		TelegramDefaultVlessFlow: current.TelegramDefaultVlessFlow,
		WebhookAddresses:         current.WebhookAddresses, WebhookSecret: current.WebhookSecret, DiscordWebhookUrl: current.DiscordWebhookUrl,
		TelegramTopicIds: encodedTopics,
	}
	if _, err := h.store.Queries.UpdateIntegrationSettings(ctx, params); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not save topic ids"})
		return
	}
	if err := h.store.InvalidateIntegrationSettings(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not invalidate integration settings cache"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"chat_id": body.ChatID, "created": created, "errors": errs})
}

type integrationSettingsDTO struct {
	ResellerApiEnabled       bool             `json:"reseller_api_enabled"`
	ResellerApiSecret        *string          `json:"reseller_api_secret"` // masked
	ResellerApiUrl           *string          `json:"reseller_api_url"`
	ResellerApiLicense       *string          `json:"reseller_api_license"` // masked
	TelegramEnabled          bool             `json:"telegram_enabled"`
	TelegramAPIToken         *string          `json:"telegram_api_token"` // masked
	TelegramAdminIDs         []int64          `json:"telegram_admin_ids"`
	TelegramProxyURL         *string          `json:"telegram_proxy_url"` // masked
	TelegramLoggerChannelID  *int64           `json:"telegram_logger_channel_id"`
	TelegramLoggerTopicID    *int64           `json:"telegram_logger_topic_id"`
	TelegramTopicIDs         map[string]int64 `json:"telegram_topic_ids"`
	TelegramDefaultVlessFlow *string          `json:"telegram_default_vless_flow"`
	WebhookAddresses         []string         `json:"webhook_addresses"`
	WebhookSecret            *string          `json:"webhook_secret"`      // masked
	DiscordWebhookURL        *string          `json:"discord_webhook_url"` // masked
	UpdatedAt                *time.Time       `json:"updated_at"`
}

func maskedPtr(value string) *string {
	masked := integrationsettings.Mask(value)
	if masked == "" {
		return nil
	}
	return &masked
}

func plainPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func int64PtrOrNil(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func (h *Handler) resolveIntegrationSettings(ctx *gin.Context) (integrationsettings.Values, generated.IntegrationSetting, error) {
	row, err := h.store.CachedGetIntegrationSettings(ctx.Request.Context())
	if err != nil {
		return integrationsettings.Values{}, row, err
	}
	return integrationsettings.Resolve(row, h.envDefaults), row, nil
}

func toIntegrationSettingsDTO(vals integrationsettings.Values, row generated.IntegrationSetting) integrationSettingsDTO {
	dto := integrationSettingsDTO{
		ResellerApiEnabled:       vals.ResellerApiSecret != "",
		ResellerApiSecret:        maskedPtr(vals.ResellerApiSecret),
		ResellerApiUrl:           plainPtr(vals.ResellerApiUrl),
		ResellerApiLicense:       maskedPtr(vals.ResellerApiLicense),
		TelegramEnabled:          vals.TelegramAPIToken != "",
		TelegramAPIToken:         maskedPtr(vals.TelegramAPIToken),
		TelegramAdminIDs:         vals.TelegramAdminIDs,
		TelegramProxyURL:         maskedPtr(vals.TelegramProxyURL),
		TelegramLoggerChannelID:  int64PtrOrNil(vals.TelegramLoggerChannelID),
		TelegramLoggerTopicID:    int64PtrOrNil(vals.TelegramLoggerTopicID),
		TelegramTopicIDs:         vals.TelegramTopicIDs,
		TelegramDefaultVlessFlow: plainPtr(vals.TelegramDefaultVlessFlow),
		WebhookAddresses:         vals.WebhookAddresses,
		WebhookSecret:            maskedPtr(vals.WebhookSecret),
		DiscordWebhookURL:        maskedPtr(vals.DiscordWebhookURL),
	}
	if row.UpdatedAt.Valid {
		t := row.UpdatedAt.Time
		dto.UpdatedAt = &t
	}
	return dto
}

// handleGetIntegrationSettings implements GET /api/settings/integrations
// (sudo only): the DB override where a sudo admin has set one, otherwise
// whatever env provided. Secrets/tokens come back masked (last 4 characters
// only); IDs and plain hostnames don't need masking and are returned as-is.
func (h *Handler) handleGetIntegrationSettings(c *gin.Context) {
	vals, row, err := h.resolveIntegrationSettings(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read integration settings"})
		return
	}
	c.JSON(http.StatusOK, toIntegrationSettingsDTO(vals, row))
}

// handleUpdateIntegrationSettings implements PUT /api/settings/integrations
// (sudo only). Only fields present in the request body change - a field
// left out keeps its current value, sending it explicitly as null clears
// the override back to "use the env value". This tri-state PATCH semantics
// is why the body is bound as raw JSON keys rather than a plain struct: a
// Go struct of *string/*int64 fields can't distinguish "key omitted" from
// "key present with value null" the way Python's model_dump(exclude_unset=True)
// does, but map[string]json.RawMessage's key presence can.
func (h *Handler) handleUpdateIntegrationSettings(c *gin.Context) {
	var body map[string]json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	if len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "No fields to update"})
		return
	}

	ctx := c.Request.Context()
	current, err := h.store.Queries.GetIntegrationSettings(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read integration settings"})
		return
	}

	params := generated.UpdateIntegrationSettingsParams{
		ResellerApiSecret:        current.ResellerApiSecret,
		ResellerApiUrl:           current.ResellerApiUrl,
		ResellerApiLicense:       current.ResellerApiLicense,
		TelegramApiToken:         current.TelegramApiToken,
		TelegramAdminIds:         current.TelegramAdminIds,
		TelegramProxyUrl:         current.TelegramProxyUrl,
		TelegramLoggerChannelID:  current.TelegramLoggerChannelID,
		TelegramLoggerTopicID:    current.TelegramLoggerTopicID,
		TelegramDefaultVlessFlow: current.TelegramDefaultVlessFlow,
		WebhookAddresses:         current.WebhookAddresses,
		WebhookSecret:            current.WebhookSecret,
		DiscordWebhookUrl:        current.DiscordWebhookUrl,
	}

	if raw, ok := body["reseller_api_secret"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid reseller_api_secret"})
			return
		}
		params.ResellerApiSecret = textFromPtr(v)
	}
	if raw, ok := body["reseller_api_url"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid reseller_api_url"})
			return
		}
		params.ResellerApiUrl = textFromPtr(v)
	}
	if raw, ok := body["reseller_api_license"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid reseller_api_license"})
			return
		}
		params.ResellerApiLicense = textFromPtr(v)
	}
	if raw, ok := body["telegram_api_token"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid telegram_api_token"})
			return
		}
		params.TelegramApiToken = textFromPtr(v)
	}
	if raw, ok := body["telegram_admin_ids"]; ok {
		var v []int64
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid telegram_admin_ids"})
			return
		}
		if len(v) == 0 {
			params.TelegramAdminIds = nil
		} else if encoded, err := json.Marshal(v); err == nil {
			params.TelegramAdminIds = encoded
		}
	}
	if raw, ok := body["telegram_proxy_url"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid telegram_proxy_url"})
			return
		}
		params.TelegramProxyUrl = textFromPtr(v)
	}
	if raw, ok := body["telegram_logger_channel_id"]; ok {
		var v *int64
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid telegram_logger_channel_id"})
			return
		}
		params.TelegramLoggerChannelID = int8FromPtr(v)
	}
	if raw, ok := body["telegram_logger_topic_id"]; ok {
		var v *int64
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid telegram_logger_topic_id"})
			return
		}
		params.TelegramLoggerTopicID = int8FromPtr(v)
	}
	if raw, ok := body["telegram_default_vless_flow"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid telegram_default_vless_flow"})
			return
		}
		params.TelegramDefaultVlessFlow = textFromPtr(v)
	}
	if raw, ok := body["webhook_addresses"]; ok {
		var v []string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid webhook_addresses"})
			return
		}
		if len(v) == 0 {
			params.WebhookAddresses = nil
		} else if encoded, err := json.Marshal(v); err == nil {
			params.WebhookAddresses = encoded
		}
	}
	if raw, ok := body["webhook_secret"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid webhook_secret"})
			return
		}
		params.WebhookSecret = textFromPtr(v)
	}
	if raw, ok := body["discord_webhook_url"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid discord_webhook_url"})
			return
		}
		if err := validateDiscordWebhook(v); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
			return
		}
		params.DiscordWebhookUrl = textFromPtr(v)
	}

	updated, err := h.store.Queries.UpdateIntegrationSettings(ctx, params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not update integration settings"})
		return
	}
	if err := h.store.InvalidateIntegrationSettings(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not invalidate integration settings cache"})
		return
	}
	c.JSON(http.StatusOK, toIntegrationSettingsDTO(integrationsettings.Resolve(updated, h.envDefaults), updated))
}
