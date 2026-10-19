import { FC, PropsWithChildren, ReactNode, TouchEvent, useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import classNames from "classnames";
import {
  HomeIcon,
  UsersIcon,
  GlobeAltIcon,
  ShieldCheckIcon,
  PuzzlePieceIcon,
  DocumentDuplicateIcon,
  TicketIcon,
  ServerStackIcon,
  ChartBarIcon,
  CommandLineIcon,
  Cog6ToothIcon,
  CircleStackIcon,
  ArrowsRightLeftIcon,
  SignalIcon,
  Bars3Icon,
  XMarkIcon,
  ChevronDoubleLeftIcon,
  ChevronDoubleRightIcon,
  ArrowRightOnRectangleIcon,
  PaintBrushIcon,
} from "@heroicons/react/24/outline";
import { ReactComponent as Logo } from "assets/logo.svg";
import { useCurrentAdminQuery } from "hooks/useCurrentAdminQuery";
import { removeAuthToken } from "utils/authStorage";
import { extensionNavItems, mergeNavItems } from "extensions";
import { LanguageSwitcher } from "rapido-ui/LanguageSwitcher";
import { AppearanceButton } from "rapido-ui/AppearancePanel";
import { dirOf } from "utils/language";
import "rapido-ui/tailwind.css";

const NavLink: FC<{
  href: string;
  active?: boolean;
  icon: FC<{ className?: string }>;
  children: ReactNode;
  /** Desktop rail collapsed to icons: the label stays for screen readers and as a tooltip. */
  collapsed?: boolean;
  title?: string;
  onNavigate?: () => void;
}> = ({ href, active, icon: Icon, children, collapsed, title, onNavigate }) => (
  <Link
    to={href}
    title={collapsed ? title : undefined}
    onClick={onNavigate}
    className={classNames(
      "relative flex items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium transition-all duration-200",
      active
        ? "bg-gradient-to-r from-rapido-accent/20 to-rapido-violet/10 text-rapido-accent shadow-[inset_0_0_0_1px_rgba(34,211,238,.25),0_0_18px_-6px_rgba(34,211,238,.6)]"
        : "text-rapido-muted hover:translate-x-0.5 hover:bg-rapido-raised hover:text-rapido-text rtl:hover:-translate-x-0.5"
    )}
  >
    <Icon className="h-5 w-5 shrink-0" />
    <span className={classNames("truncate", collapsed && "md:sr-only")}>{children}</span>
  </Link>
);

// This phase's whole page list. "coreConfig" (the nav key/route stayed
// this name for stability, see CoreConfigPage.tsx's own comment) is the
// merged Xray/sing-box config page - log level, sniffing, outbounds,
// routing rules, DNS, AND inbounds all as one JSON document, plus the
// Hosts admin embedded directly below it - not a port of the old
// dashboard's raw-JSON CoreSettings.tsx (that shape doesn't exist here at
// all, the engine changed from Xray to sing-box) and no longer split
// across a separate "Inbounds" nav item either (see
// rapido-ui/XrayConfigAdmin.tsx). Nodes and Monitoring used to be the
// "still missing" pages too (node-side reporting hadn't landed yet); both
// now have real Go-backed pages (internal/httpapi/node.go,
// internal/httpapi/monitoring.go).
export type RapidoNavKey =
  | "overview"
  | "users"
  | "tickets"
  | "hosts"
  | "coreConfig"
  | "nodes"
  | "monitoring"
  | "tunnels"
  | "theme"
  | "logs"
  | "admins"
  | "templates"
  | "integrations"
  | "backups"
  | "gateway"
  // Reached from the Appearance button and the settings pages, not the nav.
  | "appearance";

const NAV_ITEMS: {
  key: RapidoNavKey;
  href: string;
  labelKey: string;
  icon: FC<{ className?: string }>;
  sudoOnly?: boolean;
}[] = [
  { key: "overview", href: "/", labelKey: "rapido.overview", icon: HomeIcon },
  { key: "users", href: "/users/", labelKey: "users", icon: UsersIcon },
  // Deliberately not sudoOnly: GET /api/tickets is requireAdmin, not
  // requireSudo - a reseller answers their own customers' tickets, and the
  // backend already scopes the list to the users they own.
  { key: "tickets", href: "/tickets/", labelKey: "rapido.tickets.nav", icon: TicketIcon },
  { key: "hosts", href: "/hosts/", labelKey: "rapido.hosts.nav", icon: GlobeAltIcon, sudoOnly: true },
  {
    // Formerly two nav items (Inbounds, Core Config) - merged into one, see
    // rapido-ui/XrayConfigAdmin.tsx's own doc comment.
    key: "coreConfig",
    href: "/core-config/",
    labelKey: "rapido.xrayConfig.nav",
    icon: Cog6ToothIcon,
    sudoOnly: true,
  },
  { key: "nodes", href: "/nodes/", labelKey: "rapido.nodes.nav", icon: ServerStackIcon, sudoOnly: true },
  {
    key: "monitoring",
    href: "/monitoring/",
    labelKey: "rapido.monitoring.nav",
    icon: ChartBarIcon,
    sudoOnly: true,
  },
  {
    key: "tunnels",
    href: "/tunnels/",
    labelKey: "rapido.tunnels.nav",
    icon: SignalIcon,
    sudoOnly: true,
  },
  { key: "theme", href: "/theme/", labelKey: "rapido.theme.nav", icon: PaintBrushIcon, sudoOnly: true },
  // GET /api/logs and /api/logs/sources are requireSudo, same as Monitoring.
  { key: "logs", href: "/logs/", labelKey: "rapido.logs.nav", icon: CommandLineIcon, sudoOnly: true },
  { key: "admins", href: "/admins/", labelKey: "rapido.admins.nav", icon: ShieldCheckIcon, sudoOnly: true },
  // Not sudoOnly: every admin can use their own reusable presets, not just
  // sudo - the backend's own GET /api/user_template is requireAdmin, not
  // requireSudo (only the write endpoints are sudo-gated).
  { key: "templates", href: "/templates/", labelKey: "rapido.templates.nav", icon: DocumentDuplicateIcon },
  {
    key: "integrations",
    href: "/integrations/",
    labelKey: "rapido.integrations.nav",
    icon: PuzzlePieceIcon,
    sudoOnly: true,
  },
  {
    key: "backups",
    href: "/backups/",
    labelKey: "rapido.backups.nav",
    icon: CircleStackIcon,
    sudoOnly: true,
  },
  {
    key: "gateway",
    href: "/gateway/",
    labelKey: "rapido.gateway.nav",
    icon: ArrowsRightLeftIcon,
    sudoOnly: true,
  },
];

// The built-in entries plus any an optional add-on registers (none in the public build).
const ALL_NAV_ITEMS: { key: string; href: string; labelKey: string; icon: FC<{ className?: string }>; sudoOnly?: boolean }[] =
  mergeNavItems<{ key: string; href: string; labelKey: string; icon: FC<{ className?: string }>; sudoOnly?: boolean }>(NAV_ITEMS, extensionNavItems());

const COLLAPSE_KEY = "rapido.navCollapsed";
const readCollapsed = (): boolean => {
  try {
    return window.localStorage.getItem(COLLAPSE_KEY) === "1";
  } catch {
    return false;
  }
};

/** True from the md breakpoint up (the nav is a rail); false on a phone (a drawer). */
const useIsDesktop = (): boolean => {
  const query = "(min-width: 768px)";
  const [desktop, setDesktop] = useState(() =>
    typeof window === "undefined" || typeof window.matchMedia !== "function" ? true : window.matchMedia(query).matches
  );
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia(query);
    const on = () => setDesktop(mq.matches);
    on();
    if (mq.addEventListener) mq.addEventListener("change", on);
    else mq.addListener(on);
    return () => {
      if (mq.removeEventListener) mq.removeEventListener("change", on);
      else mq.removeListener(on);
    };
  }, []);
  return desktop;
};

