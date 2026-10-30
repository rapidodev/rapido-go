import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { User } from "types/User";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { UserActionModals } from "../UserActionModals";
import { useUsersUiStore } from "../usersUiStore";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({});
});

afterEach(async () => {
  act(() =>
    useUsersUiStore.setState({ deletingUser: null, resetUsageUser: null, revokeSubscriptionUser: null })
  );
  await i18n.changeLanguage("en");
});

const someone = { username: "customer-1" } as User;

const renderModals = () =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <UserActionModals />
    </QueryClientProvider>
  );

describe("the user action confirmations", () => {
  it("say deleting cuts the user off everywhere for good, and only delete on the second click", async () => {
    act(() => useUsersUiStore.getState().setDeletingUser(someone));
    renderModals();
    const text = screen.getByText(/Permanently delete/);
    expect(text).toHaveTextContent("customer-1");
    expect(text).toHaveTextContent("subscription link and configs stop working");
    expect(text).toHaveTextContent("disconnected from every node");
    expect(text).toHaveTextContent("This cannot be undone");
    expect(fetchMock).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/user/customer-1", { method: "DELETE" }));
    await waitFor(() => expect(screen.queryByText(/Permanently delete/)).not.toBeInTheDocument());
  });

  it("say a usage reset can bring a user back, what it erases, and that nobody is disconnected", () => {
    act(() => useUsersUiStore.getState().setResetUsageUser(someone));
    renderModals();
    const text = screen.getByText(/used traffic to zero/);
    expect(text).toHaveTextContent("customer-1");
    expect(text).toHaveTextContent("can connect again");
    expect(text).toHaveTextContent("usage history and any queued next plan are deleted");
    expect(text).toHaveTextContent("nobody is disconnected");
    expect(text).toHaveTextContent("This cannot be undone");
  });

  it("say revoking kills the old link and its configs, disconnects devices, and needs a new link", () => {
    act(() => useUsersUiStore.getState().setRevokeSubscriptionUser(someone));
    renderModals();
    const text = screen.getByText(/Revoke .*subscription/);
    expect(text).toHaveTextContent("customer-1");
    expect(text).toHaveTextContent("every config imported from it stop working");
    expect(text).toHaveTextContent("disconnected within moments");
    expect(text).toHaveTextContent("need the new link");
    expect(text).toHaveTextContent("This cannot be undone");
  });

  it("render the username as an element, not as literal tags, in every language", async () => {
    act(() => useUsersUiStore.getState().setDeletingUser(someone));
    renderModals();
    for (const lang of ["en", "fa", "ru", "zh"]) {
      await act(async () => {
        await i18n.changeLanguage(lang);
      });
      expect(screen.getByText("customer-1").tagName).toBe("B");
      expect(document.body.textContent).not.toContain("<b>");
    }
  });
});
