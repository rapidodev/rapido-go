import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { Host, HostsMap } from "types/Host";
import { queryKeys } from "utils/queryClient";
import { emptyHost } from "../hostsReducers";

// The real hosts query against a faked network: what is under test is what a
// refetch does to the edit buffer, and that hangs on react-query handing back
// the same object for the same answer, which a stubbed hook would not do.
const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { HostsAdmin } from "../HostsAdmin";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

const host = (remark: string, over: Partial<Host> = {}): Host => ({ ...emptyHost(0), id: 1, remark, address: "a.example.com", port: 443, ...over });
const map = (remark: string): HostsMap => ({ "vless-in": [host(remark)] });

let server: HostsMap;
let failReads = false;

beforeEach(() => {
  server = map("server copy");
  failReads = false;
  fetchMock.mockReset();
  fetchMock.mockImplementation((url: string, opts?: { method?: string; body?: HostsMap }) => {
    if (url === "/hosts" && opts?.method === "PUT") {
      server = opts.body as HostsMap;
      return Promise.resolve(server);
    }
    if (url === "/hosts") return failReads ? Promise.reject(new TypeError("Failed to fetch")) : Promise.resolve(server);
    if (url === "/inbounds") return Promise.resolve({ vless: ["vless-in"] });
    return Promise.resolve({ capacity: 0, indicator: false, sort_by_load: false, updated_at: "", hosts: [] });
  });
});

const renderPage = async () => {
  const client = new QueryClient({ defaultOptions: { queries: { refetchOnWindowFocus: false } } });
  render(
    <QueryClientProvider client={client}>
      <HostsAdmin />
    </QueryClientProvider>
  );
  await screen.findByDisplayValue("server copy");
  return client;
};

const remarkField = () => screen.getByLabelText("Remark") as HTMLInputElement;
const edit = (value: string) => fireEvent.change(remarkField(), { target: { value } });
const refetchFromServer = async (client: QueryClient) => {
  await act(async () => {
    await client.refetchQueries({ queryKey: queryKeys.hosts });
  });
};

describe("backing out of an edit", () => {
  it("Discard brings the server's copy back, even when the refetch answers exactly the same", async () => {
    await renderPage();
    edit("my edit");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));

    expect(remarkField()).toHaveValue("server copy");
    expect(screen.queryByText("Unsaved changes")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Discard changes" })).not.toBeInTheDocument();
  });

  it("Discard also closes a save confirmation that was open", async () => {
    await renderPage();
    edit("my edit");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(screen.queryByText(i18n.t("rapido.hosts.saveWarning"))).not.toBeInTheDocument();
  });
});

describe("a refetch while an admin is editing", () => {
  it("does not overwrite the edit, says the hosts changed, and Discard takes the newer copy", async () => {
    const client = await renderPage();
    edit("my edit");

    server = map("someone else's change");
    await refetchFromServer(client);

    expect(await screen.findByRole("status")).toHaveTextContent("The hosts were changed elsewhere");
    expect(remarkField()).toHaveValue("my edit");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(remarkField()).toHaveValue("someone else's change");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("does not take the list and the edit off the page when it fails", async () => {
    const client = await renderPage();
    edit("my edit");

    failReads = true;
    await refetchFromServer(client);
    await waitFor(() => expect(client.getQueryState(queryKeys.hosts)?.status).toBe("error"));

    expect(remarkField()).toHaveValue("my edit");
    expect(screen.queryByText("Could not load hosts.")).not.toBeInTheDocument();
  });

  it("does show a newer copy when there is nothing unsaved", async () => {
    const client = await renderPage();
    server = map("someone else's change");
    await refetchFromServer(client);
    expect(await screen.findByDisplayValue("someone else's change")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});

describe("saving", () => {
  it("sends the edit, keeps saying it was saved, and is clean afterwards", async () => {
    await renderPage();
    edit("my edit");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    fireEvent.click(screen.getByRole("button", { name: i18n.t("rapido.hosts.saveConfirm") }));

    expect(await screen.findByText(i18n.t("rapido.hosts.saved"))).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Unsaved changes")).not.toBeInTheDocument());
    expect(server["vless-in"][0].remark).toBe("my edit");
    expect(remarkField()).toHaveValue("my edit");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    // Still there once the cache's copy of the answer has come through.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(screen.getByText(i18n.t("rapido.hosts.saved"))).toBeInTheDocument();
  });
});

describe("the port field", () => {
  const portField = () => screen.getByLabelText("Port") as HTMLInputElement;

  it("reads a Persian keyboard's digits instead of turning them into NaN", async () => {
    await renderPage();
    fireEvent.change(portField(), { target: { value: "۸۴۴۳" } });
    expect(portField()).toHaveValue("8443");
  });

  it("ignores anything that is not a digit, and empties to no port", async () => {
    await renderPage();
    fireEvent.change(portField(), { target: { value: "44a3" } });
    expect(portField()).toHaveValue("443");
    fireEvent.change(portField(), { target: { value: "" } });
    expect(portField()).toHaveValue("");
  });
});
