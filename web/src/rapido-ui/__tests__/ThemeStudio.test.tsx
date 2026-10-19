import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import i18n from "locales/i18n";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

const adminState = vi.hoisted(() => ({ sudo: true }));
vi.mock("hooks/useCurrentAdminQuery", () => ({
  useCurrentAdminQuery: () => ({ data: { username: "a", is_sudo: adminState.sudo }, isSuccess: true, isPending: false }),
}));

import {
  DEFAULT_APPEARANCE,
  STORAGE_KEY,
  currentAppearance,
  getPanelDefault,
  hasPersonalOverride,
  initAppearance,
  sanitizeAppearance,
  setPanelDefault,
  useAppearance,
} from "../appearance";
import {
  DEFAULT_ALLOWED,
  PANEL_THEME_CACHE_KEY,
  appearanceToPanelTheme,
  applyServerTheme,
  cachedPanelAppearance,
  panelThemeToAppearance,
  validatePanelTheme,
} from "../panelTheme";
import { RapidoShell } from "../Shell";
import { ThemeStudio } from "../ThemeStudio";
import { AppearancePanel } from "../AppearancePanel";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

const html = document.documentElement;
const resetAll = () => {
  window.localStorage.clear();
  setPanelDefault(null);
  useAppearance.getState().reset();
  html.removeAttribute("style");
};

beforeEach(() => {
  resetAll();
  fetchMock.mockReset();
  fetchMock.mockImplementation((url: string) =>
    url === "/settings/theme" ? Promise.resolve({ theme: {}, updated_at: null, updated_by: null, allowed: DEFAULT_ALLOWED }) : Promise.resolve({})
  );
});
afterEach(resetAll);

describe("panel theme validation", () => {
  it("accepts a complete valid theme, with or without the API wrapper", () => {
    const theme = { preset: "aurora", accent: "#22D3EE", palette: { primary: "#ff0000", surface: "#111111" }, depth: "subtle", environment: "off", gauge: "needle", meters: "bars", motion: "reduced", density: "compact", radius: "pill", background: "off", parallax: "off" };
    const v = validatePanelTheme(theme);
    expect(v.ok).toBe(true);
    if (v.ok) {
      expect(v.theme.accent).toBe("#22d3ee");
      expect(v.theme.palette).toEqual({ primary: "#ff0000", surface: "#111111" });
    }
    expect(validatePanelTheme({ theme: { depth: "off" } })).toEqual({ ok: true, theme: { depth: "off" } });
  });

  it("rejects what is not valid and says why, without applying any of it", () => {
    const v = validatePanelTheme({ preset: "neon", accent: "blue", palette: { primary: "#12", sparkle: "#ffffff" }, depth: "full", bogus: 1 });
    expect(v.ok).toBe(false);
    if (!v.ok) {
      expect(v.errors.join("\n")).toMatch(/preset: expected one of cockpit, aurora, graphite, daylight/);
      expect(v.errors).toContain("accent: expected #rrggbb");
      expect(v.errors).toContain("palette.primary: expected #rrggbb");
      expect(v.errors).toContain("palette.sparkle: unknown colour");
      expect(v.errors).toContain("unknown key: bogus");
    }
    expect(validatePanelTheme([]).ok).toBe(false);
    expect(validatePanelTheme(null).ok).toBe(false);
    expect(validatePanelTheme("x").ok).toBe(false);
  });

  it("uses the server's allowed lists when it sends them", () => {
    expect(validatePanelTheme({ preset: "daylight" }, { ...DEFAULT_ALLOWED, preset: ["cockpit"] }).ok).toBe(false);
    expect(validatePanelTheme({ preset: "cockpit" }, { ...DEFAULT_ALLOWED, preset: ["cockpit"] }).ok).toBe(true);
  });
});

