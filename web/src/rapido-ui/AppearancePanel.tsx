import { FC, ReactNode, useState } from "react";
import { createPortal } from "react-dom";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { SwatchIcon } from "@heroicons/react/24/outline";
import {
  ACCENT_IDS,
  ACCENT_SWATCH,
  Appearance,
  AccentId,
  THEME_IDS,
  ThemeId,
  getPanelDefault,
  useAppearance,
} from "./appearance";
import { Button } from "./Button";
import { Card, CardSubtitle, CardTitle } from "./Card";
import { Modal } from "./Modal";
import { ResourceMeter } from "./ResourceMeter";
import { Badge } from "./Badge";
import { Segmented } from "./Segmented";

// Every visual effect in the panel is a choice made here. The panel itself
// only reads and writes the appearance store; the effect of each choice is
// applied to <html> (see appearance.ts) so the live preview below - and the
// rest of the page behind it - reacts at once.

const Row: FC<{ title: string; hint?: string; children: ReactNode }> = ({ title, hint, children }) => (
  <div className="flex flex-col gap-1.5">
    <div className="text-xs font-semibold text-rapido-text">{title}</div>
    {children}
    {hint && <div className="text-[11px] leading-snug text-rapido-muted">{hint}</div>}
  </div>
);

// A tile that shows what a preset looks like by carrying that preset's own
// data-theme: the colour variables are defined per [data-theme], so the
// tile re-colours itself regardless of which theme the page is using.
export const ThemeTile: FC<{ id: ThemeId; selected: boolean; name: string; desc: string; onPick: () => void }> = ({
  id,
  selected,
  name,
  desc,
  onPick,
}) => (
  <button
    type="button"
    role="radio"
    aria-checked={selected}
    data-theme={id}
    onClick={onPick}
    className={classNames(
      "group flex flex-col gap-2 rounded-xl2 border bg-rapido-bg p-2.5 text-start transition-all focus:outline-none focus-visible:ring-2 focus-visible:ring-rapido-accent/60",
      selected ? "border-rapido-accent shadow-rp-glow" : "border-rapido-border hover:border-rapido-accent/50"
    )}
  >
    <span className="relative block h-14 overflow-hidden rounded-lg border border-rapido-border bg-rapido-surface">
      <span
        className="absolute -start-3 -top-4 h-14 w-14 rounded-full"
        style={{ background: "radial-gradient(circle, rgb(var(--rp-blob-1) / .55), transparent 70%)" }}
      />
      <span
        className="absolute -bottom-5 -end-2 h-14 w-14 rounded-full"
        style={{ background: "radial-gradient(circle, rgb(var(--rp-blob-2) / .5), transparent 70%)" }}
      />
      <span className="absolute inset-x-2 top-2 h-2 rounded-full bg-rapido-text/70" />
      <span className="absolute inset-x-2 top-5 h-1.5 w-1/2 rounded-full bg-rapido-muted/60" />
      <span className="absolute bottom-2 start-2 h-3 w-8 rounded-md bg-rapido-accent" />
      <span className="absolute bottom-2 start-12 h-3 w-5 rounded-md border border-rapido-border bg-rapido-raised" />
    </span>
    <span className="text-xs font-semibold text-rapido-text">{name}</span>
    <span className="text-[11px] leading-snug text-rapido-muted">{desc}</span>
  </button>
);

/** What the look is and how to change it: preset, accent, and every option. Used by the personal
 * dialog (bound to the browser's own choice) and by the Theme page (bound to a draft). */
