import { render, screen } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ tickets: [] as unknown[], total: 0 }));

vi.mock("hooks/useTicketsQuery", () => ({
  useTicketsQuery: () => ({
    data: { tickets: state.tickets, total: state.total },
    isLoading: false,
    isError: false,
    isFetching: false,
    refetch: vi.fn(),
  }),
  useTicketQuery: () => ({ data: undefined, isError: false }),
  useReplyTicketMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateTicketMutation: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock("hooks/useCurrentAdminQuery", () => ({
  useCurrentAdminQuery: () => ({ data: { username: "a", is_sudo: true }, isPending: false, isSuccess: true }),
}));

import { TicketsAdmin } from "../TicketsAdmin";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

const waiting = (id: number) => ({
  id,
  subject: `ticket ${id}`,
  status: "open",
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
  username: `user-${id}`,
  messages: [{ id, body: "help", is_admin: false, created_at: "2026-10-01T00:00:00Z" }],
});

beforeEach(() => {
  state.tickets = [waiting(1), waiting(2)];
  state.total = 2;
});

describe("the awaiting-reply count", () => {
  it("is the count when everything is on one page", () => {
    render(<TicketsAdmin />);
    expect(screen.getByText("2 awaiting reply")).toBeInTheDocument();
  });

  it("says it is only this page when there are more pages it cannot see", () => {
    state.total = 300;
    render(<TicketsAdmin />);
    expect(screen.getByText("2 awaiting reply on this page")).toBeInTheDocument();
  });
});
