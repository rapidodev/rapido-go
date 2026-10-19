package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/auth"
	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// The panel-wide default look. The owner edits it on the dashboard's Theme
// page; admins who have not chosen their own look in their browser get it.
// Only the keys below are ever stored, each checked against its allowed values,
// so a stray or hostile value can never reach the page's CSS.

var (
	hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	// The colours of the data palette and surfaces an owner may override.
	themePaletteKeys = []string{"primary", "secondary", "tertiary", "good", "warn", "bad", "background", "surface"}
	themeChoices     = map[string][]string{
		"preset":      {"cockpit", "aurora", "graphite", "daylight"},
		"depth":       {"off", "subtle", "full"},
		"environment": {"off", "subtle", "immersive"},
		"gauge":       {"ring", "needle"},
		"meters":      {"gauges", "bars"},
		"motion":      {"auto", "full", "reduced", "none"},
		"density":     {"comfortable", "compact"},
		"radius":      {"sharp", "rounded", "pill"},
		"background":  {"on", "off"},
		"parallax":    {"on", "off"},
	}
)

const maxThemeBytes = 4096

// sanitizeTheme validates raw and returns the stored form: only known keys,
// only allowed values. The error names the first offending field.
func sanitizeTheme(raw map[string]json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	for key, v := range raw {
		switch {
		case key == "accent":
			var s string
			if err := json.Unmarshal(v, &s); err != nil || !hexColorRE.MatchString(s) {
				return nil, fmt.Errorf("accent must be a colour like #22d3ee")
			}
			out[key] = strings.ToLower(s)
		case key == "palette":
			var p map[string]string
			if err := json.Unmarshal(v, &p); err != nil {
				return nil, fmt.Errorf("palette must be an object of colours")
			}
			clean := map[string]string{}
			for name, c := range p {
				if !themeHas(themePaletteKeys, name) {
					return nil, fmt.Errorf("palette.%s is not a known colour (allowed: %s)", name, strings.Join(themePaletteKeys, ", "))
				}
				if !hexColorRE.MatchString(c) {
					return nil, fmt.Errorf("palette.%s must be a colour like #22d3ee", name)
				}
				clean[name] = strings.ToLower(c)
			}
			if len(clean) > 0 {
				out[key] = clean
			}
		default:
			allowed, ok := themeChoices[key]
			if !ok {
				return nil, fmt.Errorf("unknown theme setting %q", key)
			}
			var s string
			if err := json.Unmarshal(v, &s); err != nil || !themeHas(allowed, s) {
				return nil, fmt.Errorf("%s must be one of: %s", key, strings.Join(allowed, ", "))
			}
			out[key] = s
		}
	}
	return out, nil
}

func themeHas(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

type themeResponse struct {
	Theme     map[string]any `json:"theme"`
	UpdatedAt *time.Time     `json:"updated_at"`
	UpdatedBy *string        `json:"updated_by"`
	// Allowed lists what each choice setting accepts, so the Theme page does
	// not have to duplicate the rules.
	Allowed map[string][]string `json:"allowed"`
}

func allowedThemeValues() map[string][]string {
	out := map[string][]string{}
	for k, v := range themeChoices {
		out[k] = append([]string(nil), v...)
	}
	keys := append([]string(nil), themePaletteKeys...)
	sort.Strings(keys)
	out["palette"] = keys
	return out
}

func (h *Handler) readTheme(c *gin.Context) (themeResponse, bool) {
	row, err := h.store.Queries.GetPanelTheme(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read the theme"})
		return themeResponse{}, false
	}
	theme := map[string]any{}
	if len(row.Theme) > 0 {
		_ = json.Unmarshal(row.Theme, &theme)
	}
	resp := themeResponse{Theme: theme, Allowed: allowedThemeValues()}
	if row.UpdatedAt.Valid && row.UpdatedBy.Valid {
		t := row.UpdatedAt.Time
		resp.UpdatedAt = &t
		by := row.UpdatedBy.String
		resp.UpdatedBy = &by
	}
	return resp, true
}

func (h *Handler) currentUsername(c *gin.Context) string {
	if identity := auth.CurrentIdentity(c); identity != nil {
		return identity.Username
	}
	return ""
}

// handleGetTheme implements GET /api/settings/theme (any signed-in admin): the
// panel-wide default look, or {} when the owner has not set one.
func (h *Handler) handleGetTheme(c *gin.Context) {
	if resp, ok := h.readTheme(c); ok {
		c.JSON(http.StatusOK, resp)
	}
}

// handlePutTheme implements PUT /api/settings/theme (sudo only). The body is
// {"theme": {...}}; it replaces the stored theme as a whole.
func (h *Handler) handlePutTheme(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil || len(body) > maxThemeBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"detail": "the theme is too large"})
		return
	}
	var req struct {
		Theme map[string]json.RawMessage `json:"theme"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Theme == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": `send {"theme": {...}}`})
		return
	}
	clean, err := sanitizeTheme(req.Theme)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	stored, _ := json.Marshal(clean)
	if _, err := h.store.Queries.SetPanelTheme(c.Request.Context(), generated.SetPanelThemeParams{
		Theme: stored, UpdatedBy: textOrNull(h.currentUsername(c)),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not save the theme"})
		return
	}
	if resp, ok := h.readTheme(c); ok {
		c.JSON(http.StatusOK, resp)
	}
}

// handleResetTheme implements DELETE /api/settings/theme (sudo only): back to
// the built-in look for everyone who has not chosen their own.
func (h *Handler) handleResetTheme(c *gin.Context) {
	if _, err := h.store.Queries.SetPanelTheme(c.Request.Context(), generated.SetPanelThemeParams{
		Theme: []byte("{}"), UpdatedBy: textOrNull(h.currentUsername(c)),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not reset the theme"})
		return
	}
	if resp, ok := h.readTheme(c); ok {
		c.JSON(http.StatusOK, resp)
	}
}
