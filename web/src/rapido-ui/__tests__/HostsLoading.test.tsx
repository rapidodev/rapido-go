import { fireEvent, render, screen } from "@testing-library/react";
import { beforeAll, beforeEach, expect, it, vi } from "vitest";
import i18n from "locales/i18n";

const state = vi.hoisted(() => ({ loading: true, error: false, refetch: vi.fn() }));
vi.mock("hooks/useHostsQuery", () => ({
  useHostsQuery: () => ({ data: undefined, isLoading: state.loading, isError: state.error, refetch: state.refetch }),
  useSaveHostsMutation: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock("hooks/useInboundsQuery", () => ({ useInboundsQuery: () => ({ data: {} }) }));
vi.mock("hooks/useHostsLoadQuery", () => ({ useHostsLoadQuery: () => ({ data: undefined }) }));

import { HostsAdmin } from "../HostsAdmin";

beforeAll(async () => { await i18n.changeLanguage("en"); });
beforeEach(() => { state.loading = true; state.error = false; state.refetch.mockReset(); });

it("shows a neutral loading message on the hosts page", () => {
  render(<HostsAdmin />);
  expect(screen.getByText("Please wait...")).toBeInTheDocument();
  expect(screen.queryByText("Loading tickets...")).not.toBeInTheDocument();
});

it("shows a recovery action when hosts could not be loaded", () => {
  state.loading = false;
  state.error = true;
  render(<HostsAdmin />);
  expect(screen.getByRole("alert")).toHaveTextContent("Could not load hosts.");
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  expect(state.refetch).toHaveBeenCalledTimes(1);
});
