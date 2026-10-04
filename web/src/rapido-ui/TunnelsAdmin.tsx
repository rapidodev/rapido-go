import { FC, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import classNames from "classnames";
import {
  useCreateTunnelMutation,
  useDeleteTunnelMutation,
  useTunnelsQuery,
} from "hooks/useTunnelsQuery";
import { useNodesQuery } from "hooks/useNodesQuery";
import { useInboundPortsQuery } from "hooks/useInboundsQuery";
import { Tunnel, TunnelCreatePayload, TunnelStatus } from "types/Tunnel";
import { Node } from "types/Node";
import { parseInboundPortInput, summarizePorts } from "utils/inboundPorts";
import { errorText } from "service/errors";
import { Card } from "rapido-ui/Card";
import { Badge, BadgeTone } from "rapido-ui/Badge";
import { Button } from "rapido-ui/Button";
import { Input } from "rapido-ui/Input";
import { Select } from "rapido-ui/Select";
import { Checkbox } from "rapido-ui/Checkbox";
import { Modal } from "rapido-ui/Modal";

// Panel-provisioned GRE+FRP tunnels: unlike TunnelRelaysAdmin.tsx (pure
// monitoring of a relay someone set up by hand), creating a row here makes
// the panel itself SSH into both the relay and the node and bring the whole
// tunnel up (internal/tunnelprovision), and deleting one tears both ends
// back down. Provisioning runs in the background on the server, so a freshly
// created row starts "pending" and this page polls (useTunnelsQuery) until
// it settles into "active" or "failed".

const cardToneClasses: Partial<Record<BadgeTone, string>> = {
  green: "!border-emerald-500/60 bg-emerald-500/[0.04]",
  red: "!border-red-500/60 bg-red-500/[0.04]",
  yellow: "!border-yellow-500/50 bg-yellow-500/[0.03]",
};

const statusTone: Record<TunnelStatus, BadgeTone> = {
  pending: "yellow",
  active: "green",
  failed: "red",
  deleting: "yellow",
};

const statusLabelKey: Record<TunnelStatus, string> = {
  pending: "rapido.tunnels.statusPending",
  active: "rapido.tunnels.statusActive",
  failed: "rapido.tunnels.statusFailed",
  deleting: "rapido.tunnels.statusDeleting",
};

// ---------------------------------------------------------------------------

type FormValues = {
  name: string;
  nodeId: string;
  nodeSshPort: string;
  nodeSshUser: string;
  nodeSshPassword: string;
  relayHost: string;
  relaySshPort: string;
  relaySshUser: string;
  relaySshPassword: string;
  customPorts: string;
};

const emptyForm = (): FormValues => ({
  name: "",
  nodeId: "",
  nodeSshPort: "22",
  nodeSshUser: "root",
  nodeSshPassword: "",
  relayHost: "",
  relaySshPort: "22",
  relaySshUser: "root",
  relaySshPassword: "",
  customPorts: "",
});

// A node's own candidate ports: its explicit listen_ports profile if it has
// one, otherwise every port any inbound actually listens on (an
// unrestricted node serves all of them) - the same source of truth
// NodesAdmin.tsx's own per-node port field reads from.
const candidatePortsForNode = (node: Node | undefined, portsByTag: Record<string, number[]>): number[] => {
  if (node?.listen_ports && node.listen_ports.length > 0) {
    return [...node.listen_ports].sort((a, b) => a - b);
  }
  const all = new Set<number>();
  for (const ports of Object.values(portsByTag)) for (const p of ports) all.add(p);
  return [...all].sort((a, b) => a - b);
};

const TunnelFormModal: FC<{ onClose: () => void; onCreated: () => void }> = ({ onClose, onCreated }) => {
  const { t } = useTranslation();
  const { data: nodes } = useNodesQuery();
  const { data: portsByTag } = useInboundPortsQuery();
  const [values, setValues] = useState<FormValues>(emptyForm());
  const [selectedPorts, setSelectedPorts] = useState<ReadonlySet<number>>(new Set());
  const [error, setError] = useState("");
  const createTunnel = useCreateTunnelMutation();

  const set = (patch: Partial<FormValues>) => setValues((v) => ({ ...v, ...patch }));

  const selectedNode = useMemo(
    () => (nodes ?? []).find((n) => String(n.id) === values.nodeId),
    [nodes, values.nodeId]
  );
  const candidatePorts = useMemo(
    () => candidatePortsForNode(selectedNode, portsByTag ?? {}),
    [selectedNode, portsByTag]
  );

  const togglePort = (port: number) =>
    setSelectedPorts((prev) => {
      const next = new Set(prev);
      if (next.has(port)) next.delete(port);
      else next.add(port);
      return next;
    });

  const customParsed = parseInboundPortInput(values.customPorts);
  const allPorts = useMemo(() => {
    const set = new Set(selectedPorts);
    if (customParsed.ok) for (const p of customParsed.ports) set.add(p);
    return [...set].sort((a, b) => a - b);
  }, [selectedPorts, customParsed]);

  const canSubmit =
    !!values.name.trim() &&
    !!values.nodeId &&
    !!values.relayHost.trim() &&
    !!values.relaySshUser.trim() &&
    !!values.relaySshPassword &&
    !!values.nodeSshUser.trim() &&
    !!values.nodeSshPassword &&
    customParsed.ok &&
    allPorts.length > 0;

  const submit = () => {
    setError("");
    if (!customParsed.ok) {
      setError(t("rapido.tunnels.customPortsInvalid"));
      return;
    }
    const body: TunnelCreatePayload = {
      name: values.name.trim(),
      node_id: Number(values.nodeId),
      node_ssh_port: Number(values.nodeSshPort) || 22,
      node_ssh_user: values.nodeSshUser.trim(),
      node_ssh_password: values.nodeSshPassword,
      relay_host: values.relayHost.trim(),
      relay_ssh_port: Number(values.relaySshPort) || 22,
      relay_ssh_user: values.relaySshUser.trim(),
      relay_ssh_password: values.relaySshPassword,
      ports: allPorts,
    };
    createTunnel.mutate(body, {
      onSuccess: onCreated,
      onError: (e) => setError(errorText(e, t("rapido.tunnels.saveFailed"))),
    });
  };

  return (
    <Modal onClose={onClose} title={t("rapido.tunnels.addTitle")}>
      <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto pe-1">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnels.name")}</span>
          <Input
            dir="ltr"
            placeholder={t("rapido.tunnels.namePlaceholder")}
            value={values.name}
            onChange={(e) => set({ name: e.target.value })}
          />
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnels.node")}</span>
          <Select
            value={values.nodeId}
            onChange={(e) => {
              set({ nodeId: e.target.value });
              setSelectedPorts(new Set());
            }}
          >
            <option value="">{t("rapido.tunnels.nodePlaceholder")}</option>
            {(nodes ?? []).map((n) => (
              <option key={n.id} value={n.id}>
                {n.name}
              </option>
            ))}
          </Select>
        </label>

        <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3">
          <span className="text-xs font-medium text-rapido-muted">{t("rapido.tunnels.relaySection")}</span>
          <label className="flex flex-col gap-1">
            <span className="text-xs text-rapido-muted">{t("rapido.tunnels.relayHost")}</span>
            <Input dir="ltr" value={values.relayHost} onChange={(e) => set({ relayHost: e.target.value })} />
          </label>
          <div className="grid grid-cols-3 gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshPort")}</span>
              <Input
                dir="ltr"
                type="number"
                inputMode="numeric"
                value={values.relaySshPort}
                onChange={(e) => set({ relaySshPort: e.target.value })}
              />
            </label>
            <label className="col-span-1 flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshUser")}</span>
              <Input dir="ltr" value={values.relaySshUser} onChange={(e) => set({ relaySshUser: e.target.value })} />
            </label>
            <label className="col-span-1 flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshPassword")}</span>
              <Input
                dir="ltr"
                type="password"
                value={values.relaySshPassword}
                onChange={(e) => set({ relaySshPassword: e.target.value })}
              />
            </label>
          </div>
        </div>

        <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3">
          <span className="text-xs font-medium text-rapido-muted">{t("rapido.tunnels.nodeSection")}</span>
          <div className="grid grid-cols-3 gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshPort")}</span>
              <Input
                dir="ltr"
                type="number"
                inputMode="numeric"
                value={values.nodeSshPort}
                onChange={(e) => set({ nodeSshPort: e.target.value })}
              />
            </label>
            <label className="col-span-1 flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshUser")}</span>
              <Input dir="ltr" value={values.nodeSshUser} onChange={(e) => set({ nodeSshUser: e.target.value })} />
            </label>
            <label className="col-span-1 flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshPassword")}</span>
              <Input
                dir="ltr"
                type="password"
                value={values.nodeSshPassword}
                onChange={(e) => set({ nodeSshPassword: e.target.value })}
              />
            </label>
          </div>
        </div>

        <div className="flex flex-col gap-2">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnels.ports")}</span>
          {!values.nodeId ? (
            <p className="text-xs text-rapido-muted">{t("rapido.tunnels.portsNeedsNode")}</p>
          ) : candidatePorts.length === 0 ? (
            <p className="text-xs text-rapido-muted">{t("rapido.tunnels.portsEmpty")}</p>
          ) : (
            <div className="flex flex-wrap gap-3 rounded-lg border border-rapido-border p-3">
              {candidatePorts.map((port) => (
                <Checkbox
                  key={port}
                  checked={selectedPorts.has(port)}
                  onChange={() => togglePort(port)}
                  label={<span dir="ltr">{port}</span>}
                />
              ))}
            </div>
          )}
          <label className="flex flex-col gap-1">
            <span className="text-xs text-rapido-muted">{t("rapido.tunnels.customPorts")}</span>
            <Input
              dir="ltr"
              placeholder="20001, 20005, 20009"
              value={values.customPorts}
              onChange={(e) => set({ customPorts: e.target.value })}
            />
            <span className="text-xs text-rapido-muted">{t("rapido.tunnels.customPortsHint")}</span>
          </label>
          {!customParsed.ok && values.customPorts.trim() !== "" && (
            <span className="text-xs text-red-400">{t("rapido.tunnels.customPortsInvalid")}</span>
          )}
        </div>

        {error && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
            {error}
          </div>
        )}

        <div className="mt-2 flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} disabled={createTunnel.isPending}>
            {t("cancel")}
          </Button>
          <Button variant="primary" disabled={createTunnel.isPending || !canSubmit} onClick={submit}>
            {createTunnel.isPending ? t("rapido.pleaseWait") : t("rapido.tunnels.addTitle")}
          </Button>
        </div>
      </div>
    </Modal>
  );
};

