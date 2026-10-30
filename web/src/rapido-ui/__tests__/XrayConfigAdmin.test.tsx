import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18n from "locales/i18n";
import { ReactNode } from "react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { XrayConfigAdmin } from "../XrayConfigAdmin";
import { CoreSnapshots } from "../CoreSnapshots";

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
  routing_rules: [
    { inbound: ["main"], inbound_port: [20100], outbound_tag: "exit" },
    { domain_suffix: [".ir"], outbound_tag: "block" },
  ],
  dns_servers: [{ tag: "dns-a", type: "udp", address: "1.1.1.1" }],
  inbounds: [{ tag: "main", protocol: "vless", network: "tcp", header_type: "", security: "none" }],
};

const wrap = (node: ReactNode) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{node}</QueryClientProvider>
);

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  fetchMock.mockReset();
});

type Handler = (url: string, opts?: { method?: string; body?: unknown }) => unknown;

const serve = (extra: Handler = () => undefined) =>
  fetchMock.mockImplementation(async (url: string, opts?: { method?: string; body?: unknown }) => {
    const handled = extra(url, opts);
    if (handled !== undefined) return handled;
    if (url === "/settings/xray-config") return opts?.method === "PUT" ? { ...doc, ...(opts.body as object) } : doc;
    if (url === "/inbounds") return { vless: [{ tag: "main", protocol: "vless", port: 20100, ports: [20100, 20101] }] };
    if (url === "/monitoring") {
      return {
        hosts: [
          { name: "n1", tunnels: [{ name: "wg0", up: true, fallback_active: false }] },
          { name: "n2", tunnels: [{ name: "wg0", up: false, fallback_active: true }] },
        ],
      };
    }
    return {};
  });

const putBody = () => {
  const call = [...fetchMock.mock.calls].reverse().find(([url, opts]) => url === "/settings/xray-config" && opts?.method === "PUT");
  return call?.[1]?.body as Record<string, unknown[]> & Record<string, unknown>;
};

const open = async () => {
  serve();
  render(wrap(<XrayConfigAdmin />));
  await screen.findByRole("tablist");
};

const goTo = (name: RegExp | string) => userEvent.setup().click(screen.getByRole("tab", { name }));
const applyButton = () => screen.getByRole("button", { name: "Apply" });
const rowOf = (text: string) => screen.getByText(text).closest("tr") as HTMLElement;
const save = () => userEvent.setup().click(screen.getByRole("button", { name: "Save to document" }));
const code = () => screen.getByLabelText("Core Config (JSON)") as HTMLTextAreaElement;
const documentNow = () => JSON.parse(code().value);

describe("tabs", () => {
  it("has a tab for every section, with its size", async () => {
    await open();
    const tabs = screen.getAllByRole("tab").map((tab) => tab.textContent);
    expect(tabs).toEqual(["General", "Routing rules2", "Outbounds2", "Balancers1", "DNS1", "Inbounds1", "Advanced (code)"]);
  });

  it("files balancers apart from plain outbounds, numbered within their own list", async () => {
    await open();
    await goTo(/^Outbounds/);
    expect(screen.getByText("exit")).toBeInTheDocument();
    expect(screen.queryByText("fast")).toBeNull();
    await goTo(/^Balancers/);
    expect(screen.getByText("fast")).toBeInTheDocument();
    expect(within(rowOf("fast")).getByText("1")).toBeInTheDocument();
  });

  it("keeps the same document behind the forms and the code", async () => {
    await open();
    fireEvent.change(document.getElementById("core-log-level") as HTMLElement, { target: { value: "error" } });
    expect(documentNow().log_level).toBe("error");
    expect(applyButton()).toBeEnabled();

    await userEvent.setup().click(screen.getByRole("checkbox", { name: /Detect domains/ }));
    expect(documentNow().sniff_enabled).toBe(false);
  });
});

describe("general", () => {
  it("does not offer Apply until something changed", async () => {
    await open();
    expect(applyButton()).toBeDisabled();
    expect(screen.getByText("In sync with the server.")).toBeInTheDocument();
  });
});

