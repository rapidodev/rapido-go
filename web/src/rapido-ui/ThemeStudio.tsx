import { ChangeEvent, FC, ReactNode, useEffect, useMemo, useRef, useState } from "react";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { useResetThemeMutation, useSaveThemeMutation, useThemeQuery } from "hooks/useThemeQuery";
import { errorText } from "service/errors";
import { Appearance, THEME_IDS, ThemeId, currentAppearance, useAppearance } from "./appearance";
import { AppearanceControls } from "./AppearancePanel";
import { Badge } from "./Badge";
import { Button } from "./Button";
import { Card, CardSubtitle, CardTitle } from "./Card";
import { Input } from "./Input";
import { JsonEditor } from "./JsonEditor";
import { DEFAULT_PALETTE, PALETTE_KEYS, Palette, PaletteKey, contrastIssues, isHex, rgbToHex } from "./palette";
import {
  DEFAULT_ALLOWED,
  appearanceToPanelTheme,
  applyServerTheme,
  panelThemeToAppearance,
  validatePanelTheme,
} from "./panelTheme";
import { GAUGE_CYAN, GAUGE_VIOLET, TYPE, solidZones } from "./instrument";
import { ResourceMeter } from "./ResourceMeter";
import { SegmentBar, TrendBars } from "./Segments";
import { StatusPill } from "./StatusPill";
import { ThemeTile } from "./AppearancePanel";

// The Theme page: one place to design the look - a preset, a full custom palette, and every
// option - with every change applied live to the real UI (the draft lives in the appearance
// store, applied to <html>, remembered nowhere until it is saved). It can be kept for this
// browser only, or applied to the whole panel as the default for every admin who has not
// chosen their own look.

const COLOR_VAR: Record<PaletteKey, string> = {
  primary: "--rp-accent",
  secondary: "--rp-violet",
  tertiary: "--rp-good",
  good: "--rp-good",
  warn: "--rp-warn",
  bad: "--rp-bad",
  background: "--rp-bg",
  surface: "--rp-surface",
};

/** The colour a slot has right now (a custom one, else the preset's own as the page computes it). */
const effectiveHex = (key: PaletteKey, palette: Palette): string => {
  const set = palette[key];
  if (isHex(set)) return set;
  try {
    const raw = getComputedStyle(document.documentElement).getPropertyValue(COLOR_VAR[key]).trim();
    const parts = raw.split(/\s+/).map(Number);
    if (parts.length === 3 && parts.every((n) => Number.isFinite(n))) return rgbToHex([parts[0], parts[1], parts[2]]);
  } catch {
    // fall through to the built-in colour
  }
  return DEFAULT_PALETTE[key];
};

const ColorRow: FC<{ slot: PaletteKey; palette: Palette; onChange: (hex: string | undefined) => void }> = ({ slot, palette, onChange }) => {
  const { t } = useTranslation();
  const name = String(t(`rapido.theme.color.${slot}`));
  const current = effectiveHex(slot, palette);
  const custom = isHex(palette[slot]);
  const [text, setText] = useState(current);
  useEffect(() => setText(current), [current]);
  const bad = text !== "" && !isHex(text);

  return (
    <div className="flex items-center gap-3" data-slot={slot}>
      <input
        type="color"
        aria-label={String(t("rapido.theme.pickColor", { name }))}
        value={current}
        onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.target.value.toLowerCase())}
        className="h-9 w-11 shrink-0 cursor-pointer rounded-lg border border-rapido-border bg-transparent p-0.5"
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <span className={classNames("text-xs font-medium", custom ? "text-rapido-text" : "text-rapido-muted")}>{name}</span>
        <Input
          dir="ltr"
          value={text}
          hasError={bad}
          aria-label={`${name} #rrggbb`}
          className="mt-0.5 py-1 text-xs"
          onChange={(e) => {
            const v = e.target.value.trim();
            setText(v);
            if (isHex(v)) onChange(v.toLowerCase());
          }}
        />
        {bad && <span className="mt-0.5 text-[11px] text-rapido-bad">{t("rapido.theme.hexInvalid")}</span>}
      </div>
      <Button variant="chip" disabled={!custom} onClick={() => onChange(undefined)} title={String(t("rapido.theme.resetColor"))} aria-label={`${name}: ${t("rapido.theme.resetColor")}`}>
        ↺
      </Button>
    </div>
  );
};

