import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, expect, it, vi } from "vitest";
import { NodeCreateResult } from "types/Node";

// The real create mutation against a faked POST: what is under test is that the
// one-time secrets survive the admin's clicks, and that hangs on when the
// mutation's callback fires, which a stubbed hook would not show.
const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { NodesAdmin } from "../NodesAdmin";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

const created: NodeCreateResult = {
  node: { id: 9, name: "fresh", address: "203.0.113.5", port: 62050, api_port: 62051, status: "connecting", usage_coefficient: 1 },
  setup_blob: "test-only-setup-blob",
  certificate: "test-only-cert",
  key: "test-only-key",
  ca_certificate: "test-only-ca",
  report_secret: "test-only-report-secret",
};

let finishCreate: (result: NodeCreateResult) => void;

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
    if (url === "/node" && opts?.method === "POST") {
      return new Promise((resolve) => {
        finishCreate = resolve;
      });
    }
    if (url === "/nodes") return Promise.resolve([]);
    if (url.startsWith("/nodes/usage")) return Promise.resolve({ usages: [] });
    if (url === "/tunnels") return Promise.resolve([]);
    if (url === "/monitoring") return Promise.resolve({ generated_at: "", hosts: [] });
    return Promise.resolve({});
  });
});

const overlay = () => document.querySelector(".fixed.inset-0") as HTMLElement;

const startCreate = () => {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <NodesAdmin />
    </QueryClientProvider>
  );
  fireEvent.click(screen.getByRole("button", { name: /Add New Rapido Node/ }));
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "fresh" } });
  fireEvent.change(screen.getByLabelText("Address"), { target: { value: "203.0.113.5" } });
  fireEvent.click(screen.getByRole("button", { name: "Add Node" }));
};

it("does not let a click outside close the form while the create is in flight, which would lose the secrets", async () => {
  startCreate();
  await waitFor(() => expect(screen.getByRole("button", { name: "Please wait..." })).toBeDisabled());

  fireEvent.mouseDown(overlay());
  expect(screen.getByLabelText("Name")).toBeInTheDocument();

  finishCreate(created);
  expect(await screen.findByLabelText("Setup blob")).toHaveValue("test-only-setup-blob");
});

it("does not let a click outside close the panel that shows them, only its own button", async () => {
  startCreate();
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/node", expect.anything()));
  finishCreate(created);
  await screen.findByLabelText("Setup blob");

  fireEvent.mouseDown(overlay());
  expect(screen.getByLabelText("Setup blob")).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  expect(screen.queryByLabelText("Setup blob")).not.toBeInTheDocument();
});

it("still closes the form on a click outside when nothing is in flight", () => {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <NodesAdmin />
    </QueryClientProvider>
  );
  fireEvent.click(screen.getByRole("button", { name: /Add New Rapido Node/ }));
  fireEvent.mouseDown(overlay());
  expect(screen.queryByLabelText("Name")).not.toBeInTheDocument();
});
