import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18n from "locales/i18n";
import { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { XrayConfigAdmin } from "../XrayConfigAdmin";

// Testing an outbound before it is saved, through the real Core Config page: the dialog's Test
// sends what it holds as a draft, and the table tests a row by tag only while it is exactly what
// was last saved.

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock, $fetch: fetchMock }));

const UUID = "1b0f8d6e-3c2a-4e5f-9a7b-0c1d2e3f4a5b";
const PBK = "tNusmlSozuXV95B7bOpp7J_vPBWfCis3SChNyuEL9BA";
const REALITY_LINK = `vless://${UUID}@203.0.113.9:443?flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=${PBK}&sid=0123abcd&spx=%2F#NL`;

const doc = {
  log_level: "warn",
  sniff_enabled: true,
  outbounds: [
    { tag: "exit", type: "direct", bind_interface: "wg0", direct_fallback: true },
    { tag: "other", type: "direct" },
  ],
  routing_rules: [],
  dns_servers: [],
  inbounds: [{ tag: "main", protocol: "vless", network: "tcp", header_type: "", security: "none" }],
};

type TestBody = { tags?: string[]; outbounds?: Record<string, unknown>[]; node_id?: number };
let testBodies: TestBody[] = [];

const answer = (body: TestBody) => ({
  url: "https://www.gstatic.com/generate_204",
  nodes: [{ id: 1, name: "de-1" }],
  results: [
    ...(body.tags ?? []).map((tag) => ({ node_id: 1, node: "de-1", tag, delay_ms: 74, status_code: 204, interface_here: false, fallback: false })),
    ...(body.outbounds ?? []).map((o) => ({
      node_id: 1,
      node: "de-1",
      tag: o.tag,
      delay_ms: 212,
      status_code: 204,
      interface_here: false,
      fallback: false,
      draft: true,
    })),
  ],
});

const wrap = (node: ReactNode) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{node}</QueryClientProvider>
);

beforeEach(async () => {
  await i18n.changeLanguage("en");
  testBodies = [];
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (url: string, opts?: { method?: string; body?: unknown }) => {
    if (url === "/settings/xray-config") return doc;
    if (url === "/inbounds") return { vless: [{ tag: "main", protocol: "vless", port: 20100, ports: [20100] }] };
    if (url === "/monitoring") return { hosts: [] };
    if (url === "/nodes") return [{ id: 1, name: "de-1", status: "connected" }];
    if (url === "/core/outbounds/test") {
      testBodies.push(opts?.body as TestBody);
      return answer(opts?.body as TestBody);
    }
    return {};
  });
});

const openOutbounds = async () => {
  render(wrap(<XrayConfigAdmin />));
  await screen.findByRole("tablist");
  await userEvent.setup().click(screen.getByRole("tab", { name: /^Outbounds/ }));
};

// The dialog is the part of the page around its Save button.
const dialog = () => screen.getByRole("button", { name: "Save to document" }).closest(".fixed, [class*='max-w-3xl']") as HTMLElement;

describe("the outbound dialog's Test", () => {
  it("pastes a link, tests the draft and shows the time, then saves it", async () => {
    await openOutbounds();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Add outbound/ }));
    await user.click(screen.getByRole("radio", { name: "JSON" }));
    await user.click(screen.getByLabelText("Link"));
    await user.paste(REALITY_LINK);
    await user.click(screen.getByRole("button", { name: "Fill in from the link" }));
    expect(screen.getByText(/spiderX/)).toBeInTheDocument();

    await user.click(within(dialog()).getByRole("button", { name: "Test" }));
    const result = await screen.findByTestId("draft-test-result");
    await waitFor(() => expect(result.textContent).toContain("212ms (204)"));
    expect(result.textContent).toContain("draft");
    expect(testBodies).toHaveLength(1);
    expect(testBodies[0].tags).toBeUndefined();
    expect(testBodies[0].outbounds).toEqual([
      {
        tag: "outbound",
        type: "vless",
        server: "203.0.113.9",
        server_port: 443,
        uuid: UUID,
        flow: "xtls-rprx-vision",
        tls_enabled: true,
        tls_server_name: "www.example.com",
        tls_fingerprint: "chrome",
        reality_public_key: PBK,
        reality_short_id: "0123abcd",
      },
    ]);

    await user.click(within(dialog()).getByRole("button", { name: "Save to document" }));
    expect(screen.queryByRole("button", { name: "Save to document" })).toBeNull();
  });

  it("tests from the form too, on the node picked, and holds back what saving would refuse", async () => {
    await openOutbounds();
    const user = userEvent.setup();
    await user.click(screen.getByText("other"));
    const nodeSelect = within(dialog()).getByLabelText("Test from");
    await user.selectOptions(nodeSelect, "1");

    await user.click(within(dialog()).getByRole("button", { name: "Test" }));
    const result = await screen.findByTestId("draft-test-result");
    await waitFor(() => expect(result.textContent).toContain("212ms"));
    expect(testBodies[0]).toEqual({ outbounds: [{ tag: "other", type: "direct" }], node_id: 1 });

    // A draft the server would refuse is not sent; the dialog says what is wrong instead.
    await user.click(screen.getByRole("radio", { name: "JSON" }));
    fireEvent.change(screen.getByLabelText("Edit outbound (JSON)"), {
      target: { value: JSON.stringify({ tag: "other", type: "vless", server: "h", server_port: 443, uuid: "u", reality_public_key: "short" }) },
    });
    await user.click(within(dialog()).getByRole("button", { name: "Test" }));
    expect(within(dialog()).getByText(/REALITY public key must be 43 characters/)).toBeInTheDocument();
    expect(testBodies).toHaveLength(1);
  });
});

describe("the Outbounds table's tests", () => {
  it("tests a row as saved by tag, and a changed one as a draft, marked so", async () => {
    await openOutbounds();
    const user = userEvent.setup();
    const table = screen.getByRole("table", { name: "Outbounds" });
    await user.click(screen.getByRole("button", { name: "Test all" }));
    await waitFor(() => expect(within(table).getAllByText("74ms (204)")).toHaveLength(2));
    expect(testBodies[0]).toEqual({ tags: ["exit", "other"] });
    expect(within(table).queryByText("draft")).toBeNull();

    // Change "other" in its dialog: it no longer is what the nodes run.
    await user.click(screen.getByText("other"));
    await user.click(screen.getByRole("radio", { name: "JSON" }));
    fireEvent.change(screen.getByLabelText("Edit outbound (JSON)"), {
      target: { value: JSON.stringify({ tag: "other", type: "direct", bind_interface: "wg9" }) },
    });
    await user.click(within(dialog()).getByRole("button", { name: "Save to document" }));

    await user.click(screen.getByRole("button", { name: "Test all" }));
    await waitFor(() => expect(testBodies).toHaveLength(2));
    expect(testBodies[1]).toEqual({ tags: ["exit"], outbounds: [{ tag: "other", type: "direct", bind_interface: "wg9" }] });
    await waitFor(() => expect(within(table).getByText("212ms (204)")).toBeInTheDocument());
    expect(within(table).getAllByText("draft")).toHaveLength(1);
  });
});