describe("outbounds", () => {
  it("adds one through the form, refusing it until it is complete", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(screen.getByRole("button", { name: /Add outbound/ }));

    fireEvent.change(document.getElementById("core-field-type") as HTMLElement, { target: { value: "vless" } });
    await save();
    expect(screen.getByText("the server address is required.")).toBeInTheDocument();
    expect(screen.getByText("a UUID is required.")).toBeInTheDocument();
    expect(documentNow().outbounds).toHaveLength(3);

    await user.type(document.getElementById("core-field-server") as HTMLElement, "198.51.100.1");
    await user.type(document.getElementById("core-field-server_port") as HTMLElement, "۴۴۳");
    await user.type(document.getElementById("core-field-uuid") as HTMLElement, "u-1");
    await save();

    expect(screen.queryByRole("button", { name: "Save to document" })).toBeNull();
    const added = documentNow().outbounds[3];
    expect(added).toMatchObject({ tag: "outbound", type: "vless", server: "198.51.100.1", server_port: 443, uuid: "u-1" });
    expect(screen.getByText("198.51.100.1:443")).toBeInTheDocument();
    expect(applyButton()).toBeEnabled();
  });

  it("takes a duplicate tag at the field, not at Apply", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(screen.getByRole("button", { name: /Add outbound/ }));
    const tag = document.getElementById("core-field-tag") as HTMLInputElement;
    await user.clear(tag);
    await user.type(tag, "exit");
    await save();
    expect(screen.getByRole("alert").textContent).toContain("is already used");
    expect(documentNow().outbounds).toHaveLength(3);
  });

  it("edits as JSON, and the form picks the edit up", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(screen.getByText("other"));
    await user.click(screen.getByRole("radio", { name: "JSON" }));
    const box = screen.getByLabelText("Edit outbound (JSON)") as HTMLTextAreaElement;
    expect(JSON.parse(box.value)).toEqual({ tag: "other", type: "direct" });

    fireEvent.change(box, { target: { value: '{"tag":"other","type":"direct","bind_interface":"wg9"}' } });
    await user.click(screen.getByRole("radio", { name: "Form" }));
    expect((document.getElementById("core-field-bind_interface") as HTMLInputElement).value).toBe("wg9");
    await save();
    expect(documentNow().outbounds[1].bind_interface).toBe("wg9");
  });

  it("stays on the JSON tab while the JSON is broken", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(screen.getByText("other"));
    await user.click(screen.getByRole("radio", { name: "JSON" }));
    fireEvent.change(screen.getByLabelText("Edit outbound (JSON)"), { target: { value: "{" } });
    await user.click(screen.getByRole("radio", { name: "Form" }));
    expect(screen.getByLabelText("Edit outbound (JSON)")).toBeInTheDocument();
    await save();
    expect(documentNow().outbounds[1]).toEqual({ tag: "other", type: "direct" });
  });

  it("does not lose what was typed to a click outside", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(screen.getByText("other"));
    const tag = document.getElementById("core-field-tag") as HTMLInputElement;
    await user.type(tag, "-x");
    fireEvent.mouseDown(tag.closest("div.fixed") as HTMLElement);
    expect(screen.getByRole("button", { name: "Save to document" })).toBeInTheDocument();
  });

  it("renaming follows through to its rules and balancers", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(screen.getByText("exit"));
    const tag = document.getElementById("core-field-tag") as HTMLInputElement;
    await user.clear(tag);
    await user.type(tag, "renamed");
    await save();
    const next = documentNow();
    expect(next.routing_rules[0].outbound_tag).toBe("renamed");
    expect(next.outbounds[2].outbounds).toEqual(["renamed", "other"]);
  });

  it("asks before deleting one that is in use, and says where", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(within(rowOf("exit")).getByRole("button", { name: "Delete" }));
    expect(screen.getByText(/It is still used by: rule 1/)).toBeInTheDocument();
    expect(screen.getByText(/balancer fast/)).toBeInTheDocument();
    expect(documentNow().outbounds).toHaveLength(3);

    await user.click(screen.getByRole("button", { name: "Delete anyway" }));
    expect(documentNow().outbounds).toHaveLength(2);
    expect(screen.getByText(/outbound .*exit.* does not exist/)).toBeInTheDocument();
    expect(applyButton()).toBeDisabled();
  });

  it("deletes one nothing uses straight away", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Balancers/);
    await user.click(within(rowOf("fast")).getByRole("button", { name: "Delete" }));
    expect(documentNow().outbounds.map((o: { tag: string }) => o.tag)).toEqual(["exit", "other"]);
  });

  it("duplicates into the form, right after the original, under a free name", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(within(rowOf("exit")).getByRole("button", { name: "Duplicate" }));
    expect((document.getElementById("core-field-tag") as HTMLInputElement).value).toBe("exit-copy");
    expect(documentNow().outbounds).toHaveLength(3);
    await save();
    expect(documentNow().outbounds.map((o: { tag: string }) => o.tag)).toEqual(["exit", "exit-copy", "other", "fast"]);
  });

  it("shows how an interface-bound outbound's tunnel is doing across the nodes", async () => {
    await open();
    await goTo(/^Outbounds/);
    expect(await screen.findByText("1/2")).toBeInTheDocument();
  });

  it("searches across every field of a row", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.type(screen.getByRole("searchbox"), "WG0");
    expect(screen.getByText("exit")).toBeInTheDocument();
    expect(screen.queryByText("other")).toBeNull();
    await user.clear(screen.getByRole("searchbox"));
    await user.type(screen.getByRole("searchbox"), "nothing-like-this");
    expect(screen.getByText("Nothing matches this search.")).toBeInTheDocument();
  });
});

