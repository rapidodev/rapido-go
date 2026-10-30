import { fireEvent, render, screen } from "@testing-library/react";
import i18n from "locales/i18n";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { useUsersUiStore } from "rapido-ui/usersUiStore";
import { queryClient } from "utils/queryClient";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { Login } from "../Login";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  fetchMock.mockReset();
});

afterEach(() => {
  localStorage.clear();
  queryClient.clear();
});

const renderLogin = (state?: unknown) =>
  render(
    <MemoryRouter initialEntries={[{ pathname: "/login", state }]}>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={<div>Overview page</div>} />
      </Routes>
    </MemoryRouter>
  );

const submit = () => {
  fireEvent.change(screen.getByPlaceholderText("Username"), { target: { value: "alice" } });
  fireEvent.change(screen.getByPlaceholderText("Password"), { target: { value: "not-a-real-password" } });
  fireEvent.submit(screen.getByRole("button", { name: "Login" }).closest("form") as HTMLFormElement);
};

describe("the login page", () => {
  it("ends whatever session was there when it opens, which is how logging out works", () => {
    localStorage.setItem("token", "test-only-session");
    queryClient.setQueryData(["users", {}], { total: 1 });
    useUsersUiStore.getState().setFilters({ search: "someone" });
    renderLogin();
    expect(localStorage.getItem("token")).toBeNull();
    expect(queryClient.getQueryData(["users", {}])).toBeUndefined();
    expect(useUsersUiStore.getState().filters.search).toBeUndefined();
  });

  it("says why the admin is here when their session expired, and only then", () => {
    const { unmount } = renderLogin({ sessionExpired: true });
    expect(screen.getByRole("status")).toHaveTextContent("Your session has expired");
    unmount();
    renderLogin({ sessionExpired: false });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("shows the panel's answer for a wrong password in place of that note, and stays here", async () => {
    fetchMock.mockRejectedValue(
      Object.assign(new Error("401"), { status: 401, response: { _data: { detail: "Incorrect username or password" } } })
    );
    renderLogin({ sessionExpired: true });
    submit();
    expect(await screen.findByText("Incorrect username or password")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(localStorage.getItem("token")).toBeNull();
    expect(screen.queryByText("Overview page")).not.toBeInTheDocument();
  });

  it("stores the token and opens the panel on success", async () => {
    fetchMock.mockResolvedValue({ access_token: "test-only-new-session" });
    renderLogin();
    submit();
    expect(await screen.findByText("Overview page")).toBeInTheDocument();
    expect(localStorage.getItem("token")).toBe("test-only-new-session");
    expect(fetchMock.mock.calls[0][0]).toBe("/admin/token");
  });
});