describe("panel theme and appearance", () => {
  it("turns a server theme into an appearance: gauge becomes the dial, the accent colour becomes the primary", () => {
    const a = panelThemeToAppearance({ preset: "graphite", accent: "#ff8800", gauge: "needle", environment: "subtle", density: "compact" });
    expect(a).toMatchObject({ theme: "graphite", dial: "needle", environment: "subtle", density: "compact", envAuto: "on", accent: "theme" });
    expect(a.palette.primary).toBe("#ff8800");
  });

  it("lets a palette primary win over the accent, and treats background off as no environment", () => {
    const a = panelThemeToAppearance({ accent: "#111111", palette: { primary: "#222222" }, background: "off", environment: "immersive" });
    expect(a.palette.primary).toBe("#222222");
    expect(a.environment).toBe("off");
  });

  it("keeps what the server omits at its base value", () => {
    const base = sanitizeAppearance({ depth: "off", radius: "sharp" });
    const a = panelThemeToAppearance({ preset: "aurora" }, base);
    expect(a).toMatchObject({ theme: "aurora", depth: "off", radius: "sharp" });
  });

  it("round-trips through the server shape, sending only the colours that are set", () => {
    const a = sanitizeAppearance({ theme: "daylight", depth: "off", dial: "needle", palette: { good: "#00ff00" } });
    const t = appearanceToPanelTheme(a);
    expect(t).toMatchObject({ preset: "daylight", depth: "off", gauge: "needle", background: "on" });
    expect(t.palette).toEqual({ good: "#00ff00" });
    expect(t.accent).toBeUndefined();
    const back = panelThemeToAppearance(t);
    expect(back.theme).toBe("daylight");
    expect(back.palette).toEqual({ good: "#00ff00" });
  });

  it("sends a chosen accent swatch as an accent colour", () => {
    const t = appearanceToPanelTheme(sanitizeAppearance({ accent: "rose" }));
    expect(t.accent).toBe("#fb7185");
  });
});

describe("palette to CSS variables on <html>", () => {
  it("writes a custom palette onto the page and removes it again", () => {
    useAppearance.getState().set({ palette: { primary: "#ff0000", background: "#101820" } });
    expect(html.style.getPropertyValue("--rp-accent")).toBe("255 0 0");
    expect(html.style.getPropertyValue("--rp-bg")).toBe("16 24 32");
    expect(html.style.getPropertyValue("--rp-surface")).not.toBe("");
    useAppearance.getState().set({ palette: {} });
    expect(html.style.getPropertyValue("--rp-accent")).toBe("");
    expect(html.style.getPropertyValue("--rp-bg")).toBe("");
  });

  it("a custom primary beats a chosen accent swatch", () => {
    useAppearance.getState().set({ accent: "rose", palette: { primary: "#123456" } });
    expect(html.style.getPropertyValue("--rp-accent")).toBe("18 52 86");
  });

  it("keeps the built-in look exactly when nothing is customised", () => {
    expect(html.style.getPropertyValue("--rp-accent")).toBe("");
    expect(html.style.getPropertyValue("--rp-good")).toBe("");
    expect(currentAppearance().palette).toEqual({});
  });
});