const FOCUSABLE = 'a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])';

export const RapidoShell: FC<PropsWithChildren<{ active: RapidoNavKey | (string & {}) }>> = ({ active, children }) => {
  const navigate = useNavigate();
  const { t, i18n } = useTranslation();
  const { data, isSuccess, isPending } = useCurrentAdminQuery();
  const dir = dirOf(i18n.language);
  const desktop = useIsDesktop();
  const [open, setOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(readCollapsed);
  const drawer = useRef<HTMLElement>(null);
  const opener = useRef<HTMLButtonElement>(null);
  const touchStart = useRef<{ x: number; y: number } | null>(null);

  // Fail closed: while /admin is still in flight the admin is treated as
  // non-sudo, so a link the backend would answer with 403 is never shown.
  const isSudo = !isPending && isSuccess && !!data?.is_sudo;
  const navItems = ALL_NAV_ITEMS.filter((item) => !item.sudoOnly || isSudo);

  const logout = () => {
    removeAuthToken();
    navigate("/login");
  };

  const close = useCallback(() => {
    setOpen(false);
    opener.current?.focus();
  }, []);

  const toggleCollapsed = () =>
    setCollapsed((c) => {
      try {
        window.localStorage.setItem(COLLAPSE_KEY, c ? "0" : "1");
      } catch {
        // the choice just lasts until reload
      }
      return !c;
    });

  const drawerOpen = open && !desktop;

  // The phone drawer: Escape closes it, Tab stays inside it, the page behind
  // does not scroll, and focus moves in on open (back to the button on close).
  useEffect(() => {
    if (!drawerOpen) return;
    const el = drawer.current;
    el?.querySelector<HTMLElement>(FOCUSABLE)?.focus();
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
      } else if (e.key === "Tab" && el) {
        const items = [...el.querySelectorAll<HTMLElement>(FOCUSABLE)];
        if (items.length === 0) return;
        const firstEl = items[0];
        const lastEl = items[items.length - 1];
        if (e.shiftKey && document.activeElement === firstEl) {
          e.preventDefault();
          lastEl.focus();
        } else if (!e.shiftKey && document.activeElement === lastEl) {
          e.preventDefault();
          firstEl.focus();
        }
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prevOverflow;
    };
  }, [drawerOpen, close]);

  // Growing to a desktop width resets the drawer state, so it is not left
  // "open" when the rail comes back.
  useEffect(() => {
    if (desktop) setOpen(false);
  }, [desktop]);

  // A swipe toward the inline-start edge closes the drawer (left in LTR, right in RTL).
  const onTouchStart = (e: TouchEvent) => {
    touchStart.current = { x: e.touches[0].clientX, y: e.touches[0].clientY };
  };
  const onTouchMove = (e: TouchEvent) => {
    const s = touchStart.current;
    if (!s) return;
    const dx = e.touches[0].clientX - s.x;
    const dy = e.touches[0].clientY - s.y;
    const toward = dir === "rtl" ? dx : -dx;
    if (toward > 64 && Math.abs(dy) < Math.abs(dx)) {
      touchStart.current = null;
      close();
    }
  };

  const rail = desktop && collapsed;
  // Out of the tab order and hidden from assistive tech while it is an off-screen drawer.
  const hidden = !desktop && !open;

  return (
    <div
      dir={dir}
      // `lang` is what tells the shaper this run is Persian, which is how the
      // browser picks Vazirmatn's Arabic subset over a device fallback for
      // codepoints several fonts could claim. `font-sans` states the stack
      // rather than inheriting it from a theme, so the Rapido pages keep
      // their typography no matter what.
      lang={i18n.language}
      className="relative min-h-screen max-w-full overflow-x-clip font-sans text-rapido-text"
    >
      <div className="flex max-w-full">
        {/* phone: a dimmed backdrop that closes the drawer on tap */}
        <div
          aria-hidden="true"
          onClick={close}
          className={classNames(
            "rp-drawer-backdrop fixed inset-0 z-40 bg-black/55 backdrop-blur-sm transition-opacity duration-300 md:hidden",
            drawerOpen ? "opacity-100" : "pointer-events-none opacity-0"
          )}
        />
        <aside
          id="rapido-nav"
          ref={drawer}
          role={desktop ? undefined : "dialog"}
          aria-modal={desktop ? undefined : drawerOpen}
          aria-label={String(t("rapido.nav.menu"))}
          aria-hidden={hidden || undefined}
          {...(hidden ? { inert: "" } : {})}
          onTouchStart={desktop ? undefined : onTouchStart}
          onTouchMove={desktop ? undefined : onTouchMove}
          className={classNames(
            "rp-drawer z-50 flex flex-col rounded-xl2 border border-rapido-border bg-rapido-surface/90 p-4 shadow-rp-3 backdrop-blur-lg",
            // phone: a drawer from the inline-start edge
            "max-md:fixed max-md:inset-y-2 max-md:start-2 max-md:w-72 max-md:max-w-[85vw] max-md:transition-transform max-md:duration-300 max-md:ease-out",
            drawerOpen ? "max-md:translate-x-0" : "max-md:-translate-x-[115%] max-md:rtl:translate-x-[115%]",
            // desktop: a floating rail, optionally icons-only
            "md:sticky md:top-3 md:m-3 md:h-[calc(100vh-1.5rem)] md:shrink-0 md:bg-rapido-surface/60 md:transition-[width] md:duration-200",
            collapsed ? "md:w-[4.75rem] md:px-2.5" : "md:w-56"
          )}
        >
          <div className={classNames("mb-6 flex items-center gap-2 px-2", rail && "md:justify-center md:px-0")}>
            <Logo className="h-6 w-6 shrink-0 text-rapido-accent drop-shadow-[0_0_8px_rgb(var(--rp-accent)/.7)]" />
            <span
              className={classNames(
                "bg-gradient-to-r from-rapido-text to-rapido-accent bg-clip-text text-lg font-bold text-transparent",
                rail && "md:hidden"
              )}
            >
              Rapido
            </span>
            <button
              type="button"
              onClick={close}
              aria-label={String(t("rapido.nav.close"))}
              className="ms-auto flex h-8 w-8 items-center justify-center rounded-lg text-rapido-muted hover:bg-rapido-raised hover:text-rapido-text md:hidden"
            >
              <XMarkIcon className="h-5 w-5" />
            </button>
            <button
              type="button"
              onClick={toggleCollapsed}
              aria-label={String(t(collapsed ? "rapido.nav.expand" : "rapido.nav.collapse"))}
              aria-pressed={collapsed}
              className={classNames(
                "hidden h-8 w-8 items-center justify-center rounded-lg text-rapido-muted hover:bg-rapido-raised hover:text-rapido-text md:flex",
                rail ? "md:hidden" : "ms-auto"
              )}
            >
              {dir === "rtl" ? <ChevronDoubleRightIcon className="h-4 w-4" /> : <ChevronDoubleLeftIcon className="h-4 w-4" />}
            </button>
          </div>
          <nav className="flex flex-col gap-1 overflow-y-auto">
            {navItems.map((item) => (
              <NavLink
                key={item.key}
                href={item.href}
                active={active === item.key}
                icon={item.icon}
                collapsed={rail}
                title={String(t(item.labelKey))}
                onNavigate={() => setOpen(false)}
              >
                {t(item.labelKey)}
              </NavLink>
            ))}
          </nav>
          <div className={classNames("mt-auto flex flex-col gap-1 pt-3", rail && "md:items-center md:[&_span.truncate]:sr-only")}>
            {rail && (
              <button
                type="button"
                onClick={toggleCollapsed}
                aria-label={String(t("rapido.nav.expand"))}
                title={String(t("rapido.nav.expand"))}
                className="hidden h-9 w-9 items-center justify-center rounded-lg text-rapido-muted hover:bg-rapido-raised hover:text-rapido-text md:flex"
              >
                {dir === "rtl" ? <ChevronDoubleLeftIcon className="h-4 w-4" /> : <ChevronDoubleRightIcon className="h-4 w-4" />}
              </button>
            )}
            <AppearanceButton />
            <LanguageSwitcher openDirection="up" />
            <button
              onClick={logout}
              title={String(t("header.logout"))}
              className="flex items-center gap-2 rounded-lg px-3 py-2 text-start text-sm font-medium text-rapido-muted transition-colors hover:bg-rapido-raised hover:text-rapido-text"
            >
              <ArrowRightOnRectangleIcon className="h-5 w-5 shrink-0 rtl:rotate-180" aria-hidden="true" />
              <span className={classNames("truncate", rail && "md:sr-only")}>{t("header.logout")}</span>
            </button>
          </div>
        </aside>

        {/* min-w-0: a flex item defaults to min-width:auto, so one wide child
            (a long subscription URL, a chart) would stretch this column past
            the viewport and give the whole page a horizontal scrollbar. */}
        <div className="flex min-h-screen min-w-0 flex-1 flex-col">
          {/* phone top bar: the menu button opens the drawer */}
          <header className="sticky top-2 z-30 m-2 flex items-center gap-3 rounded-xl2 border border-rapido-border bg-rapido-surface/70 px-3 py-2.5 shadow-rp-3 backdrop-blur-lg md:hidden">
            <button
              ref={opener}
              type="button"
              onClick={() => setOpen(true)}
              aria-label={String(t("rapido.nav.open"))}
              aria-expanded={drawerOpen}
              aria-controls="rapido-nav"
              className="flex h-9 w-9 items-center justify-center rounded-lg border border-rapido-border text-rapido-text hover:bg-rapido-raised"
            >
              <Bars3Icon className="h-5 w-5" />
            </button>
            <Logo className="h-6 w-6 shrink-0 text-rapido-accent" />
            <span className="text-lg font-bold">Rapido</span>
          </header>

          <main className="min-w-0 flex-1 animate-rp-fade-up">{children}</main>
        </div>
      </div>
    </div>
  );
};
