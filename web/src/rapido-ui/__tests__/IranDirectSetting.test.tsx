import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { IranDirectSetting } from "../IranDirectSetting";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

const status = (on: boolean) => ({
  telegram_enabled: false,
  telegram_admin_ids: [],
  webhook_addresses: [],
  reseller_api_enabled: false,
  subscription_iran_direct: on,
});

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockImplementation((_url: string, opts?: { method?: string; body?: { subscription_iran_direct: boolean | null } }) => {
    if (opts?.method === "PUT") {
      // null goes back to the environment's value, which is on.
      return Promise.resolve(status(opts.body?.subscription_iran_direct ?? true));
    }
    return Promise.resolve(status(true));
  });
});

const puts = () => fetchMock.mock.calls.filter((c) => (c[1] as { method?: string } | undefined)?.method === "PUT");
const toggle = () => screen.getByRole("checkbox", { name: i18n.t("rapido.iranDirect.label") as string });

describe("the send-Iranian-traffic-direct setting", () => {
  it("shows the saved value once it has loaded", async () => {
    render(<IranDirectSetting />);
    await waitFor(() => expect(toggle()).toBeEnabled());
    expect(toggle()).toBeChecked();
    expect(screen.getByText(i18n.t("rapido.integrations.enabled") as string)).toBeInTheDocument();
  });

  it("saves only its own key the moment it is switched", async () => {
    render(<IranDirectSetting />);
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());
    await waitFor(() => expect(puts()).toHaveLength(1));
    expect(puts()[0][0]).toBe("/settings/integrations");
    expect(puts()[0][1].body).toEqual({ subscription_iran_direct: false });
    await screen.findByText(i18n.t("rapido.iranDirect.saved") as string);
    expect(toggle()).not.toBeChecked();
    expect(screen.getByText(i18n.t("rapido.integrations.disabled") as string)).toBeInTheDocument();
  });

  it("goes back to the environment's value with null", async () => {
    render(<IranDirectSetting />);
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: i18n.t("rapido.integrations.resetToEnv") as string }));
    await waitFor(() => expect(puts()).toHaveLength(1));
    expect(puts()[0][1].body).toEqual({ subscription_iran_direct: null });
  });

  it("keeps the switch where it was when saving fails", async () => {
    render(<IranDirectSetting />);
    await waitFor(() => expect(toggle()).toBeEnabled());
    fetchMock.mockRejectedValueOnce({ response: { _data: { detail: "Could not update integration settings" } } });
    fireEvent.click(toggle());
    await screen.findByText("Could not update integration settings");
    expect(toggle()).toBeChecked();
  });

  it("tells the operator which apps need their users to act, and what turning it off leaves alone", async () => {
    render(<IranDirectSetting />);
    await waitFor(() => expect(toggle()).toBeEnabled());
    expect(screen.getByText(/V2Box and v2RayTun/)).toBeInTheDocument();
    expect(screen.getByText(i18n.t("rapido.iranDirect.offHint") as string)).toBeInTheDocument();
  });
});