describe("who wins: my own look or the panel default", () => {
  const panelLook = panelThemeToAppearance({ preset: "aurora", depth: "off" });

  it("a visitor with no look of their own gets the panel default", () => {
    setPanelDefault(panelLook);
    expect(useAppearance.getState().source).toBe("panel");
    expect(html.getAttribute("data-theme")).toBe("aurora");
    expect(html.getAttribute("data-depth")).toBe("off");
  });

  it("a visitor's own saved choice wins over the panel default", () => {
    useAppearance.getState().set({ theme: "graphite" });
    expect(hasPersonalOverride()).toBe(true);
    setPanelDefault(panelLook);
    expect(html.getAttribute("data-theme")).toBe("graphite");
    expect(useAppearance.getState().source).toBe("personal");
  });

  it("starting up with a cached panel default applies it before anything is fetched, unless the visitor has their own", () => {
    window.localStorage.setItem(PANEL_THEME_CACHE_KEY, JSON.stringify({ preset: "daylight" }));
    initAppearance(cachedPanelAppearance());
    expect(html.getAttribute("data-theme")).toBe("daylight");
    expect(useAppearance.getState().source).toBe("panel");

    window.localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...DEFAULT_APPEARANCE, theme: "cockpit" }));
    initAppearance(cachedPanelAppearance());
    expect(useAppearance.getState().source).toBe("personal");
  });

  it("a fresh answer from the server is remembered and applied, and an empty one clears the default", () => {
    applyServerTheme({ preset: "graphite" });
    expect(html.getAttribute("data-theme")).toBe("graphite");
    expect(window.localStorage.getItem(PANEL_THEME_CACHE_KEY)).toContain("graphite");
    expect(getPanelDefault()?.theme).toBe("graphite");
    applyServerTheme({});
    expect(window.localStorage.getItem(PANEL_THEME_CACHE_KEY)).toBeNull();
    expect(getPanelDefault()).toBeNull();
    expect(html.getAttribute("data-theme")).toBe("cockpit");
  });

  it("'use the panel default' in the personal dialog drops my own choices", () => {
    setPanelDefault(panelLook);
    useAppearance.getState().set({ theme: "graphite" });
    render(<AppearancePanel />);
    expect(screen.getByText("You are using your own look, saved in this browser.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: "Panel default" }));
    expect(hasPersonalOverride()).toBe(false);
    expect(html.getAttribute("data-theme")).toBe("aurora");
    expect(useAppearance.getState().source).toBe("panel");
  });

  it("changing any option while following the panel default makes the look mine", () => {
    setPanelDefault(panelLook);
    render(<AppearancePanel />);
    expect(screen.getByText(/You are following the panel default/)).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole("radiogroup", { name: "Density" })).getByRole("radio", { name: "Compact" }));
    expect(hasPersonalOverride()).toBe(true);
    expect(useAppearance.getState().source).toBe("personal");
  });

  it("a preview changes the page but is remembered nowhere", () => {
    useAppearance.getState().preview({ theme: "daylight" });
    expect(html.getAttribute("data-theme")).toBe("daylight");
    expect(hasPersonalOverride()).toBe(false);
  });
});

// ---------------------------------------------------------------------------

const renderStudio = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ThemeStudio />
    </QueryClientProvider>
  );
};