describe("balancers", () => {
  it("builds one from the outbounds, without offering itself as a member", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Balancers/);
    await user.click(screen.getByText("fast"));
    expect(screen.queryByRole("checkbox", { name: "fast" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await user.click(screen.getByRole("button", { name: /Add balancer/ }));
    await save();
    expect(screen.getByText("choose at least one member.")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "exit" }));
    await user.type(document.getElementById("core-field-interval") as HTMLElement, "2m");
    await save();
    expect(documentNow().outbounds[3]).toEqual({ tag: "balancer", type: "urltest", outbounds: ["exit"], interval: "2m" });
  });

  it("offers a selector its own members as the one to use", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Balancers/);
    await user.click(screen.getByRole("button", { name: /Add balancer/ }));
    fireEvent.change(document.getElementById("core-field-type") as HTMLElement, { target: { value: "selector" } });
    await user.click(screen.getByRole("checkbox", { name: "other" }));
    const options = within(document.getElementById("core-field-default") as HTMLElement).getAllByRole("option").map((o) => o.textContent);
    expect(options).toEqual(["None", "other"]);
  });
});

describe("routing rules", () => {
  it("adds a rule with its conditions, offering the selected inbound's ports", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Routing rules/);
    await user.click(screen.getByRole("button", { name: /Add rule/ }));
    expect(screen.queryByLabelText(/Only on these local ports/)).toBeNull();

    await user.click(screen.getByRole("checkbox", { name: "main" }));
    await user.click(screen.getByRole("button", { name: "20101" }));
    expect((document.getElementById("core-field-inbound_port") as HTMLInputElement).value).toBe("20101");

    fireEvent.change(document.getElementById("core-field-outbound_tag") as HTMLElement, { target: { value: "other" } });
    await user.type(document.getElementById("core-field-domain_keyword") as HTMLElement, "google, youtube");
    await user.type(document.getElementById("core-field-ip_cidr") as HTMLElement, "10.0.0.0/8");
    await user.click(screen.getByRole("checkbox", { name: "udp" }));
    await save();

    expect(documentNow().routing_rules[2]).toEqual({
      outbound_tag: "other",
      inbound: ["main"],
      inbound_port: [20101],
      domain_keyword: ["google", "youtube"],
      ip_cidr: ["10.0.0.0/8"],
      network: ["udp"],
    });
  });

  it("takes the source and user matchers too", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Routing rules/);
    await user.click(screen.getByRole("button", { name: /Add rule/ }));
    await user.type(document.getElementById("core-field-auth_user") as HTMLElement, "ali");
    await user.type(document.getElementById("core-field-source_ip_cidr") as HTMLElement, "192.168.0.0/16");
    await user.type(document.getElementById("core-field-source_port") as HTMLElement, "53");
    await user.type(document.getElementById("core-field-source_port_range") as HTMLElement, "1000:2000");
    await user.click(screen.getByRole("checkbox", { name: "Private source IPs" }));
    await user.type(document.getElementById("core-field-domain_regex") as HTMLElement, "^a$");
    await save();
    expect(documentNow().routing_rules[2]).toMatchObject({
      auth_user: ["ali"],
      source_ip_cidr: ["192.168.0.0/16"],
      source_port: [53],
      source_port_range: ["1000:2000"],
      source_ip_is_private: true,
      domain_regex: ["^a$"],
    });
  });

  it("says when a rule matches every connection", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Routing rules/);
    await user.click(screen.getByRole("button", { name: /Add rule/ }));
    await save();
    expect(screen.getByText("Every connection")).toBeInTheDocument();
  });

  it("refuses a bad port at the field, and keeps Save from taking it", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Routing rules/);
    await user.click(screen.getByRole("button", { name: /Add rule/ }));
    await user.type(document.getElementById("core-field-port") as HTMLElement, "70000");
    expect(screen.getByRole("alert").textContent).toMatch(/70000/);
    await save();
    expect(documentNow().routing_rules).toHaveLength(2);
  });

  it("moves rules with the buttons, disabled at the ends", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Routing rules/);
    const first = screen.getAllByRole("button", { name: "Move up" })[0];
    expect(first).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "Move down" })[1]).toBeDisabled();
    await user.click(screen.getAllByRole("button", { name: "Move down" })[0]);
    expect(documentNow().routing_rules[0].outbound_tag).toBe("block");
    expect(documentNow().routing_rules[1].outbound_tag).toBe("exit");
  });

  it("moves rules by dragging", async () => {
    await open();
    await goTo(/^Routing rules/);
    const rows = screen.getAllByRole("row").slice(1);
    const dataTransfer = { effectAllowed: "" };
    fireEvent.dragStart(rows[1], { dataTransfer });
    fireEvent.dragOver(rows[0], { dataTransfer });
    fireEvent.drop(rows[0], { dataTransfer });
    expect(documentNow().routing_rules[0].outbound_tag).toBe("block");
  });

  it("can't be reordered while filtered, since positions then mean nothing", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Routing rules/);
    await user.type(screen.getByRole("searchbox"), "block");
    expect(screen.getByRole("button", { name: "Move up" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Move down" })).toBeDisabled();
  });
});

