import { FC, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCreateTunnelMutation } from "hooks/useTunnelsQuery";
import { useNodesQuery } from "hooks/useNodesQuery";
import { useInboundPortsQuery } from "hooks/useInboundsQuery";
import { CreatableTunnelMethod, TunnelCreatePayload, TUNNEL_METHODS } from "types/Tunnel";
import { Node } from "types/Node";
import { parseInboundPortInput } from "utils/inboundPorts";
import { errorText } from "service/errors";
import { Button } from "rapido-ui/Button";
import { Input } from "rapido-ui/Input";
import { Select } from "rapido-ui/Select";
import { Checkbox } from "rapido-ui/Checkbox";
import { Modal } from "rapido-ui/Modal";

// The create-a-tunnel form: the panel builds both ends itself over SSH.

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
  method: CreatableTunnelMethod;
  nodeTunnelAddr: string;
  relayTunnelAddr: string;
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
  method: "frp_direct",
  nodeTunnelAddr: "",
  relayTunnelAddr: "",
});

// A node's own candidate ports: its explicit listen_ports profile if it has
// one, otherwise every port any inbound actually listens on (an
// unrestricted node serves all of them).
const candidatePortsForNode = (node: Node | undefined, portsByTag: Record<string, number[]>): number[] => {
  if (node?.listen_ports && node.listen_ports.length > 0) {
    return [...node.listen_ports].sort((a, b) => a - b);
  }
  const all = new Set<number>();
  for (const ports of Object.values(portsByTag)) for (const p of ports) all.add(p);
  return [...all].sort((a, b) => a - b);
};

export const TunnelFormModal: FC<{ onClose: () => void; onCreated: () => void }> = ({ onClose, onCreated }) => {
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
      method: values.method,
      node_tunnel_addr: values.nodeTunnelAddr.trim() || undefined,
      relay_tunnel_addr: values.relayTunnelAddr.trim() || undefined,
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

        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnels.method")}</span>
          <Select value={values.method} onChange={(e) => set({ method: e.target.value as CreatableTunnelMethod })}>
            {TUNNEL_METHODS.map((m) => (
              <option key={m} value={m}>
                {t(`rapido.tunnels.method.${m}`)}
              </option>
            ))}
          </Select>
          <span className="text-xs text-rapido-muted">{t(`rapido.tunnels.methodHint.${values.method}`)}</span>
        </label>

        <div className="grid grid-cols-2 gap-2">
          <label className="flex flex-col gap-1">
            <span className="text-xs text-rapido-muted">{t("rapido.tunnels.nodeTunnelAddr")}</span>
            <Input
              dir="ltr"
              placeholder={values.method === "ip6gre_dnat" ? "2a12:de40:51::1" : t("rapido.tunnels.addrOptional")}
              value={values.nodeTunnelAddr}
              onChange={(e) => set({ nodeTunnelAddr: e.target.value })}
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-xs text-rapido-muted">{t("rapido.tunnels.relayTunnelAddr")}</span>
            <Input
              dir="ltr"
              placeholder={values.method === "ip6gre_dnat" ? "2001:db8:3::1" : t("rapido.tunnels.addrOptional")}
              value={values.relayTunnelAddr}
              onChange={(e) => set({ relayTunnelAddr: e.target.value })}
            />
          </label>
        </div>

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

