import { ComponentType, FC, LazyExoticComponent } from "react";

/**
 * A tiny registry for optional add-ons. If a `private/index.ts(x)` module exists next to
 * this file it is picked up at build time and may contribute navigation entries, routes,
 * translations and small pieces of UI to a few named slots. When there is no such module
 * the registry is empty and the panel is exactly the public one: nothing here depends on
 * an add-on being present.
 */

export type ExtensionNavItem = {
  key: string;
  href: string;
  labelKey: string;
  icon: FC<{ className?: string }>;
  sudoOnly?: boolean;
  /** Place the entry right after this built-in nav key; appended at the end when omitted. */
  after?: string;
};

export type ExtensionRoute = {
  path: string;
  sudoOnly?: boolean;
  component: LazyExoticComponent<ComponentType>;
};

/** Slot: rendered at the bottom of the node zone on a tunnel card. */
export type TunnelNodeSlotProps = { tunnelId: number; nodeId: number; nodeName: string };

/** Slot: rendered at the bottom of the links zone on a node card. */
export type NodeLinksSlotProps = {
  nodeId: number | null;
  /** The first tunnel that ends on this node, when there is one. */
  tunnelId: number | null;
  links: { total: number; down: number };
};

/** A slot component may be lazy, so an add-on can keep its code out of the main bundle. */
export type SlotComponent<P> = ComponentType<P> | LazyExoticComponent<ComponentType<P>>;

export type Extension = {
  navItems?: ExtensionNavItem[];
  routes?: ExtensionRoute[];
  /** Extra translation keys per language code (flat dotted keys, merged over the bundled ones). */
  locales?: Record<string, Record<string, string>>;
  slots?: {
    tunnelNodeFooter?: SlotComponent<TunnelNodeSlotProps>;
    nodeLinksFooter?: SlotComponent<NodeLinksSlotProps>;
  };
};

type Loaded = { default?: Extension };

// Zero matches (the add-on folder is absent) is a valid, empty result.
const modules = import.meta.glob<Loaded>("./private/index.{ts,tsx}", { eager: true });

export const extensions: Extension[] = Object.values(modules)
  .map((m) => m?.default)
  .filter((e): e is Extension => !!e);

export const extensionNavItems = (list: Extension[] = extensions): ExtensionNavItem[] =>
  list.flatMap((e) => e.navItems ?? []);

export const extensionRoutes = (list: Extension[] = extensions): ExtensionRoute[] =>
  list.flatMap((e) => e.routes ?? []);

export const extensionLocales = (lang: string, list: Extension[] = extensions): Record<string, string> =>
  Object.assign({}, ...list.map((e) => e.locales?.[lang] ?? {}));

export const extensionSlots = <K extends keyof NonNullable<Extension["slots"]>>(
  name: K,
  list: Extension[] = extensions
): NonNullable<NonNullable<Extension["slots"]>[K]>[] =>
  list.map((e) => e.slots?.[name]).filter(Boolean) as NonNullable<NonNullable<Extension["slots"]>[K]>[];

/** Insert the add-ons' nav entries into a built-in list. */
export const mergeNavItems = <T extends { key: string }>(base: T[], extra: (T & { after?: string })[]): T[] => {
  const out = [...base];
  for (const item of extra) {
    const at = item.after ? out.findIndex((i) => i.key === item.after) : -1;
    if (at >= 0) out.splice(at + 1, 0, item);
    else out.push(item);
  }
  return out;
};