describe("DNS", () => {
  it("shows only the fields a type uses", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^DNS/);
    await user.click(screen.getByRole("button", { name: /Add DNS server/ }));
    expect(document.getElementById("core-field-address")).not.toBeNull();
    expect(document.getElementById("core-field-path")).toBeNull();
    const type = document.getElementById("core-field-type") as HTMLElement;
    fireEvent.change(type, { target: { value: "https" } });
    expect(document.getElementById("core-field-path")).not.toBeNull();
    fireEvent.change(type, { target: { value: "local" } });
    expect(document.getElementById("core-field-address")).toBeNull();
    await save();
    expect(documentNow().dns_servers[1]).toEqual({ tag: "dns", type: "local" });
  });
});

describe("inbounds", () => {
  it("locks the tag of an inbound the server has, not of a new one", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Inbounds/);
    await user.click(screen.getByText("main"));
    expect(document.getElementById("core-field-tag")).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: /Add inbound manually/ }));
    expect(document.getElementById("core-field-tag")).toBeEnabled();
  });

  it("generates secrets, and swaps fields with the protocol", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Inbounds/);
    await user.click(screen.getByRole("button", { name: /Add inbound manually/ }));
    fireEvent.change(document.getElementById("core-field-protocol") as HTMLElement, { target: { value: "snell" } });
    expect(document.getElementById("core-field-security")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Generate" }));
    expect((document.getElementById("core-field-snell_psk") as HTMLInputElement).value).toMatch(/^[0-9a-f]{32}$/);
    await save();
    expect(documentNow().inbounds[1]).toMatchObject({ tag: "inbound", protocol: "snell", security: "none" });
  });

  it("appends generated short IDs to what is there", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Inbounds/);
    await user.click(screen.getByRole("button", { name: /Add inbound manually/ }));
    fireEvent.change(document.getElementById("core-field-security") as HTMLElement, { target: { value: "reality" } });
    const generates = () => screen.getAllByRole("button", { name: "Generate" });
    await user.click(generates()[1]);
    await user.click(generates()[1]);
    const ids = (document.getElementById("core-field-reality_short_ids") as HTMLTextAreaElement).value.split("\n");
    expect(ids).toHaveLength(2);
    expect(ids[0]).not.toBe(ids[1]);
  });

  it("holds the server-writing buttons back while there are unapplied changes", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Inbounds/);
    expect(screen.getByRole("button", { name: /Quick add/ })).toBeEnabled();
    await user.click(screen.getByText("main"));
    fireEvent.change(document.getElementById("core-field-security") as HTMLElement, { target: { value: "tls" } });
    await save();
    expect(screen.getByRole("button", { name: /Quick add/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /Add all protocols/ })).toBeDisabled();
  });

  it("warns who is affected before an Apply that removes one", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Inbounds/);
    await user.click(within(rowOf("main")).getByRole("button", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Delete anyway" }));

    await goTo(/^Routing rules/);
    await user.click(within(rowOf("exit")).getByRole("button", { name: "Delete" }));
    await user.click(applyButton());

    expect(screen.getByText(/users connected through them will be disconnected: main/)).toBeInTheDocument();
    expect(putBody()).toBeUndefined();
    await user.click(screen.getByRole("button", { name: /Yes|apply and delete/i }));
    await waitFor(() => expect(putBody()).toBeDefined());
    expect(putBody().inbounds).toEqual([]);
  });
});

