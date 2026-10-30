import { fireEvent, render, screen, within } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
  restorePending: false,
  uploadPending: false,
  restore: vi.fn(),
}));

vi.mock("hooks/useBackupsQuery", () => ({
  downloadBackup: vi.fn(),
  useBackupsQuery: () => ({
    data: [{ filename: "backup-1.sql.gz", size_bytes: 1024, created_at: "2026-10-01T00:00:00Z" }],
    isLoading: false,
    isError: false,
  }),
  useCreateBackupMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useDeleteBackupMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useRestoreBackupMutation: () => ({ mutate: state.restore, isPending: state.restorePending }),
  useRestoreUploadMutation: () => ({ mutate: vi.fn(), isPending: state.uploadPending }),
}));
vi.mock("hooks/useCurrentAdminQuery", () => ({ useIsOwner: () => true }));

import { BackupsAdmin } from "../BackupsAdmin";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  state.restorePending = false;
  state.uploadPending = false;
  state.restore.mockReset();
});

const row = () => within(screen.getByText("backup-1.sql.gz").closest(".rp-card") as HTMLElement);

describe("while a restore runs", () => {
  it("still asks first", () => {
    render(<BackupsAdmin />);
    fireEvent.click(row().getByRole("button", { name: "Restore" }));
    expect(state.restore).not.toHaveBeenCalled();
    fireEvent.click(row().getByRole("button", { name: "Yes, replace the database" }));
    expect(state.restore.mock.calls[0][0]).toBe("backup-1.sql.gz");
  });

  it("shows that it is running on the backup's row, with nothing left to click there", () => {
    state.restorePending = true;
    render(<BackupsAdmin />);
    expect(row().getByRole("button", { name: "Restoring…" })).toBeDisabled();
    expect(row().queryByRole("button", { name: "Restore" })).not.toBeInTheDocument();
    expect(row().getByRole("button", { name: "Delete" })).toBeDisabled();
    expect(row().getByRole("status")).toHaveTextContent("Keep this page open");
  });

  it("shows the same for an uploaded file, and locks the file picker", () => {
    state.uploadPending = true;
    render(<BackupsAdmin />);
    fireEvent.click(screen.getByRole("button", { name: "Show" }));
    const card = within(screen.getByText(i18n.t("rapido.backups.uploadTitle")).closest(".rp-card") as HTMLElement);
    expect(card.getByRole("button", { name: "Restoring…" })).toBeDisabled();
    expect(card.getByRole("status")).toHaveTextContent("Keep this page open");
    expect((document.querySelector('input[type="file"]') as HTMLInputElement).disabled).toBe(true);
  });

  it("shows nothing of the sort when none is running", () => {
    render(<BackupsAdmin />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
