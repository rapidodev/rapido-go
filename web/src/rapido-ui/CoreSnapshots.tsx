import { FC, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";
import { errorText } from "service/errors";
import { queryKeys } from "utils/queryClient";
import { Button } from "rapido-ui/Button";
import { Card } from "rapido-ui/Card";
import { JsonEditor } from "rapido-ui/JsonEditor";
import { Modal } from "rapido-ui/Modal";
import { ltrIsolate } from "rapido-ui/bidi";

type SnapshotRow = { id: number; created_at: string; reason: string };
type Snapshot = SnapshotRow & { config: unknown };

// A snapshot's reason is the request that was about to change the fleet when
// it was taken ("PUT /api/settings/xray-config", see atomic.go), so what it
// holds is the state right before that kind of change. Anything unrecognised
// is shown as sent.
const reasonKey = (reason: string): string | null => {
  const path = reason.split(" ")[1] ?? "";
  if (path.endsWith("/restore")) return "rapido.snapshots.reasonRestore";
  if (path.startsWith("/api/inbounds")) return "rapido.snapshots.reasonInbounds";
  if (path === "/api/hosts") return "rapido.snapshots.reasonHosts";
  if (path.startsWith("/api/node")) return "rapido.snapshots.reasonNodes";
  if (path === "/api/settings/xray-config" || path === "/api/settings/core-config" || path === "/api/core/config") {
    return "rapido.snapshots.reasonCore";
  }
  return null;
};

// The saved versions behind the page's Apply: each one can be read before it
// is restored, and restoring asks for confirmation with its effects spelled
// out, since it replaces far more than the document above it.
export const CoreSnapshots: FC = () => {
  const { t, i18n } = useTranslation();
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<Snapshot | null>(null);
  const [saving, setSaving] = useState(false);
  const [restored, setRestored] = useState(false);
  const [error, setError] = useState("");
  const history = useQuery({
    queryKey: queryKeys.coreSnapshots,
    queryFn: () => fetch<SnapshotRow[]>("/core/config/backups"),
    enabled: open,
    staleTime: 0,
  });

  const when = (createdAt: string) => new Date(createdAt).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" });
  const reasonText = (reason: string) => {
    const key = reasonKey(reason);
    return key ? t(key) : ltrIsolate(reason);
  };

  const preview = async (id: number) => {
    setError("");
    setRestored(false);
    try {
      setSelected(await fetch<Snapshot>(`/core/config/backups/${id}`));
    } catch (e) {
      setError(errorText(e, t("rapido.snapshots.failed")));
    }
  };

  const restore = async () => {
    if (!selected) return;
    setSaving(true);
    setError("");
    try {
      await fetch(`/core/config/backups/${selected.id}/restore`, { method: "POST", body: { confirm: true } });
      setSelected(null);
      setRestored(true);
      await client.invalidateQueries();
    } catch (e) {
      setError(errorText(e, t("rapido.snapshots.failed")));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card className="mt-6 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold">{t("rapido.snapshots.title")}</h3>
          <p className="text-xs text-rapido-muted">{t("rapido.snapshots.desc")}</p>
        </div>
        <Button variant="chip" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? t("rapido.snapshots.hide") : t("rapido.snapshots.show")}
        </Button>
      </div>

      {restored && <p className="mt-3 text-xs text-emerald-400">{t("rapido.snapshots.restored")}</p>}
      {error && !selected && (
        <p role="alert" className="mt-3 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {error}
        </p>
      )}

      {open && (
        <div className="mt-3">
          {history.isError && (
            <p role="alert" className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
              {t("rapido.snapshots.failed")}
            </p>
          )}
          {history.isLoading && <p className="text-xs text-rapido-muted">{t("rapido.pleaseWait")}</p>}
          {history.data?.length === 0 && <p className="text-xs text-rapido-muted">{t("rapido.snapshots.empty")}</p>}
          {!!history.data?.length && (
            <div className="flex max-h-80 flex-col gap-1.5 overflow-y-auto pe-1">
              {history.data.map((row) => (
                <button
                  key={row.id}
                  type="button"
                  onClick={() => void preview(row.id)}
                  className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 rounded-lg border border-rapido-border bg-rapido-bg/40 p-2.5 text-start text-xs transition-colors hover:border-rapido-accent/50"
                >
                  <span className="text-rapido-text">{reasonText(row.reason)}</span>
                  <span className="text-rapido-muted">
                    {when(row.created_at)} · <span dir="ltr">#{row.id}</span>
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {selected && (
        <Modal onClose={() => !saving && setSelected(null)} className="max-w-3xl">
          <h2 className="mb-1 text-lg font-semibold">{t("rapido.snapshots.preview")}</h2>
          <p className="mb-3 text-xs text-rapido-muted">
            {reasonText(selected.reason)} · {when(selected.created_at)} · <span dir="ltr">#{selected.id}</span>
          </p>
          <JsonEditor readOnly rows={16} value={JSON.stringify(selected.config, null, 2)} ariaLabel={String(t("rapido.snapshots.preview"))} />
          <p className="mt-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-300">
            {t("rapido.snapshots.warning")}
          </p>
          {error && (
            <p role="alert" className="mt-2 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
              {error}
            </p>
          )}
          <div className="mt-3 flex justify-end gap-2">
            <Button variant="chip" disabled={saving} onClick={() => setSelected(null)}>
              {t("cancel")}
            </Button>
            <Button variant="chip" tone="amber" disabled={saving} onClick={() => void restore()}>
              {saving ? t("rapido.pleaseWait") : t("rapido.snapshots.restore")}
            </Button>
          </div>
        </Modal>
      )}
    </Card>
  );
};