describe("code", () => {
  it("edits the whole document, and shows where the caret is", async () => {
    await open();
    await goTo(/^Advanced/);
    const total = code().value.split("\n").length;
    expect(screen.getByText(`Line 1 of ${total}, column 1`)).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "routing_rules" }));
    await waitFor(() => expect(code().value.slice(0, code().selectionStart).split("\n").pop()).toBe(""));
    expect(document.activeElement).toBe(code());
  });

  it("points at a syntax error from any tab, holds Apply back, and can go to the line", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Advanced/);
    fireEvent.change(code(), { target: { value: `x${code().value}` } });
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toMatch(/1/);
    expect(applyButton()).toBeDisabled();
    expect(screen.getByTestId("json-gutter").querySelector(".text-rapido-bad")?.textContent).toBe("1");

    await goTo("General");
    expect(screen.getByRole("alert")).toBeInTheDocument();
    await user.click(within(screen.getByRole("alert")).getByRole("button", { name: "Go to line 1" }));
    await waitFor(() => expect(document.activeElement).toBe(code()));
    expect(code().selectionStart).toBe(0);
  });

  it("edits one section on its own, written back as soon as it parses", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Advanced/);
    await user.click(screen.getByRole("radio", { name: "DNS" }));
    const box = screen.getByLabelText("DNS") as HTMLTextAreaElement;
    expect(JSON.parse(box.value)).toEqual(doc.dns_servers);

    fireEvent.change(box, { target: { value: '[{"tag":"typed","type":"local"}]' } });
    expect(box.value).toBe('[{"tag":"typed","type":"local"}]');
    await goTo(/^DNS/);
    expect(screen.getByText("typed")).toBeInTheDocument();
  });

  it("holds Apply back while a section doesn't parse, or isn't a list, and says so from other tabs", async () => {
    const user = userEvent.setup();
    await open();
    fireEvent.change(document.getElementById("core-log-level") as HTMLElement, { target: { value: "error" } });
    await goTo(/^Advanced/);
    await user.click(screen.getByRole("radio", { name: "DNS" }));
    const box = screen.getByLabelText("DNS") as HTMLTextAreaElement;

    fireEvent.change(box, { target: { value: "[" } });
    expect(applyButton()).toBeDisabled();
    fireEvent.change(box, { target: { value: '{"tag":"x"}' } });
    expect(applyButton()).toBeDisabled();
    expect(screen.getByRole("alert").textContent).toContain("must be a JSON list");

    await goTo("General");
    expect(screen.getByText(/has a JSON error/)).toBeInTheDocument();
  });

  it("a form's change reaches a section's code, and Discard clears a broken draft", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Advanced/);
    await user.click(screen.getByRole("radio", { name: "DNS" }));
    await goTo(/^DNS/);
    await user.click(within(rowOf("dns-a")).getByRole("button", { name: "Delete" }));
    await goTo(/^Advanced/);
    const box = () => screen.getByLabelText("DNS") as HTMLTextAreaElement;
    expect(JSON.parse(box().value)).toEqual([]);

    fireEvent.change(box(), { target: { value: "[" } });
    expect(applyButton()).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(JSON.parse(box().value)).toEqual(doc.dns_servers);
    expect(screen.queryByText(/has a JSON error/)).toBeNull();
    expect(applyButton()).toBeDisabled();
  });
});

