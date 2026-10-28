import { fireEvent, render, screen } from "@testing-library/react";
import { beforeAll, expect, it, vi } from "vitest";
import i18n from "locales/i18n";

const refetch = vi.hoisted(() => vi.fn());
vi.mock("hooks/useGatewayQuery", () => ({
  useGatewaySettingsQuery: () => ({ data: undefined, isLoading: false, isError: true, refetch }),
  useGatewayPeersQuery: () => ({ data: undefined, isLoading: false, isError: true }),
  useUpdateGatewaySettingsMutation: () => ({ mutate: vi.fn(), isPending: false }),
}));

import { GatewayAdmin } from "../GatewayAdmin";

beforeAll(async () => { await i18n.changeLanguage("en"); });

it("offers a retry for failed settings and does not report failed peers as empty", () => {
  render(<GatewayAdmin />);
  expect(screen.getByRole("alert")).toHaveTextContent("Could not load gateway settings.");
  expect(screen.queryByText("Please wait...")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  expect(refetch).toHaveBeenCalledTimes(1);
});
