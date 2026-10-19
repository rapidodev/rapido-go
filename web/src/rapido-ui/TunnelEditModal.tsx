import { FC, ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { useUpdateTunnelMutation } from "hooks/useTunnelsQuery";
import { Tunnel, TunnelUpdatePayload } from "types/Tunnel";
import { errorText } from "service/errors";
import { Button } from "./Button";
import { Input } from "./Input";
import { Modal } from "./Modal";
import { PortChips } from "./PortChips";

const Field: FC<{ label: string; hint?: string; children: ReactNode }> = ({ label, hint, children }) => (
  <label className="flex min-w-0 flex-col gap-1">
    <span className="text-xs text-rapido-muted">{label}</span>
    {children}
    {hint && <span className="text-[11px] text-rapido-muted">{hint}</span>}
  </label>
);

const sameList = (a: readonly number[], b: readonly number[]) =>
  a.length === b.length && a.every((v, i) => v === b[i]);

/** The PATCH body for what the admin actually changed: untouched fields and
 * empty passwords are left out, which the backend reads as "keep current". */
export const buildUpdatePayload = (
  tunnel: Pick<
    Tunnel,
    "name" | "node_ssh_port" | "node_ssh_user" | "relay_host" | "relay_ssh_port" | "relay_ssh_user" | "ports"
  >,
  v: {
    name: string;
    nodeSshPort: string;
    nodeSshUser: string;
    nodeSshPassword: string;
    relayHost: string;
    relaySshPort: string;
    relaySshUser: string;
    relaySshPassword: string;
    ports: number[];
  }
): TunnelUpdatePayload => {
  const body: TunnelUpdatePayload = {};
  const name = v.name.trim();
  if (name && name !== tunnel.name) body.name = name;
  const nodePort = Number(v.nodeSshPort);
  if (nodePort && nodePort !== tunnel.node_ssh_port) body.node_ssh_port = nodePort;
  const nodeUser = v.nodeSshUser.trim();
  if (nodeUser && nodeUser !== tunnel.node_ssh_user) body.node_ssh_user = nodeUser;
  if (v.nodeSshPassword) body.node_ssh_password = v.nodeSshPassword;
  const host = v.relayHost.trim();
  if (host && host !== tunnel.relay_host) body.relay_host = host;
  const relayPort = Number(v.relaySshPort);
  if (relayPort && relayPort !== tunnel.relay_ssh_port) body.relay_ssh_port = relayPort;
  const relayUser = v.relaySshUser.trim();
  if (relayUser && relayUser !== tunnel.relay_ssh_user) body.relay_ssh_user = relayUser;
  if (v.relaySshPassword) body.relay_ssh_password = v.relaySshPassword;
  const sortedOld = [...tunnel.ports].sort((a, b) => a - b);
  if (!sameList(sortedOld, v.ports)) body.ports = v.ports;
  return body;
};

export const TunnelEditModal: FC<{ tunnel: Tunnel; onClose: () => void }> = ({ tunnel, onClose }) => {
  const { t } = useTranslation();
  const L = (k: string, o?: Record<string, unknown>) => String(o ? t(k, o) : t(k));
  const managed = tunnel.managed !== false;
  const update = useUpdateTunnelMutation();
  const [error, setError] = useState("");
  const [v, setV] = useState({
    name: tunnel.name,
    nodeSshPort: String(tunnel.node_ssh_port ?? 22),
    nodeSshUser: tunnel.node_ssh_user ?? "root",
    nodeSshPassword: "",
    relayHost: tunnel.relay_host,
    relaySshPort: String(tunnel.relay_ssh_port ?? 22),
    relaySshUser: tunnel.relay_ssh_user ?? "root",
    relaySshPassword: "",
    ports: [...tunnel.ports].sort((a, b) => a - b),
  });
  const set = (patch: Partial<typeof v>) => setV((p) => ({ ...p, ...patch }));

  const body = buildUpdatePayload(
    {
      name: tunnel.name,
      node_ssh_port: tunnel.node_ssh_port,
      node_ssh_user: tunnel.node_ssh_user,
      relay_host: tunnel.relay_host,
      relay_ssh_port: tunnel.relay_ssh_port,
      relay_ssh_user: tunnel.relay_ssh_user,
      ports: tunnel.ports,
    },
    v
  );
  const changed = Object.keys(body).length > 0;
  const portsChanged = body.ports !== undefined;
  const canSave = changed && !!v.name.trim() && v.ports.length > 0;

  const save = () => {
    setError("");
    update.mutate(
      { id: tunnel.id, body },
      {
        onSuccess: onClose,
        onError: (e) => setError(errorText(e, L("rapido.tunnels.editFailed"))),
      }
    );
  };

  const sshRow = (side: "node" | "relay") => {
    const portKey = side === "node" ? "nodeSshPort" : "relaySshPort";
    const userKey = side === "node" ? "nodeSshUser" : "relaySshUser";
    const passKey = side === "node" ? "nodeSshPassword" : "relaySshPassword";
    return (
      <div className="grid grid-cols-3 gap-2">
        <Field label={L("rapido.tunnels.sshPort")}>
          <Input
            dir="ltr"
            type="number"
            inputMode="numeric"
            value={v[portKey]}
            onChange={(e) => set({ [portKey]: e.target.value })}
          />
        </Field>
        <Field label={L("rapido.tunnels.sshUser")}>
          <Input dir="ltr" value={v[userKey]} onChange={(e) => set({ [userKey]: e.target.value })} />
        </Field>
        <Field label={L("rapido.tunnels.sshPassword")}>
          <Input
            dir="ltr"
            type="password"
            autoComplete="new-password"
            placeholder="••••••"
            value={v[passKey]}
            onChange={(e) => set({ [passKey]: e.target.value })}
          />
        </Field>
      </div>
    );
  };

  return (
    <Modal onClose={onClose} title={L("rapido.tunnels.editTitle", { name: tunnel.name })} className="max-w-xl">
      <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto pe-1">
        {!managed && (
          <div className="rounded-lg border border-sky-500/30 bg-sky-500/10 px-3 py-2 text-xs text-sky-300">
            {L("rapido.tunnels.editDetectedNote")}
          </div>
        )}

        <Field label={L("rapido.tunnels.name")}>
          <Input dir="ltr" value={v.name} onChange={(e) => set({ name: e.target.value })} />
        </Field>

        <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3">
          <span className="text-xs font-medium text-rapido-muted">{L("rapido.tunnels.nodeSection")}</span>
          {sshRow("node")}
        </div>

        <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3">
          <span className="text-xs font-medium text-rapido-muted">{L("rapido.tunnels.relaySection")}</span>
          <Field label={L("rapido.tunnels.relayHost")}>
            <Input dir="ltr" value={v.relayHost} onChange={(e) => set({ relayHost: e.target.value })} />
          </Field>
          {sshRow("relay")}
        </div>
        <p className="-mt-2 text-[11px] text-rapido-muted">{L("rapido.tunnels.passwordKeepHint")}</p>

        <div className="flex flex-col gap-2">
          <span className="text-xs text-rapido-muted">{L("rapido.tunnels.ports")}</span>
          <PortChips ports={v.ports} onChange={(ports) => set({ ports })} disabled={update.isPending} />
          <span className="text-[11px] text-rapido-muted">
            {managed ? L("rapido.tunnels.portsManagedHint") : L("rapido.tunnels.portsDetectedHint")}
          </span>
          {managed && portsChanged && (
            <div
              role="alert"
              className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-300"
            >
              {L("rapido.tunnels.portsReconnectWarning")}
            </div>
          )}
        </div>

        {error && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{error}</div>
        )}

        <div className="mt-1 flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} disabled={update.isPending}>
            {L("cancel")}
          </Button>
          <Button variant="primary" disabled={update.isPending || !canSave} onClick={save}>
            {update.isPending ? L("rapido.pleaseWait") : L("rapido.tunnels.save")}
          </Button>
        </div>
      </div>
    </Modal>
  );
};
