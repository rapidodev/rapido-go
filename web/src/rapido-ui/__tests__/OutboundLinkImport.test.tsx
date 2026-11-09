import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18n from "locales/i18n";
import { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { XrayConfigAdmin } from "../XrayConfigAdmin";

// The share-link box on the outbound dialog's JSON tab, driven through the real Core Config page.

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock, $fetch: fetchMock }));

const doc = {
  log_level: "warn",
  sniff_enabled: true,
  outbounds: [
    { tag: "exit", type: "direct", bind_interface: "wg0" },
    { tag: "other", type: "direct" },
    { tag: "fast", type: "urltest", outbounds: ["exit", "other"] },
  ],
  routing_rules: [{ domain_suffix: [".ir"], outbound_tag: "block" }],
  dns_servers: [{ tag: "dns-a", type: "udp", address: "1.1.1.1" }],
  inbounds: [{ tag: "main", protocol: "vless", network: "tcp", header_type: "", security: "none" }],
};

const UUID = "1b0f8d6e-3c2a-4e5f-9a7b-0c1d2e3f4a5b";
const VLESS = `vless://${UUID}@de.example.com:443?security=tls&sni=sni.example.com&fp=chrome&flow=xtls-rprx-vision&type=tcp#Germany%201`;

const wrap = (node: ReactNode) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{node}</QueryClientProvider>
);

// Reset before each test, not after one: after it the dialog is still mounted, and a language
// change would re-render it outside act.
beforeEach(async () => {
  await i18n.changeLanguage("en");
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (url: string, opts?: { method?: string; body?: unknown }) => {
    if (url === "/settings/xray-config") return opts?.method === "PUT" ? { ...doc, ...(opts.body as object) } : doc;
    if (url === "/inbounds") return { vless: [{ tag: "main", protocol: "vless", port: 20100, ports: [20100] }] };
    if (url === "/monitoring") return { hosts: [] };
    if (url === "/nodes") return [];
    return {};
  });
});

const open = async () => {
  render(wrap(<XrayConfigAdmin />));
  await screen.findByRole("tablist");
};

const goTo = (name: RegExp) => userEvent.setup().click(screen.getByRole("tab", { name }));
const jsonTab = () => userEvent.setup().click(screen.getByRole("radio", { name: "JSON" }));
const linkBox = () => screen.getByLabelText("Link") as HTMLInputElement;
const fillButton = () => screen.getByRole("button", { name: "Fill in from the link" });
const editor = (title: string) => screen.getByLabelText(`${title} (JSON)`) as HTMLTextAreaElement;
const documentNow = () => JSON.parse((screen.getByLabelText("Core Config (JSON)") as HTMLTextAreaElement).value);

const pasteLink = async (link: string) => {
  const user = userEvent.setup();
  await user.click(linkBox());
  await user.paste(link);
  return user;
};