describe("problems in the document", () => {
  it("lists them with where they are, opens the form at one, and holds Apply back", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Advanced/);
    await user.click(screen.getByRole("radio", { name: "Routing rules" }));
    fireEvent.change(screen.getByLabelText("Routing rules"), {
      target: { value: '[{"outbound_tag":"nowhere"}]' },
    });
    const list = screen.getByRole("alert");
    expect(list.textContent).toContain("Routing rule 1");
    expect(list.textContent).toContain("nowhere");
    expect(applyButton()).toBeDisabled();

    await user.click(within(list).getByRole("button"));
    expect(screen.getByRole("heading", { name: "Edit rule" })).toBeInTheDocument();
    expect(document.getElementById("core-field-outbound_tag")).not.toBeNull();
  });

  it("marks the tab and the row that have one", async () => {
    await open();
    await goTo(/^Advanced/);
    await userEvent.setup().click(screen.getByRole("radio", { name: "Outbounds" }));
    fireEvent.change(screen.getByLabelText("Outbounds"), {
      target: { value: '[{"tag":"broken","type":"http"}]' },
    });
    const tab = screen.getByRole("tab", { name: /Outbounds/ });
    expect(within(tab).getByRole("img", { name: "This entry has a problem" })).toBeInTheDocument();
    await goTo(/^Outbounds/);
    expect(within(rowOf("broken")).getByRole("img", { name: "This entry has a problem" })).toBeInTheDocument();
  });
});

describe("Apply", () => {
  it("says what happened, and sends the whole document with every change", async () => {
    const user = userEvent.setup();
    await open();
    await goTo(/^Outbounds/);
    await user.click(screen.getByText("other"));
    await user.type(document.getElementById("core-field-bind_interface") as HTMLElement, "wg1");
    await save();
    await user.click(applyButton());

    expect(await screen.findByText(/^Applied\./)).toBeInTheDocument();
    const sent = putBody();
    expect(sent.log_level).toBe("warn");
    expect(sent.sniff_enabled).toBe(true);
    expect(sent.outbounds[1]).toEqual({ tag: "other", type: "direct", bind_interface: "wg1" });
    expect(sent.routing_rules).toEqual(doc.routing_rules);
    expect(sent.dns_servers).toEqual(doc.dns_servers);
    expect(sent.inbounds).toEqual(doc.inbounds);
  });

  it("shows the server's refusal and keeps the edit", async () => {
    const user = userEvent.setup();
    serve((url, opts) => {
      if (url === "/settings/xray-config" && opts?.method === "PUT") throw new Error("nope");
      return undefined;
    });
    render(wrap(<XrayConfigAdmin />));
    await screen.findByRole("tablist");
    fireEvent.change(document.getElementById("core-log-level") as HTMLElement, { target: { value: "error" } });
    await user.click(applyButton());
    await waitFor(() => expect(fetchMock.mock.calls.some(([url, opts]) => url === "/settings/xray-config" && opts?.method === "PUT")).toBe(true));
    expect(documentNow().log_level).toBe("error");
    expect(screen.getByText("Unapplied edits - click Apply to save them.")).toBeInTheDocument();
  });

  it("Discard goes back to the server's copy", async () => {
    const user = userEvent.setup();
    await open();
    fireEvent.change(document.getElementById("core-log-level") as HTMLElement, { target: { value: "error" } });
    await user.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(documentNow().log_level).toBe("warn");
    expect(applyButton()).toBeDisabled();
  });
});