export const AppearanceControls: FC<{
  value: Appearance;
  onChange: (patch: Partial<Appearance>) => void;
  /** Hide the accent swatches (the Theme page has its own palette editor). */
  hideAccent?: boolean;
  /** Hide the preset tiles (the Theme page shows them itself, above the palette). */
  hidePreset?: boolean;
}> = ({ value: a, onChange: set, hideAccent, hidePreset }) => {
  const { t } = useTranslation();
  const on = <K extends keyof Appearance>(key: K) => (v: Appearance[K]) => set({ [key]: v } as Partial<Appearance>);
  const L = (key: string) => String(t(`rapido.appearance.${key}`));

  return (
    <div className="flex flex-col gap-5" data-testid="appearance-controls">
      {!hidePreset && (
      <Row title={L("theme")}>
        <div role="radiogroup" aria-label={L("theme")} className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
          {THEME_IDS.map((id) => (
            <ThemeTile
              key={id}
              id={id}
              selected={a.theme === id}
              name={L(`theme.${id}`)}
              desc={L(`themeDesc.${id}`)}
              onPick={() => set({ theme: id })}
            />
          ))}
        </div>
      </Row>

      )}

      {!hideAccent && (
      <Row title={L("accent")}>
        <div role="radiogroup" aria-label={L("accent")} className="flex flex-wrap gap-2">
          {ACCENT_IDS.map((id: AccentId) => {
            const selected = a.accent === id;
            return (
              <button
                key={id}
                type="button"
                role="radio"
                aria-checked={selected}
                aria-label={L(`accent.${id}`)}
                title={L(`accent.${id}`)}
                onClick={() => set({ accent: id, palette: { ...a.palette, primary: undefined } })}
                className={classNames(
                  "relative h-8 w-8 rounded-full border-2 transition-transform hover:scale-110 focus:outline-none focus-visible:ring-2 focus-visible:ring-rapido-accent/60",
                  selected ? "border-rapido-text" : "border-transparent"
                )}
                style={{
                  background:
                    id === "theme"
                      ? "conic-gradient(from 0deg, rgb(var(--rp-accent)), rgb(var(--rp-violet)), rgb(var(--rp-teal)), rgb(var(--rp-accent)))"
                      : ACCENT_SWATCH[id],
                }}
              />
            );
          })}
        </div>
      </Row>
      )}

      <div className="grid gap-5 sm:grid-cols-2">
        <Row title={L("depth")} hint={L("depthHint")}>
          <Segmented
            label={L("depth")}
            value={a.depth}
            onChange={on("depth")}
            options={(["off", "subtle", "full"] as const).map((v) => ({ value: v, label: L(`depth.${v}`) }))}
          />
        </Row>
        <Row title={L("environment")} hint={a.envAuto === "on" ? L("environmentAutoHint") : L("environmentHint")}>
          <Segmented
            label={L("environment")}
            value={a.environment}
            onChange={(v) => set({ environment: v, envAuto: "off" })}
            options={(["off", "subtle", "immersive"] as const).map((v) => ({ value: v, label: L(`environment.${v}`) }))}
          />
        </Row>
        <Row title={L("parallax")}>
          <Segmented
            label={L("parallax")}
            value={a.parallax}
            onChange={on("parallax")}
            options={(["on", "off"] as const).map((v) => ({ value: v, label: L(`parallax.${v}`) }))}
          />
        </Row>
        <Row title={L("motion")} hint={L("motionHint")}>
          <Segmented
            label={L("motion")}
            value={a.motion}
            onChange={on("motion")}
            options={(["auto", "full", "reduced", "none"] as const).map((v) => ({ value: v, label: L(`motion.${v}`) }))}
          />
        </Row>
        <Row title={L("meters")}>
          <Segmented
            label={L("meters")}
            value={a.meters}
            onChange={on("meters")}
            options={(["gauges", "bars"] as const).map((v) => ({ value: v, label: L(`meters.${v}`) }))}
          />
        </Row>
        <Row title={L("dial")}>
          <Segmented
            label={L("dial")}
            value={a.dial}
            onChange={on("dial")}
            options={(["needle", "ring"] as const).map((v) => ({ value: v, label: L(`dial.${v}`) }))}
          />
        </Row>
        <Row title={L("density")}>
          <Segmented
            label={L("density")}
            value={a.density}
            onChange={on("density")}
            options={(["comfortable", "compact"] as const).map((v) => ({ value: v, label: L(`density.${v}`) }))}
          />
        </Row>
        <Row title={L("radius")}>
          <Segmented
            label={L("radius")}
            value={a.radius}
            onChange={on("radius")}
            options={(["sharp", "rounded", "pill"] as const).map((v) => ({ value: v, label: L(`radius.${v}`) }))}
          />
        </Row>
      </div>

    </div>
  );
};