const sameLook = (a: Appearance, b: Appearance) => JSON.stringify(a) === JSON.stringify(b);

const Section: FC<{ title: string; hint?: string; children: ReactNode }> = ({ title, hint, children }) => (
  <section className="flex flex-col gap-3">
    <div>
      <h2 className="text-sm font-semibold text-rapido-text">{title}</h2>
      {hint && <p className="mt-0.5 text-xs text-rapido-muted">{hint}</p>}
    </div>
    {children}
  </section>
);

export const ThemeStudio: FC = () => {
  const { t, i18n } = useTranslation();
  const L = (k: string, o?: Record<string, unknown>) => String(o ? t(k, o) : t(k));
  const draft = useAppearance();
  const preview = useAppearance((s) => s.preview);
  const { data: panel } = useThemeQuery(true);
  const save = useSaveThemeMutation();
  const resetPanel = useResetThemeMutation();

  // What was in force when the page opened; "revert" and leaving without saving return to it.
  const baseline = useRef<Appearance>(currentAppearance());
  const saved = useRef(false);
  const [notice, setNotice] = useState<{ tone: "ok" | "err"; text: string } | null>(null);
  const [confirmReset, setConfirmReset] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState("");
  const [importErrors, setImportErrors] = useState<string[]>([]);
  const [copied, setCopied] = useState(false);

  const look = currentAppearance();
  const dirty = !sameLook(look, baseline.current);
  const allowed = panel?.allowed ?? DEFAULT_ALLOWED;
  const presets = THEME_IDS.filter((id) => (allowed.preset ?? DEFAULT_ALLOWED.preset).includes(id));
  const issues = useMemo(() => contrastIssues(look.palette), [look.palette]); // eslint-disable-line react-hooks/exhaustive-deps
  const json = useMemo(() => JSON.stringify(appearanceToPanelTheme(look), null, 2), [look]);

  // Leaving with unsaved changes drops them: the live preview was only ever a preview.
  useEffect(
    () => () => {
      if (!saved.current) useAppearance.getState().preview(baseline.current);
    },
    []
  );

  const setPalette = (slot: PaletteKey, hex: string | undefined) => {
    const palette = { ...look.palette };
    if (hex) palette[slot] = hex;
    else delete palette[slot];
    preview({ palette, ...(slot === "primary" && hex ? { accent: "theme" as const } : {}) });
  };

  const saveForMe = () => {
    useAppearance.getState().set(look);
    baseline.current = currentAppearance();
    saved.current = true;
    setNotice({ tone: "ok", text: L("rapido.theme.saved") });
  };

  const applyToPanel = () => {
    setNotice(null);
    save.mutate(appearanceToPanelTheme(look), {
      onSuccess: (res) => {
        applyServerTheme(res.theme);
        // The panel default is what this browser now follows too, unless it holds its own choice.
        baseline.current = currentAppearance();
        saved.current = true;
        setNotice({ tone: "ok", text: L("rapido.theme.appliedPanel") });
      },
      onError: (e) => setNotice({ tone: "err", text: errorText(e, L("rapido.theme.failed")) }),
    });
  };

  const doResetPanel = () => {
    setNotice(null);
    resetPanel.mutate(undefined, {
      onSuccess: (res) => {
        applyServerTheme(res.theme);
        setConfirmReset(false);
        setNotice({ tone: "ok", text: L("rapido.theme.resetDone") });
      },
      onError: (e) => {
        setConfirmReset(false);
        setNotice({ tone: "err", text: errorText(e, L("rapido.theme.failed")) });
      },
    });
  };

  const doImport = () => {
    setImportErrors([]);
    let raw: unknown;
    try {
      raw = JSON.parse(importText);
    } catch {
      setImportErrors([L("rapido.theme.importInvalidJson")]);
      return;
    }
    const v = validatePanelTheme(raw, allowed);
    if (!v.ok) {
      setImportErrors(v.errors);
      return;
    }
    preview(panelThemeToAppearance(v.theme, look));
    setImportOpen(false);
    setImportText("");
    setNotice({ tone: "ok", text: L("rapido.theme.importOk") });
  };

  const copyJson = async () => {
    try {
      await navigator.clipboard.writeText(json);
      setCopied(true);
    } catch {
      // clipboard blocked: the text is selectable in the box
    }
  };
  const downloadJson = () => {
    const url = URL.createObjectURL(new Blob([json], { type: "application/json" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "rapido-theme.json";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  const when = panel?.updated_at ? new Date(panel.updated_at).toLocaleString(i18n.language) : "";
  const hasPanel = !!panel && Object.keys(panel.theme ?? {}).length > 0;

  return (
    <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_22rem]" data-testid="theme-studio">
      <Card className="flex min-w-0 flex-col gap-6">
        <p className="text-xs text-rapido-muted" data-testid="panel-default-status">
          {hasPanel ? L("rapido.theme.defaultSet", { by: panel?.updated_by || "-", when }) : L("rapido.theme.defaultNone")}
        </p>

        <Section title={L("rapido.appearance.theme")}>
          <div role="radiogroup" aria-label={L("rapido.appearance.theme")} className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
            {presets.map((id: ThemeId) => (
              <ThemeTile
                key={id}
                id={id}
                selected={look.theme === id}
                name={L(`rapido.appearance.theme.${id}`)}
                desc={L(`rapido.appearance.themeDesc.${id}`)}
                onPick={() => preview({ theme: id })}
              />
            ))}
          </div>
        </Section>

        <Section title={L("rapido.theme.palette")} hint={L("rapido.theme.paletteHint")}>
          <div className="grid gap-x-6 gap-y-3 sm:grid-cols-2" data-testid="palette-editor">
            {PALETTE_KEYS.map((slot) => (
              <ColorRow key={slot} slot={slot} palette={look.palette} onChange={(hex) => setPalette(slot, hex)} />
            ))}
          </div>
          <div
            role={issues.length ? "alert" : "status"}
            data-testid="contrast"
            className={classNames(
              "rounded-lg border px-3 py-2.5 text-xs",
              issues.length ? "border-rapido-warn/40 bg-rapido-warn/10 text-rapido-warn" : "border-rapido-border bg-rapido-raised/30 text-rapido-muted"
            )}
          >
            <div className="font-semibold">{L("rapido.theme.contrastTitle")}</div>
            {issues.length === 0 ? (
              <p className="mt-0.5">{L("rapido.theme.contrastOk")}</p>
            ) : (
              <ul className="mt-1 flex flex-col gap-0.5">
                {issues.map((i) => {
                  const nm = (k: string) => (PALETTE_KEYS.includes(k as PaletteKey) ? L(`rapido.theme.color.${k}`) : L(`rapido.theme.contrast.${k}`));
                  return (
                    <li key={i.pair.join("-")} dir="ltr">
                      {L("rapido.theme.contrastIssue", { a: nm(i.pair[0]), b: nm(i.pair[1]), ratio: i.ratio, min: i.min })}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </Section>

        <Section title={L("rapido.theme.look")}>
          <AppearanceControls value={look} onChange={(patch) => preview(patch)} hideAccent hidePreset />
        </Section>
      </Card>

      {/* the live strip and the actions stay in view while the controls scroll */}
      <div className="flex min-w-0 flex-col gap-4 lg:sticky lg:top-4 lg:self-start">
        <Card className="flex flex-col gap-4" data-testid="theme-preview">
          <CardTitle>{L("rapido.theme.preview")}</CardTitle>
          <div className="flex flex-wrap items-center gap-4">
            <ResourceMeter value={62} max={100} unit="%" label={L("rapido.theme.sample.cpu")} size={120} />
            <div className="flex min-w-[9rem] flex-1 flex-col gap-2">
              <CardSubtitle>{L("rapido.theme.sample.card")}</CardSubtitle>
              <div className="flex flex-wrap gap-1.5">
                <StatusPill tone="green" live>OK</StatusPill>
                <StatusPill tone="yellow">!</StatusPill>
                <StatusPill tone="red">x</StatusPill>
                <Badge tone="brand">blue</Badge>
                <Badge tone="violet">violet</Badge>
              </div>
              <SegmentBar value={68} max={100} count={20} className="h-1.5" />
              <TrendBars values={[12, 30, 22, 48, 40, 66, 54, 72, 60, 82, 74, 90]} max={100} bars={12} height={28} zones={solidZones(GAUGE_CYAN)} />
              <TrendBars values={[20, 24, 30, 28, 40, 38, 52, 50, 60, 58, 64, 70]} max={100} bars={12} height={20} zones={solidZones(GAUGE_VIOLET)} />
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="primary">{L("rapido.appearance.previewPrimary")}</Button>
            <Button variant="secondary">{L("rapido.appearance.previewSecondary")}</Button>
          </div>
        </Card>

        <Card className="flex flex-col gap-3">
          {dirty && (
            <p className={classNames(TYPE.caption, "text-rapido-warn")} data-testid="unsaved">
              {L("rapido.theme.unsaved")}
            </p>
          )}
          <div className="flex flex-col gap-2">
            <Button variant="primary" onClick={saveForMe}>
              {L("rapido.theme.saveMine")}
            </Button>
            <Button variant="primary" disabled={save.isPending} onClick={applyToPanel}>
              {save.isPending ? L("rapido.pleaseWait") : L("rapido.theme.applyPanel")}
            </Button>
            {confirmReset ? (
              <div className="flex flex-col gap-2 rounded-lg border border-rapido-bad/40 bg-rapido-bad/10 p-2.5 text-xs text-rapido-bad">
                {L("rapido.theme.resetPanelConfirm")}
                <div className="flex gap-2">
                  <Button variant="chip" tone="red" disabled={resetPanel.isPending} onClick={doResetPanel}>
                    {L("rapido.theme.resetPanel")}
                  </Button>
                  <Button variant="chip" onClick={() => setConfirmReset(false)}>
                    {L("cancel")}
                  </Button>
                </div>
              </div>
            ) : (
              <Button variant="secondary" onClick={() => setConfirmReset(true)}>
                {L("rapido.theme.resetPanel")}
              </Button>
            )}
            <div className="grid grid-cols-2 gap-2">
              <Button variant="secondary" aria-expanded={exportOpen} onClick={() => setExportOpen((v) => !v)}>
                {L("rapido.theme.export")}
              </Button>
              <Button variant="secondary" aria-expanded={importOpen} onClick={() => setImportOpen((v) => !v)}>
                {L("rapido.theme.import")}
              </Button>
            </div>
            <Button variant="secondary" disabled={!dirty} onClick={() => preview(baseline.current)}>
              {L("rapido.theme.revert")}
            </Button>
          </div>

          {notice && (
            <div
              role={notice.tone === "err" ? "alert" : "status"}
              className={classNames(
                "rounded-lg border px-3 py-2 text-xs",
                notice.tone === "ok" ? "border-rapido-good/40 bg-rapido-good/10 text-rapido-good" : "border-rapido-bad/40 bg-rapido-bad/10 text-rapido-bad"
              )}
            >
              {notice.text}
            </div>
          )}

          {exportOpen && (
            <div className="flex flex-col gap-2" data-testid="export-box">
              <span className="text-xs font-medium text-rapido-text">{L("rapido.theme.exportTitle")}</span>
              <JsonEditor readOnly gutter={false} value={json} rows={10} ariaLabel={L("rapido.theme.exportTitle")} />
              <div className="flex gap-2">
                <Button variant="chip" tone="accent" onClick={() => void copyJson()}>
                  {copied ? L("rapido.theme.copied") : L("rapido.theme.copy")}
                </Button>
                <Button variant="chip" onClick={downloadJson}>
                  {L("rapido.theme.download")}
                </Button>
              </div>
            </div>
          )}

          {importOpen && (
            <div className="flex flex-col gap-2" data-testid="import-box">
              <span className="text-xs font-medium text-rapido-text">{L("rapido.theme.importTitle")}</span>
              <JsonEditor gutter={false} value={importText} rows={8} ariaLabel={L("rapido.theme.importTitle")} onChange={setImportText} />
              {importErrors.length > 0 && (
                <div role="alert" className="rounded-lg border border-rapido-bad/40 bg-rapido-bad/10 px-3 py-2 text-xs text-rapido-bad">
                  <div className="font-medium">{L("rapido.theme.importErrors")}</div>
                  <ul className="mt-1 list-disc ps-4" dir="ltr">
                    {importErrors.map((e) => (
                      <li key={e}>{e}</li>
                    ))}
                  </ul>
                </div>
              )}
              <Button variant="chip" tone="accent" disabled={!importText.trim()} onClick={doImport}>
                {L("rapido.theme.importApply")}
              </Button>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
};