describe("the configuration history", () => {
  const rows = [
    { id: 12, created_at: "2026-10-30T09:12:00Z", reason: "PUT /api/settings/xray-config" },
    { id: 11, created_at: "2026-10-29T18:40:00Z", reason: "POST /api/inbounds" },
    { id: 10, created_at: "2026-10-28T08:05:00Z", reason: "POST /api/core/config/backups/9/restore" },
    { id: 9, created_at: "2026-10-27T08:05:00Z", reason: "something unknown" },
  ];
  const serveHistory = (list: unknown = rows) =>
    serve((url, opts) => {
      if (url === "/core/config/backups") return list;
      if (url === "/core/config/backups/12") return { ...rows[0], config: { snapshot_version: 1, core: { log_level: "warn" } } };
      if (url === "/core/config/backups/12/restore" && opts?.method === "POST") return {};
      return undefined;
    });

  it("explains itself and only loads when opened", async () => {
    serveHistory();
    render(wrap(<CoreSnapshots />));
    expect(screen.getByText(/Right before every change/)).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
    await userEvent.setup().click(screen.getByRole("button", { name: "Show history" }));
    await screen.findByText("Before a core configuration change");
  });

  it("names what each version was saved before, with no stray placeholder characters", async () => {
    serveHistory();
    render(wrap(<CoreSnapshots />));
    await userEvent.setup().click(screen.getByRole("button", { name: "Show history" }));
    await screen.findByText("Before a core configuration change");
    expect(screen.getByText("Before an inbound change")).toBeInTheDocument();
    expect(screen.getByText("Before a restore")).toBeInTheDocument();
    expect(screen.getByText(/something unknown/)).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("?");
  });

  it("says when there is nothing yet", async () => {
    serveHistory([]);
    render(wrap(<CoreSnapshots />));
    await userEvent.setup().click(screen.getByRole("button", { name: "Show history" }));
    expect(await screen.findByText("No versions saved yet.")).toBeInTheDocument();
  });

  it("previews a version as readable JSON and restores it only after confirming", async () => {
    const user = userEvent.setup();
    serveHistory();
    render(wrap(<CoreSnapshots />));
    await user.click(screen.getByRole("button", { name: "Show history" }));
    await user.click(await screen.findByRole("button", { name: /Before a core configuration change/ }));

    const preview = (await screen.findByLabelText("Preview this version")) as HTMLTextAreaElement;
    expect(JSON.parse(preview.value).core.log_level).toBe("warn");
    expect(preview).toHaveAttribute("readonly");
    expect(screen.getByText(/connected users may be disconnected/)).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith("/restore"))).toBe(false);

    await user.click(screen.getByRole("button", { name: "Confirm restore" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/core/config/backups/12/restore", { method: "POST", body: { confirm: true } })
    );
    expect(await screen.findByText(/^Restored\./)).toBeInTheDocument();
    expect(screen.queryByLabelText("Preview this version")).toBeNull();
  });

  it("can be cancelled without restoring", async () => {
    const user = userEvent.setup();
    serveHistory();
    render(wrap(<CoreSnapshots />));
    await user.click(screen.getByRole("button", { name: "Show history" }));
    await user.click(await screen.findByRole("button", { name: /Before a core configuration change/ }));
    await screen.findByLabelText("Preview this version");

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByLabelText("Preview this version")).toBeNull();
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith("/restore"))).toBe(false);
  });

  it("shows a failed restore inside the dialog, and keeps it open", async () => {
    const user = userEvent.setup();
    serve((url, opts) => {
      if (url === "/core/config/backups") return rows;
      if (url === "/core/config/backups/12" && !opts) return { ...rows[0], config: {} };
      if (String(url).endsWith("/restore")) throw new Error("boom");
      return undefined;
    });
    render(wrap(<CoreSnapshots />));
    await user.click(screen.getByRole("button", { name: "Show history" }));
    await user.click(await screen.findByRole("button", { name: /Before a core configuration change/ }));
    await user.click(await screen.findByRole("button", { name: "Confirm restore" }));

    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByLabelText("Preview this version")).toBeInTheDocument();
  });
});

describe("removing a protocol's last inbound", () => {
  it("asks before deleting credentials, and says how many users lose them", async () => {
    const refusal = Object.assign(new Error("conflict"), {
      response: {
        _data: {
          detail: "server words",
          confirm_required: "drop_credentials",
          dropped_credentials: [{ protocol: "vmess", users: 24 }],
        },
      },
    });
    const confirmed: boolean[] = [];
    serve((url, opts) => {
      if (url !== "/settings/xray-config" || opts?.method !== "PUT") return undefined;
      const yes = (opts as { query?: { confirm_drop_credentials?: string } }).query?.confirm_drop_credentials === "true";
      confirmed.push(yes);
      if (!yes) throw refusal;
      return { ...doc, ...(opts.body as object) };
    });
    render(wrap(<XrayConfigAdmin />));
    await screen.findByRole("tablist");

    const user = userEvent.setup();
    fireEvent.change(document.getElementById("core-log-level") as HTMLElement, { target: { value: "error" } });
    await user.click(screen.getByRole("button", { name: "Apply" }));

    const alert = await screen.findByText(/lose their credentials/);
    expect(alert).toBeTruthy();
    expect(screen.getByText("vmess, users affected: 24")).toBeTruthy();
    expect(screen.queryByText("server words")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Delete the credentials and apply" }));
    await waitFor(() => expect(confirmed).toEqual([false, true]));
    await waitFor(() => expect(screen.queryByText("vmess, users affected: 24")).toBeNull());
  });
});
