import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { IntegrationsForm } from "../IntegrationsForm";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({ telegram_enabled: false, telegram_admin_ids: [], webhook_addresses: [] });
});

const puts = () => fetchMock.mock.calls.filter((c) => (c[1] as { method?: string } | undefined)?.method === "PUT");
const channelField = () => screen.getAllByPlaceholderText(i18n.t("rapido.integrations.newValue") as string)[2];

describe("saving integration settings", () => {
  it("sends nothing and names the field when a number field holds no number", async () => {
    render(<IntegrationsForm />);
    await screen.findByText(i18n.t("rapido.integrations.telegram") as string);
    fireEvent.change(channelField(), { target: { value: "abc" } });
    fireEvent.click(screen.getByRole("button", { name: i18n.t("rapido.integrations.save") as string }));
    expect(puts()).toEqual([]);
    expect(screen.getByText(/These fields need whole numbers/)).toHaveTextContent(i18n.t("rapido.integrations.telegramLoggerChannelId") as string);
  });

  it("sends a number typed on a Persian keyboard", async () => {
    render(<IntegrationsForm />);
    await screen.findByText(i18n.t("rapido.integrations.telegram") as string);
    fireEvent.change(channelField(), { target: { value: "-۱۰۰۱" } });
    fireEvent.click(screen.getByRole("button", { name: i18n.t("rapido.integrations.save") as string }));
    await waitFor(() => expect(puts()).toHaveLength(1));
    expect(puts()[0][1].body).toEqual({ telegram_logger_channel_id: -1001 });
  });
});
