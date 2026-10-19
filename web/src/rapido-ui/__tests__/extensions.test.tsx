import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  Extension,
  extensionLocales,
  extensionNavItems,
  extensionRoutes,
  extensionSlots,
  mergeNavItems,
} from "../../extensions";

const Icon = () => null;
const Chip = ({ nodeId }: { nodeId: number | null }) => <span>chip {String(nodeId)}</span>;
const a: Extension = {
  navItems: [{ key: "extra", href: "/extra/", labelKey: "x.nav", icon: Icon, after: "tunnels" }],
  locales: { en: { "x.nav": "Extra" }, fa: { "x.nav": "اضافه" } },
  slots: { nodeLinksFooter: Chip as never },
};
const b: Extension = { locales: { en: { "x.other": "Other" } } };

describe("extension registry", () => {
  it("is empty and harmless for an empty list", () => {
    expect(extensionNavItems([])).toEqual([]);
    expect(extensionRoutes([])).toEqual([]);
    expect(extensionLocales("en", [])).toEqual({});
    expect(extensionSlots("tunnelNodeFooter", [])).toEqual([]);
  });

  it("collects nav items, merged locale keys per language and slot components", () => {
    expect(extensionNavItems([a, b]).map((i) => i.key)).toEqual(["extra"]);
    expect(extensionLocales("en", [a, b])).toEqual({ "x.nav": "Extra", "x.other": "Other" });
    expect(extensionLocales("ru", [a, b])).toEqual({});
    const slots = extensionSlots("nodeLinksFooter", [a, b]);
    expect(slots).toHaveLength(1);
    const Slot = slots[0] as unknown as typeof Chip;
    render(<Slot nodeId={7} />);
    expect(screen.getByText("chip 7")).toBeInTheDocument();
  });

  it("places a nav entry after the named built-in one, or at the end", () => {
    const base = [{ key: "overview" }, { key: "tunnels" }, { key: "logs" }];
    expect(mergeNavItems(base, [{ key: "extra", after: "tunnels" }]).map((i) => i.key)).toEqual(["overview", "tunnels", "extra", "logs"]);
    expect(mergeNavItems(base, [{ key: "extra" }]).map((i) => i.key)).toEqual(["overview", "tunnels", "logs", "extra"]);
    expect(mergeNavItems(base, [{ key: "extra", after: "nope" }]).map((i) => i.key)).toEqual(["overview", "tunnels", "logs", "extra"]);
  });
});