export const AppearancePanel: FC = () => {
  const { t } = useTranslation();
  const a = useAppearance();
  const { set, reset } = a;
  const L = (key: string) => String(t(`rapido.appearance.${key}`));
  const panelDefault = getPanelDefault() !== null;

  return (
    <div className="flex flex-col gap-5" data-testid="appearance-panel">
      <AppearanceControls value={a} onChange={set} />

      <Row title={L("preview")}>
        <Card tilt elevation={2} className="flex flex-wrap items-center gap-4" data-testid="appearance-preview">
          <ResourceMeter value={62} max={100} unit="%" label={L("previewGauge")} size="md" />
          <div className="flex min-w-[10rem] flex-1 flex-col gap-2">
            <div className="flex items-center gap-2">
              <CardTitle>{L("previewCard")}</CardTitle>
              <Badge tone="green">{L("previewBadge")}</Badge>
            </div>
            <CardSubtitle>{L("previewText")}</CardSubtitle>
            <div className="flex flex-wrap gap-2">
              <Button variant="primary">{L("previewPrimary")}</Button>
              <Button variant="secondary">{L("previewSecondary")}</Button>
            </div>
          </div>
        </Card>
      </Row>

      {/* Whose look is this? My own choice beats the panel default; clearing it goes back to the default. */}
      <Row title={L("source")} hint={a.source === "personal" ? L("sourcePersonalHint") : panelDefault ? L("sourcePanelHint") : L("sourceNoPanelHint")}>
        <Segmented
          label={L("source")}
          value={a.source === "personal" ? "personal" : "panel"}
          onChange={(v) => (v === "panel" ? reset() : set({}))}
          options={[
            { value: "panel", label: L("source.panel") },
            { value: "personal", label: L("source.personal") },
          ]}
        />
      </Row>

      <div className="flex justify-end">
        <Button variant="secondary" onClick={reset}>
          {L("reset")}
        </Button>
      </div>
    </div>
  );
};

/** The panel in a dialog, for the Shell Appearance button. */
export const AppearanceModal: FC<{ onClose: () => void }> = ({ onClose }) => {
  const { t } = useTranslation();
  return (
    <Modal onClose={onClose} className="max-w-3xl">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <CardTitle className="text-base">{t("rapido.appearance.title")}</CardTitle>
          <CardSubtitle>{t("rapido.appearance.subtitle")}</CardSubtitle>
        </div>
        <Button variant="secondary" onClick={onClose}>
          {t("rapido.appearance.close")}
        </Button>
      </div>
      <AppearancePanel />
    </Modal>
  );
};

/** Nav-style button that opens the dialog; used by the sidebar and mobile header. */
export const AppearanceButton: FC<{ className?: string }> = ({ className }) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className={classNames(
          "flex items-center gap-2 rounded-lg px-3 py-2 text-start text-sm font-medium text-rapido-muted transition-colors hover:bg-rapido-raised hover:text-rapido-text",
          className
        )}
      >
        <SwatchIcon className="h-5 w-5 shrink-0" aria-hidden="true" />
        <span className="truncate">{t("rapido.appearance.nav")}</span>
      </button>
      {/* Portalled: the sidebar and header are blurred glass, and a blurred
          ancestor would become the containing block of the fixed overlay. */}
      {open && createPortal(<AppearanceModal onClose={() => setOpen(false)} />, document.body)}
    </>
  );
};