describe("Theme page", () => {
  it("offers the four presets and a colour editor for all eight palette slots", async () => {
    renderStudio();
    expect(within(screen.getByRole("radiogroup", { name: "Theme" })).getAllByRole("radio")).toHaveLength(4);
    const editor = screen.getByTestId("palette-editor");
    expect(within(editor).getAllByLabelText(/^Pick the colour for /)).toHaveLength(8);
    expect(within(editor).getByLabelText("Pick the colour for Primary (blue)")).toBeInTheDocument();
    expect(await screen.findByText(/No panel default is set/)).toBeInTheDocument();
  });

  it("previews a colour live across the page as it is picked, and a hex field takes only valid colours", () => {
    renderStudio();
    fireEvent.change(screen.getByLabelText("Pick the colour for Primary (blue)"), { target: { value: "#ff0000" } });
    expect(html.style.getPropertyValue("--rp-accent")).toBe("255 0 0");
    const hex = screen.getByLabelText("Secondary (violet) #rrggbb");
    fireEvent.change(hex, { target: { value: "#12" } });
    expect(html.style.getPropertyValue("--rp-violet")).toBe("");
    expect(screen.getByText("Use the form #rrggbb")).toBeInTheDocument();
    fireEvent.change(hex, { target: { value: "#00ff88" } });
    expect(html.style.getPropertyValue("--rp-violet")).toBe("0 255 136");
    expect(hasPersonalOverride()).toBe(false);
  });

  it("warns, live, when a colour pair would be unreadable, and clears when it is fixed", () => {
    renderStudio();
    expect(screen.getByTestId("contrast")).toHaveTextContent("Every text and status colour is readable");
    fireEvent.change(screen.getByLabelText("Pick the colour for Primary (blue)"), { target: { value: "#13222a" } });
    const warn = screen.getByTestId("contrast");
    expect(warn).toHaveAttribute("role", "alert");
    expect(warn).toHaveTextContent(/Primary \(blue\) on Surface: .*:1 \(needs at least 3:1\)/);
    fireEvent.click(screen.getByRole("button", { name: /Primary \(blue\): Use the preset's colour/ }));
    expect(screen.getByTestId("contrast")).toHaveAttribute("role", "status");
  });

  it("reverts every change to what was in force when the page opened", () => {
    renderStudio();
    const revert = screen.getByRole("button", { name: "Revert changes" });
    expect(revert).toBeDisabled();
    fireEvent.click(screen.getByRole("radio", { name: /Daylight/ }));
    fireEvent.change(screen.getByLabelText("Pick the colour for Primary (blue)"), { target: { value: "#ff0000" } });
    expect(screen.getByTestId("unsaved")).toBeInTheDocument();
    fireEvent.click(revert);
    expect(html.getAttribute("data-theme")).toBe("cockpit");
    expect(html.style.getPropertyValue("--rp-accent")).toBe("");
    expect(screen.queryByTestId("unsaved")).toBeNull();
  });

  it("drops an unsaved preview when the page is left", () => {
    const { unmount } = renderStudio();
    fireEvent.click(screen.getByRole("radio", { name: /Aurora/ }));
    expect(html.getAttribute("data-theme")).toBe("aurora");
    unmount();
    expect(html.getAttribute("data-theme")).toBe("cockpit");
  });

  it("saves for me only in this browser", () => {
    renderStudio();
    fireEvent.click(screen.getByRole("radio", { name: /Graphite/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save for me" }));
    expect(hasPersonalOverride()).toBe(true);
    expect(JSON.parse(window.localStorage.getItem(STORAGE_KEY) as string).theme).toBe("graphite");
    expect(fetchMock.mock.calls.some((c) => (c[1] as { method?: string } | undefined)?.method === "PUT")).toBe(false);
    expect(screen.getByText("Saved in this browser.")).toBeInTheDocument();
  });

  it("applies to the whole panel with a PUT of the server shape, and makes it the panel default here", async () => {
    fetchMock.mockImplementation((url: string, opts?: { method?: string; body?: { theme: unknown } }) => {
      if (url === "/settings/theme" && opts?.method === "PUT") return Promise.resolve({ theme: opts.body?.theme, updated_at: "2026-10-19T12:00:00Z", updated_by: "admin", allowed: DEFAULT_ALLOWED });
      if (url === "/settings/theme") return Promise.resolve({ theme: {}, allowed: DEFAULT_ALLOWED });
      return Promise.resolve({});
    });
    renderStudio();
    fireEvent.click(screen.getByRole("radio", { name: /Aurora/ }));
    fireEvent.change(screen.getByLabelText("Pick the colour for Good"), { target: { value: "#00ff00" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply to the whole panel" }));
    await screen.findByText("Applied to the whole panel.");
    const put = fetchMock.mock.calls.find((c) => (c[1] as { method?: string } | undefined)?.method === "PUT") as unknown[];
    expect(put[0]).toBe("/settings/theme");
    const body = (put[1] as { body: { theme: Record<string, unknown> } }).body.theme;
    expect(body).toMatchObject({ preset: "aurora", depth: "full", environment: "immersive", gauge: "ring", palette: { good: "#00ff00" } });
    expect(getPanelDefault()?.theme).toBe("aurora");
    expect(hasPersonalOverride()).toBe(false);
  });

  it("shows the server's 422 message when the panel rejects the theme", async () => {
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url === "/settings/theme" && opts?.method === "PUT") return Promise.reject({ response: { _data: { detail: "palette.good must be #rrggbb" } } });
      if (url === "/settings/theme") return Promise.resolve({ theme: {}, allowed: DEFAULT_ALLOWED });
      return Promise.resolve({});
    });
    renderStudio();
    fireEvent.click(screen.getByRole("button", { name: "Apply to the whole panel" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("palette.good must be #rrggbb");
  });

  it("resets the panel default after a confirmation", async () => {
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url === "/settings/theme" && opts?.method === "DELETE") return Promise.resolve({ theme: {}, allowed: DEFAULT_ALLOWED });
      if (url === "/settings/theme") return Promise.resolve({ theme: { preset: "aurora" }, updated_by: "admin", updated_at: "2026-10-19T12:00:00Z", allowed: DEFAULT_ALLOWED });
      return Promise.resolve({});
    });
    renderStudio();
    expect(await screen.findByText(/Panel default set by admin on/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Reset panel default" }));
    expect(screen.getByText(/Remove the panel default\?/)).toBeInTheDocument();
    expect(fetchMock.mock.calls.some((c) => (c[1] as { method?: string } | undefined)?.method === "DELETE")).toBe(false);
    fireEvent.click(within(screen.getByText(/Remove the panel default\?/).parentElement as HTMLElement).getByRole("button", { name: "Reset panel default" }));
    expect(await screen.findByText("The panel default was reset.")).toBeInTheDocument();
  });

  it("exports the current theme as JSON", () => {
    renderStudio();
    fireEvent.change(screen.getByLabelText("Pick the colour for Warning"), { target: { value: "#aabbcc" } });
    fireEvent.click(screen.getByRole("button", { name: "Export JSON" }));
    const box = screen.getByLabelText("Theme JSON") as HTMLTextAreaElement;
    const parsed = JSON.parse(box.value);
    expect(parsed).toMatchObject({ preset: "cockpit", palette: { warn: "#aabbcc" } });
    expect(validatePanelTheme(parsed).ok).toBe(true);
  });

  it("imports a valid theme into the preview and refuses an invalid one without touching anything", () => {
    renderStudio();
    fireEvent.click(screen.getByRole("button", { name: "Import JSON" }));
    const box = screen.getByLabelText("Paste a theme to import");

    fireEvent.change(box, { target: { value: "{nope" } });
    fireEvent.click(screen.getByRole("button", { name: "Use this theme" }));
    expect(screen.getByRole("alert")).toHaveTextContent("That is not valid JSON.");

    fireEvent.change(box, { target: { value: JSON.stringify({ preset: "neon", depth: "huge", palette: { primary: "red" } }) } });
    fireEvent.click(screen.getByRole("button", { name: "Use this theme" }));
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("The theme was not applied:");
    expect(alert).toHaveTextContent("palette.primary: expected #rrggbb");
    expect(html.getAttribute("data-theme")).toBe("cockpit");

    fireEvent.change(box, { target: { value: JSON.stringify({ preset: "daylight", palette: { primary: "#0a84ff" } }) } });
    fireEvent.click(screen.getByRole("button", { name: "Use this theme" }));
    expect(html.getAttribute("data-theme")).toBe("daylight");
    expect(html.style.getPropertyValue("--rp-accent")).toBe("10 132 255");
    expect(screen.getByText("Imported into the preview. Save it to keep it.")).toBeInTheDocument();
    expect(hasPersonalOverride()).toBe(false);
  });
});

describe("Theme in the navigation", () => {
  const renderShell = () =>
    render(
      <MemoryRouter>
        <RapidoShell active="overview">
          <p>page</p>
        </RapidoShell>
      </MemoryRouter>
    );

  it("is in the sidebar for a sudo admin", () => {
    adminState.sudo = true;
    renderShell();
    expect(screen.getByRole("link", { name: "Theme" })).toHaveAttribute("href", "/theme/");
  });

  it("is not there for any other admin, who keeps the personal Appearance dialog", () => {
    adminState.sudo = false;
    renderShell();
    expect(screen.queryByRole("link", { name: "Theme" })).toBeNull();
    expect(screen.getByRole("button", { name: "Appearance" })).toBeInTheDocument();
    adminState.sudo = true;
  });
});

// keep the unused-import linter honest about helpers used only in some tests
void act;
void waitFor;
