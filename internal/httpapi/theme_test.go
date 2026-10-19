package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func rawTheme(t *testing.T, js string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("bad test json: %v", err)
	}
	return m
}

func TestSanitizeThemeKeepsKnownValidSettingsAndLowercasesColours(t *testing.T) {
	got, err := sanitizeTheme(rawTheme(t, `{"preset":"cockpit","accent":"#22D3EE","depth":"full","gauge":"ring","environment":"immersive","palette":{"primary":"#22D3EE","secondary":"#A78BFA","tertiary":"#34D399"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["accent"] != "#22d3ee" || got["preset"] != "cockpit" || got["gauge"] != "ring" {
		t.Errorf("sanitised = %v", got)
	}
	pal, _ := got["palette"].(map[string]string)
	if !reflect.DeepEqual(pal, map[string]string{"primary": "#22d3ee", "secondary": "#a78bfa", "tertiary": "#34d399"}) {
		t.Errorf("palette = %v", got["palette"])
	}
}

func TestSanitizeThemeRejectsEverythingThatCouldReachTheStylesheet(t *testing.T) {
	bad := map[string]string{
		"css injection in a colour": `{"accent":"#fff;} body{display:none"}`,
		"named colour":              `{"accent":"red"}`,
		"short hex":                 `{"accent":"#fff"}`,
		"unknown preset":            `{"preset":"neon"}`,
		"unknown depth":             `{"depth":"extreme"}`,
		"unknown setting":           `{"customCss":"x"}`,
		"unknown palette colour":    `{"palette":{"danger":"#ff0000"}}`,
		"palette value not a hex":   `{"palette":{"primary":"url(javascript:1)"}}`,
		"wrong type":                `{"gauge":5}`,
		"palette not an object":     `{"palette":"#ffffff"}`,
	}
	for name, js := range bad {
		if _, err := sanitizeTheme(rawTheme(t, js)); err == nil {
			t.Errorf("%s: %s was accepted", name, js)
		}
	}
}

func TestSanitizeThemeAcceptsAnEmptyThemeAndDropsAnEmptyPalette(t *testing.T) {
	got, err := sanitizeTheme(rawTheme(t, `{"palette":{}}`))
	if err != nil || len(got) != 0 {
		t.Errorf("empty palette: %v %v", got, err)
	}
}

func TestThemeEndpointsStoreAValidatedThemeForEveryAdminAndOnlySudoCanChangeIt(t *testing.T) {
	router, sudoToken := newTestRouter(t)
	doRequest(t, router, "POST", "/api/admin", sudoToken, map[string]interface{}{"username": "theme-viewer", "password": "pw12345", "is_sudo": false})
	viewer := loginAs(t, router, "theme-viewer", "pw12345")

	// Nothing set yet: an empty theme, and the allowed values are published.
	resp := doRequest(t, router, "GET", "/api/settings/theme", viewer, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("get as reseller: %d %v", resp.Code, resp.Body)
	}
	if th, _ := resp.Body["theme"].(map[string]interface{}); len(th) != 0 {
		t.Errorf("fresh panel has a theme: %v", resp.Body["theme"])
	}
	if allowed, _ := resp.Body["allowed"].(map[string]interface{}); allowed["preset"] == nil || allowed["palette"] == nil {
		t.Errorf("allowed values missing: %v", resp.Body["allowed"])
	}

	// Only sudo may change it.
	resp = doRequest(t, router, "PUT", "/api/settings/theme", viewer, map[string]interface{}{"theme": map[string]interface{}{"accent": "#22d3ee"}})
	if resp.Code != http.StatusForbidden {
		t.Errorf("a reseller changed the panel theme: %d %v", resp.Code, resp.Body)
	}
	resp = doRequest(t, router, "PUT", "/api/settings/theme", sudoToken, map[string]interface{}{"theme": map[string]interface{}{"accent": "red"}})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("an invalid colour was accepted: %d %v", resp.Code, resp.Body)
	}

	// A valid theme is stored, normalised, and every admin reads it back.
	resp = doRequest(t, router, "PUT", "/api/settings/theme", sudoToken, map[string]interface{}{"theme": map[string]interface{}{
		"preset": "cockpit", "accent": "#A78BFA", "depth": "full", "gauge": "ring",
		"palette": map[string]interface{}{"primary": "#22D3EE", "secondary": "#A78BFA", "tertiary": "#34D399"},
	}})
	if resp.Code != http.StatusOK {
		t.Fatalf("put theme: %d %v", resp.Code, resp.Body)
	}
	if resp.Body["updated_by"] == nil {
		t.Errorf("who changed it is not recorded: %v", resp.Body)
	}
	resp = doRequest(t, router, "GET", "/api/settings/theme", viewer, nil)
	th, _ := resp.Body["theme"].(map[string]interface{})
	if th["accent"] != "#a78bfa" || th["gauge"] != "ring" {
		t.Errorf("reseller reads %v", th)
	}

	// Reset goes back to the built-in look.
	resp = doRequest(t, router, "DELETE", "/api/settings/theme", sudoToken, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("reset: %d %v", resp.Code, resp.Body)
	}
	resp = doRequest(t, router, "GET", "/api/settings/theme", viewer, nil)
	if th, _ := resp.Body["theme"].(map[string]interface{}); len(th) != 0 {
		t.Errorf("theme survived a reset: %v", th)
	}
}