// ---------------------------------------------------------------------------

const TunnelCard: FC<{ tunnel: Tunnel; nodeName: string | undefined }> = ({ tunnel, nodeName }) => {
  const { t } = useTranslation();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const deleteTunnel = useDeleteTunnelMutation();
  const tone = statusTone[tunnel.status];
  const ports = summarizePorts(tunnel.ports);
  const busy = tunnel.status === "pending" || tunnel.status === "deleting";

  const remove = () => {
    setMsg(null);
    deleteTunnel.mutate(tunnel.id, {
      onSuccess: (result) => {
        if (result.teardown_warning) setMsg(result.teardown_warning);
      },
      onError: (e) => {
        setMsg(errorText(e, t("rapido.tunnels.deleteFailed")));
        setConfirmDelete(false);
      },
    });
  };

  return (
    <Card className={classNames("p-4", cardToneClasses[tone])}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-semibold">{tunnel.name}</span>
            <Badge tone={tone}>{t(statusLabelKey[tunnel.status])}</Badge>
          </div>
        </div>

        <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-rapido-muted">
          <span>
            {t("rapido.tunnels.node")}: <span className="text-rapido-text">{nodeName ?? tunnel.node_id}</span>
          </span>
          <span>
            {t("rapido.tunnels.relayHost")}:{" "}
            <span className="text-rapido-text" dir="ltr">
              {tunnel.relay_host}
            </span>
          </span>
          <span dir="ltr" title={ports.truncated ? tunnel.ports.join(", ") : undefined}>
            {t("rapido.tunnels.portsLabel")}: <span className="text-rapido-text">{ports.text}</span>
            {ports.truncated && (
              <span className="text-rapido-muted"> {t("rapido.xrayConfig.portsCount", { count: ports.total })}</span>
            )}
          </span>
        </div>

        {tunnel.status === "failed" && tunnel.status_message && (
          <div className="break-words text-xs text-rapido-muted" title={tunnel.status_message}>
            <span dir="ltr" className="text-red-300/80">
              {tunnel.status_message}
            </span>
          </div>
        )}

        {msg && (
          <div className="rounded-lg border border-yellow-500/30 bg-yellow-500/10 px-3 py-2 text-xs text-yellow-300">
            {msg}
          </div>
        )}

        <div className="flex flex-wrap items-center gap-1.5">
          {confirmDelete ? (
            <>
              <span className="text-xs text-red-400">{t("rapido.tunnels.confirmDelete")}</span>
              <Button variant="chip" tone="red" disabled={deleteTunnel.isPending} onClick={remove}>
                {t("delete")}
              </Button>
              <Button variant="chip" onClick={() => setConfirmDelete(false)}>
                {t("cancel")}
              </Button>
            </>
          ) : (
            <Button variant="chip" tone="red" disabled={busy} onClick={() => setConfirmDelete(true)}>
              {t("delete")}
            </Button>
          )}
        </div>
      </div>
    </Card>
  );
};

