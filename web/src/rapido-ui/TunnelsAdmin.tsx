import { FC, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { SignalIcon } from "@heroicons/react/24/outline";
import { useTunnelsQuery } from "hooks/useTunnelsQuery";
import { useNodesQuery } from "hooks/useNodesQuery";
import { Tunnel } from "types/Tunnel";
import { ltrIsolate } from "rapido-ui/bidi";
import { Button } from "rapido-ui/Button";
import { EmptyState } from "rapido-ui/EmptyState";
import { Skeleton } from "rapido-ui/Skeleton";
import { TunnelCard } from "rapido-ui/TunnelCard";
import { TunnelDiscoverModal } from "rapido-ui/TunnelDiscoverModal";
import { TunnelEditModal } from "rapido-ui/TunnelEditModal";
import { TunnelFormModal } from "rapido-ui/TunnelFormModal";

// Tunnels between one of your nodes and an external relay. A tunnel row IS its
// own monitoring entry (health via internal/relayhealth, resource metrics via
// internal/tunnelmetrics) - the old separate "Tunnel Relays" monitoring-only
// page was merged in here, since every real relay this fleet has is a
// provisioned tunnel and already carries the SSH credentials a resource
// check needs. Two kinds of row: tunnels the panel built itself (managed:
// start/stop/restart/reconfigure/delete all act on the servers) and tunnels
// it merely detected (managed === false: monitored only, never changed).
// Provisioning/stop/start/restart run in the background on the server, so a
// row starts in a transient status and this page polls (useTunnelsQuery)
// until it settles.

export const TunnelsAdmin: FC = () => {
  const { t } = useTranslation();
  const { data: tunnels, isLoading, isError } = useTunnelsQuery();
  const { data: nodes } = useNodesQuery();
  const [adding, setAdding] = useState(false);
  const [detecting, setDetecting] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [teardownWarnings, setTeardownWarnings] = useState<{ name: string; detail: string }[]>([]);

  const rows = tunnels ?? [];
  const active = rows.filter((r) => r.status === "active").length;
  const nodeNameById = useMemo(() => {
    const map = new Map<number, string>();
    for (const n of nodes ?? []) map.set(n.id, n.name);
    return map;
  }, [nodes]);
  // Looked up by id so the modal always edits the freshest copy after a refetch.
  const editing: Tunnel | undefined = rows.find((r) => r.id === editingId);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-sm text-rapido-muted">{t("rapido.tunnels.summary", { active, total: rows.length })}</div>
        <div className="flex flex-wrap gap-2">
          <Button variant="chip" tone="sky" onClick={() => setDetecting(true)}>
            {t("rapido.tunnels.detect")}
          </Button>
          <Button variant="chip" tone="accent" onClick={() => setAdding(true)}>
            + {t("rapido.tunnels.addNew")}
          </Button>
        </div>
      </div>

      {isError && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {t("rapido.tunnels.loadFailed")}
        </div>
      )}

      {/* Stays until it is dismissed: something may still be running on a server. */}
      {teardownWarnings.map((w, index) => (
        <div
          key={index}
          role="alert"
          className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-rapido-warn/30 bg-rapido-warn/10 px-3 py-2 text-xs text-rapido-warn"
        >
          <span className="min-w-0 break-words">
            {t("rapido.tunnels.teardownWarning", { name: w.name, detail: ltrIsolate(w.detail) })}
          </span>
          <Button variant="chip" onClick={() => setTeardownWarnings((all) => all.filter((_, i) => i !== index))}>
            {t("rapido.close")}
          </Button>
        </div>
      ))}

      {isLoading ? (
        <div className="grid gap-4" aria-busy="true">
          <Skeleton className="h-64" />
          <Skeleton className="h-64" />
        </div>
      ) : rows.length === 0 ? (
        <EmptyState
          icon={<SignalIcon />}
          title={t("rapido.tunnels.empty")}
          description={t("rapido.tunnels.emptyHint")}
          action={
            <div className="flex flex-wrap justify-center gap-2">
              <Button variant="chip" tone="sky" onClick={() => setDetecting(true)}>
                {t("rapido.tunnels.detect")}
              </Button>
              <Button variant="chip" tone="accent" onClick={() => setAdding(true)}>
                + {t("rapido.tunnels.addNew")}
              </Button>
            </div>
          }
        />
      ) : (
        <div className="grid gap-4">
          {rows.map((tunnel) => (
            <TunnelCard
              key={tunnel.id}
              tunnel={tunnel}
              nodeName={nodeNameById.get(tunnel.node_id)}
              onEdit={(tn) => setEditingId(tn.id)}
              onTeardownWarning={(name, detail) => setTeardownWarnings((all) => [...all, { name, detail }])}
            />
          ))}
        </div>
      )}

      {adding && <TunnelFormModal onClose={() => setAdding(false)} onCreated={() => setAdding(false)} />}
      {detecting && (
        <TunnelDiscoverModal onClose={() => setDetecting(false)} onAdopted={() => setDetecting(false)} />
      )}
      {editing && <TunnelEditModal tunnel={editing} onClose={() => setEditingId(null)} />}
    </div>
  );
};

export default TunnelsAdmin;
