import { fireEvent, render, screen } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, expect, it, vi } from "vitest";

const preview = vi.hoisted(() => vi.fn());
vi.mock("hooks/useAdminsQuery", () => ({
  usePreviewInactiveAdminsMutation: () => ({ mutate: preview, isPending: false }),
  useDeleteInactiveAdminsMutation: () => ({ mutate: vi.fn(), isPending: false }),
}));

import { InactiveAdmins } from "../InactiveAdmins";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

it("keeps a day count typed on a Persian keyboard, instead of dropping every digit", () => {
  render(<InactiveAdmins />);
  const field = screen.getByLabelText(i18n.t("rapido.inactiveAdmins.days") as string);
  fireEvent.change(field, { target: { value: "۳۰" } });
  expect(field).toHaveValue("30");
  fireEvent.click(screen.getByRole("button", { name: i18n.t("rapido.inactiveAdmins.preview") as string }));
  expect(preview.mock.calls[0][0]).toBe(30);
});
