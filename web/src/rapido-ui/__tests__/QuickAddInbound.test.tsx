import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { CreateInboundPayload } from "types/Inbound";

const createMutate = vi.fn();
const createMutateAsync = vi.fn();

vi.mock("hooks/useInboundsQuery", () => ({
  useCreateInboundMutation: () => ({
    mutate: createMutate,
    mutateAsync: createMutateAsync,
    isPending: false,
  }),
}));

import { AddAllProtocolsButton, QuickAddInboundModal } from "../QuickAddInbound";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  createMutate.mockReset();
  createMutateAsync.mockReset();
});

const click = (el: HTMLElement) => fireEvent.click(el);
const enter = (el: HTMLElement, value: string) => fireEvent.change(el, { target: { value } });

describe("QuickAddInboundModal", () => {
  it("defaults to vmess with a matching tag and TLS security", () => {
    render(<QuickAddInboundModal onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(screen.getByLabelText("Protocol")).toHaveValue("vmess");
    expect(screen.getByLabelText("Tag")).toHaveValue("vmess-main");
    expect(screen.getByLabelText(/^Security/)).toHaveValue("tls");
  });

  it("switching to a protocol updates the auto-filled tag and security together", () => {
    render(<QuickAddInboundModal onClose={vi.fn()} onCreated={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Protocol"), { target: { value: "shadowsocks" } });
    expect(screen.getByLabelText("Tag")).toHaveValue("shadowsocks-main");
    expect(screen.getByLabelText(/^Security/)).toHaveValue("none");
  });

  it("does not overwrite a tag the admin has already customized when switching protocol", () => {
    render(<QuickAddInboundModal onClose={vi.fn()} onCreated={vi.fn()} />);
    enter(screen.getByLabelText("Tag"), "my-own-tag");
    fireEvent.change(screen.getByLabelText("Protocol"), { target: { value: "trojan" } });
    expect(screen.getByLabelText("Tag")).toHaveValue("my-own-tag");
  });

  it("locks the security field for a TLS-mandatory protocol", () => {
    render(<QuickAddInboundModal onClose={vi.fn()} onCreated={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Protocol"), { target: { value: "hysteria2" } });
    const security = screen.getByLabelText(/^Security/);
    expect(security).toHaveValue("tls");
    expect(security).toBeDisabled();
  });

  it("locks the security field to none for snell (no TLS concept at all)", () => {
    render(<QuickAddInboundModal onClose={vi.fn()} onCreated={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Protocol"), { target: { value: "snell" } });
    const security = screen.getByLabelText(/^Security/);
    expect(security).toHaveValue("none");
    expect(security).toBeDisabled();
  });

  it("disables submit until a valid port is entered", () => {
    render(<QuickAddInboundModal onClose={vi.fn()} onCreated={vi.fn()} />);
    const submit = screen.getByRole("button", { name: "Add inbound" });
    expect(submit).toBeDisabled();
    enter(screen.getByLabelText("Port"), "20300");
    expect(submit).not.toBeDisabled();
  });

  it("submits tag/protocol/port/security exactly, with no certificate or PSK field at all", () => {
    render(<QuickAddInboundModal onClose={vi.fn()} onCreated={vi.fn()} />);
    enter(screen.getByLabelText("Port"), "20300");
    click(screen.getByRole("button", { name: "Add inbound" }));

    const sent = createMutate.mock.calls[0][0] as CreateInboundPayload;
    expect(sent).toEqual({ tag: "vmess-main", protocol: "vmess", port: 20300, security: "tls" });
  });

  it("shows the server's error and keeps the form open on failure", async () => {
    createMutate.mockImplementation((_body, opts) =>
      opts.onError({ response: { _data: { detail: "tag already exists" } } })
    );
    render(<QuickAddInboundModal onClose={vi.fn()} onCreated={vi.fn()} />);
    enter(screen.getByLabelText("Port"), "20300");
    click(screen.getByRole("button", { name: "Add inbound" }));

    expect(await screen.findByText("tag already exists")).toBeInTheDocument();
    expect(screen.getByLabelText("Tag")).toBeInTheDocument();
  });
});

describe("AddAllProtocolsButton", () => {
  it("creates all 8 protocols sequentially on distinct ports and reports the count", async () => {
    createMutateAsync.mockResolvedValue({});
    render(<AddAllProtocolsButton onDone={vi.fn()} />);
    click(screen.getByRole("button", { name: /Add all protocols/ }));

    await waitFor(() => expect(createMutateAsync).toHaveBeenCalledTimes(8));

    const ports = createMutateAsync.mock.calls.map((c) => (c[0] as CreateInboundPayload).port);
    expect(new Set(ports).size).toBe(8); // every port distinct
    expect(ports).toEqual([...ports].sort((a, b) => a - b)); // sequential, not out of order

    expect(await screen.findByText(/8 created/)).toBeInTheDocument();
  });

  it("reports per-protocol failures without stopping the rest", async () => {
    createMutateAsync.mockImplementation((body: CreateInboundPayload) =>
      body.protocol === "tuic"
        ? Promise.reject({ response: { _data: { detail: "port in use" } } })
        : Promise.resolve({})
    );
    render(<AddAllProtocolsButton onDone={vi.fn()} />);
    click(screen.getByRole("button", { name: /Add all protocols/ }));

    await waitFor(() => expect(createMutateAsync).toHaveBeenCalledTimes(8));
    expect(await screen.findByText(/7 created/)).toBeInTheDocument();
    expect(screen.getByText(/port in use/)).toBeInTheDocument();
  });

  it("calls onDone only when at least one protocol was actually created", async () => {
    createMutateAsync.mockRejectedValue({ response: { _data: { detail: "boom" } } });
    const onDone = vi.fn();
    render(<AddAllProtocolsButton onDone={onDone} />);
    click(screen.getByRole("button", { name: /Add all protocols/ }));

    await waitFor(() => expect(createMutateAsync).toHaveBeenCalledTimes(8));
    expect(onDone).not.toHaveBeenCalled();
  });
});