describe("filling an outbound from a share link", () => {
  it("puts the link's outbound in the editor under the dialog's tag, and Save takes it", async () => {
    await open();
    await goTo(/^Outbounds/);
    await userEvent.setup().click(screen.getByRole("button", { name: /Add outbound/ }));
    await jsonTab();
    expect(linkBox().placeholder).toBe("vmess:// vless:// trojan:// ss:// hysteria2://");

    const user = await pasteLink(VLESS);
    await user.click(fillButton());

    expect(JSON.parse(editor("Add outbound").value)).toEqual({
      tag: "outbound",
      type: "vless",
      server: "de.example.com",
      server_port: 443,
      uuid: UUID,
      flow: "xtls-rprx-vision",
      tls_enabled: true,
      tls_server_name: "sni.example.com",
    });
    expect(screen.getByRole("status").textContent).toContain("Germany 1");
    expect(screen.getByText(/Left out, as the outbound has no field for them/).textContent).toContain("fp");
    // The credentials are in the editor now; the box doesn't keep a second copy.
    expect(linkBox().value).toBe("");

    await user.click(screen.getByRole("button", { name: "Save to document" }));
    expect(documentNow().outbounds[3]).toMatchObject({ tag: "outbound", type: "vless", uuid: UUID, tls_server_name: "sni.example.com" });
  });

  it("keeps the tag of the outbound being edited, even one just typed into the JSON", async () => {
    await open();
    await goTo(/^Outbounds/);
    await userEvent.setup().click(screen.getByText("other"));
    await jsonTab();
    fireEvent.change(editor("Edit outbound"), { target: { value: '{"tag":"renamed","type":"direct"}' } });

    const user = await pasteLink("hy2://secret@[2001:db8::2]/?sni=s.example");
    await user.keyboard("{Enter}");

    expect(JSON.parse(editor("Edit outbound").value)).toEqual({
      tag: "renamed",
      type: "hysteria2",
      server: "2001:db8::2",
      server_port: 443,
      password: "secret",
      tls_server_name: "s.example",
    });
  });

  it("says why a link can't be used, and leaves the editor as it was", async () => {
    await open();
    await goTo(/^Outbounds/);
    await userEvent.setup().click(screen.getByText("other"));
    await jsonTab();
    const before = editor("Edit outbound").value;

    const user = await pasteLink(`vless://${UUID}@h.example:443?security=reality&pbk=key&sid=01`);
    await user.click(fillButton());
    expect(screen.getByRole("alert").textContent).toBe("REALITY isn't supported on outbounds yet.");
    expect(linkBox()).toHaveAttribute("aria-invalid", "true");
    expect(editor("Edit outbound").value).toBe(before);
    expect(screen.queryByRole("status")).toBeNull();

    await user.clear(linkBox());
    await user.paste(`vless://${UUID}@h.example:443?type=ws&path=%2F`);
    await user.click(fillButton());
    // The value is isolated left-to-right, for the Persian UI.
    expect(screen.getByRole("alert").textContent).toBe("The ⁦ws⁩ transport isn't supported: the outbound connects over plain TCP.");
    expect(editor("Edit outbound").value).toBe(before);

    await user.clear(linkBox());
    await user.click(fillButton());
    expect(screen.getByRole("alert").textContent).toBe("Paste a share link first.");
    expect(editor("Edit outbound").value).toBe(before);
  });

  it("is labelled in Persian on the Persian panel", async () => {
    await i18n.changeLanguage("fa");
    await open();
    await userEvent.setup().click(screen.getByRole("tab", { name: /^خروجی/ }));
    await userEvent.setup().click(screen.getByText("other"));
    await userEvent.setup().click(screen.getByRole("radio", { name: "JSON" }));
    expect(screen.getByLabelText("لینک")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "پر کردن از روی لینک" })).toBeInTheDocument();
  });
});

describe("where the link box is offered", () => {
  it("only on a plain outbound's JSON tab", async () => {
    await open();
    await goTo(/^Outbounds/);
    await userEvent.setup().click(screen.getByText("other"));
    expect(screen.queryByLabelText("Link")).toBeNull();
    await jsonTab();
    expect(linkBox()).toBeInTheDocument();
  });

  it.each([
    [/^Balancers/, "fast"],
    [/^DNS/, "dns-a"],
    [/^Inbounds/, "main"],
  ])("not for an item under %s", async (tab, row) => {
    await open();
    await goTo(tab);
    await userEvent.setup().click(screen.getByText(row));
    await jsonTab();
    expect(screen.getByRole("button", { name: "Save to document" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Link")).toBeNull();
  });

  it("not for a routing rule", async () => {
    await open();
    await goTo(/^Routing rules/);
    const table = screen.getByRole("table", { name: "Routing rules" });
    await userEvent.setup().click(within(table).getByRole("button", { name: "Edit" }));
    await jsonTab();
    expect(screen.getByRole("button", { name: "Save to document" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Link")).toBeNull();
  });
});