// ---------------------------------------------------------------------------

export const TunnelsAdmin: FC = () => {
  const { t } = useTranslation();
  const { data: tunnels, isLoading, isError } = useTunnelsQuery();
  const { data: nodes } = useNodesQuery();
  const [adding, setAdding] = useState(false);

  const rows = tunnels ?? [];
  const active = rows.filter((r) => r.status === "active").length;
  const nodeNameById = useMemo(() => {
    const map = new Map<number, string>();
    for (const n of nodes ?? []) map.set(n.id, n.name);
    return map;
  }, [nodes]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-sm text-rapido-muted">{t("rapido.tunnels.summary", { active, total: rows.length })}</div>
        <Button variant="chip" tone="accent" onClick={() => setAdding(true)}>
          + {t("rapido.tunnels.addNew")}
        </Button>
      </div>

      {isError && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {t("rapido.tunnels.loadFailed")}
        </div>
      )}

      {isLoading ? (
        <p className="text-sm text-rapido-muted">{t("rapido.tickets.loading")}</p>
      ) : rows.length === 0 ? (
        <Card className="p-6 text-center text-sm text-rapido-muted">{t("rapido.tunnels.empty")}</Card>
      ) : (
        <div className="grid gap-3 lg:grid-cols-2">
          {rows.map((tunnel) => (
            <TunnelCard key={tunnel.id} tunnel={tunnel} nodeName={nodeNameById.get(tunnel.node_id)} />
          ))}
        </div>
      )}

      {adding && <TunnelFormModal onClose={() => setAdding(false)} onCreated={() => setAdding(false)} />}
    </div>
  );
};

export default TunnelsAdmin;
