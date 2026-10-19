import { FC, ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAdoptTunnelMutation, useDiscoverTunnelMutation } from "hooks/useTunnelsQuery";
import { useNodesQuery } from "hooks/useNodesQuery";
import { TunnelDiscoverPayload, TunnelDiscoverResult } from "types/Tunnel";
import { errorText } from "service/errors";
import { Badge } from "./Badge";
import { Button } from "./Button";
import { Input } from "./Input";
import { Modal } from "./Modal";
import { PortList } from "./PortChips";
import { Select } from "./Select";

// "Detect an existing tunnel", in two steps. Step 1 looks at the node (and the
// relay, once its login is known) and reports what it finds; step 2 names it
// and records it. Detection and adoption only ever READ the servers - the
// panel then monitors the tunnel, it does not change or delete anything on it.

const Field: FC<{ label: string; children: ReactNode; className?: string }> = ({ label, children, className }) => (
  <label className={`flex min-w-0 flex-col gap-1 ${className ?? ""}`}>
    <span className="text-xs text-rapido-muted">{label}</span>
    {children}
  </label>
);

const ReadOnlyNote: FC = () => {
  const { t } = useTranslation();
  return (
    <div className="flex items-start gap-2 rounded-lg border border-sky-500/30 bg-sky-500/10 px-3 py-2 text-xs text-sky-300">
      <span aria-hidden="true">ⓘ</span>
      <span>{String(t("rapido.tunnels.detectReadOnly"))}</span>
    </div>
  );
};

const Chips: FC<{ items: readonly string[] }> = ({ items }) => (
  <div className="flex flex-wrap gap-1" dir="ltr">
    {items.map((a) => (
      <span key={a} className="rounded-md bg-rapido-raised px-1.5 py-0.5 text-[11px] text-rapido-text ring-1 ring-inset ring-white/10">
        {a}
      </span>
    ))}
  </div>
);

const Row: FC<{ label: string; children: ReactNode }> = ({ label, children }) => (
  <div className="grid grid-cols-[7.5rem_1fr] items-start gap-2 text-xs">
    <span className="text-rapido-muted">{label}</span>
    <div className="min-w-0 text-rapido-text">{children}</div>
  </div>
);

const ResultView: FC<{ result: TunnelDiscoverResult }> = ({ result }) => {
  const { t } = useTranslation();
  const L = (k: string) => String(t(k));
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-rapido-border bg-rapido-raised/30 p-3" data-testid="discover-result">
      {!result.found ? (
        <p className="text-sm text-amber-300">{L("rapido.tunnels.detectNotFound")}</p>
      ) : (
        <>
          <Row label={L("rapido.tunnels.protocol")}>
            <Badge tone="violet">{result.protocol}</Badge>
          </Row>
          {result.relay_addrs.length > 0 && (
            <Row label={L("rapido.tunnels.relayAddrs")}>
              <Chips items={result.relay_addrs} />
            </Row>
          )}
          {result.ports.length > 0 && (
            <Row label={L("rapido.tunnels.portsLabel")}>
              <PortList ports={result.ports} />
            </Row>
          )}
          <Row label="HAProxy">
            <Badge tone={result.haproxy ? "green" : "gray"}>
              {result.haproxy ? L("rapido.tunnels.haproxyFound") : L("rapido.tunnels.haproxyNotFound")}
            </Badge>
          </Row>
          {result.frp_transport && (
            <Row label={L("rapido.tunnels.transport")}>
              <span dir="ltr">{result.frp_transport}</span>
            </Row>
          )}
          {(result.node_nic || result.relay_nic) && (
            <Row label={L("rapido.tunnels.nics")}>
              <span dir="ltr">{[result.node_nic, result.relay_nic].filter(Boolean).join(" / ")}</span>
            </Row>
          )}
        </>
      )}
      {result.notes.length > 0 && (
        <ul className="list-disc space-y-0.5 ps-4 text-xs text-rapido-muted">
          {result.notes.map((n, i) => (
            <li key={i}>{n}</li>
          ))}
        </ul>
      )}
    </div>
  );
};

export const TunnelDiscoverModal: FC<{ onClose: () => void; onAdopted: () => void }> = ({ onClose, onAdopted }) => {
  const { t } = useTranslation();
  const L = (k: string, o?: Record<string, unknown>) => String(o ? t(k, o) : t(k));
  const { data: nodes } = useNodesQuery();
  const discover = useDiscoverTunnelMutation();
  const adopt = useAdoptTunnelMutation();

  const [step, setStep] = useState<1 | 2>(1);
  const [error, setError] = useState("");
  const [result, setResult] = useState<TunnelDiscoverResult | null>(null);
  const [name, setName] = useState("");
  const [node, setNode] = useState({ id: "", port: "22", user: "root", password: "" });
  const [relay, setRelay] = useState({ host: "", port: "22", user: "root", password: "" });

  const nodeName = (nodes ?? []).find((n) => String(n.id) === node.id)?.name ?? "";
  const relayLoginGiven = !!relay.host.trim() && !!relay.user.trim() && !!relay.password;
  const needsRelay = result !== null && result.found && !result.relay_read;

  const body = (withRelay: boolean): TunnelDiscoverPayload => ({
    node_id: Number(node.id),
    node_ssh_port: Number(node.port) || 22,
    node_ssh_user: node.user.trim(),
    node_ssh_password: node.password,
    ...(withRelay && relayLoginGiven
      ? {
          relay_host: relay.host.trim(),
          relay_ssh_port: Number(relay.port) || 22,
          relay_ssh_user: relay.user.trim(),
          relay_ssh_password: relay.password,
        }
      : {}),
  });

  const run = (withRelay: boolean) => {
    setError("");
    discover.mutate(body(withRelay), {
      onSuccess: (r) => {
        setResult(r);
        // Pre-fill the relay host from what was detected, once, so the admin
        // only has to type the login.
        if (r.found && !r.relay_read && !relay.host && r.relay_host) setRelay((p) => ({ ...p, host: r.relay_host as string }));
      },
      onError: (e) => setError(errorText(e, L("rapido.tunnels.detectFailed"))),
    });
  };

  const canDetect = !!node.id && !!node.user.trim() && !!node.password && !discover.isPending;
  const canContinue = result !== null && result.found && result.relay_read;

  const goToStep2 = () => {
    if (!result) return;
    if (!relay.host && result.relay_host) setRelay((p) => ({ ...p, host: result.relay_host as string }));
    if (!name) setName(`${nodeName || "node"}-detected`);
    setStep(2);
  };

  const canAdopt =
    !!name.trim() && !!relay.host.trim() && !!relay.user.trim() && !!relay.password && !adopt.isPending;

  const doAdopt = () => {
    setError("");
    adopt.mutate(
      {
        name: name.trim(),
        node_id: Number(node.id),
        node_ssh_port: Number(node.port) || 22,
        node_ssh_user: node.user.trim(),
        node_ssh_password: node.password,
        relay_host: relay.host.trim(),
        relay_ssh_port: Number(relay.port) || 22,
        relay_ssh_user: relay.user.trim(),
        relay_ssh_password: relay.password,
      },
      {
        onSuccess: onAdopted,
        onError: (e) => setError(errorText(e, L("rapido.tunnels.adoptFailed"))),
      }
    );
  };

  const relayFields = (
    <div className="flex flex-col gap-2">
      <Field label={L("rapido.tunnels.relayHost")}>
        <Input dir="ltr" value={relay.host} onChange={(e) => setRelay({ ...relay, host: e.target.value })} />
      </Field>
      <div className="grid grid-cols-3 gap-2">
        <Field label={L("rapido.tunnels.sshPort")}>
          <Input dir="ltr" type="number" inputMode="numeric" value={relay.port} onChange={(e) => setRelay({ ...relay, port: e.target.value })} />
        </Field>
        <Field label={L("rapido.tunnels.sshUser")}>
          <Input dir="ltr" value={relay.user} onChange={(e) => setRelay({ ...relay, user: e.target.value })} />
        </Field>
        <Field label={L("rapido.tunnels.sshPassword")}>
          <Input dir="ltr" type="password" autoComplete="new-password" value={relay.password} onChange={(e) => setRelay({ ...relay, password: e.target.value })} />
        </Field>
      </div>
    </div>
  );

  return (
    <Modal onClose={onClose} title={L("rapido.tunnels.detectTitle")} className="max-w-xl">
      <div className="flex max-h-[72vh] flex-col gap-4 overflow-y-auto pe-1">
        <div className="flex items-center gap-2 text-xs text-rapido-muted" aria-label={L("rapido.tunnels.detectStep", { step })}>
          <span className={step === 1 ? "font-semibold text-rapido-accent" : ""}>1 · {L("rapido.tunnels.detectStep1")}</span>
          <span aria-hidden="true">→</span>
          <span className={step === 2 ? "font-semibold text-rapido-accent" : ""}>2 · {L("rapido.tunnels.detectStep2")}</span>
        </div>

        <ReadOnlyNote />

        {step === 1 ? (
          <>
            <Field label={L("rapido.tunnels.node")}>
              <Select
                value={node.id}
                onChange={(e) => {
                  setNode({ ...node, id: e.target.value });
                  setResult(null);
                }}
              >
                <option value="">{L("rapido.tunnels.nodePlaceholder")}</option>
                {(nodes ?? []).map((n) => (
                  <option key={n.id} value={n.id}>
                    {n.name}
                  </option>
                ))}
              </Select>
            </Field>
            <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3">
              <span className="text-xs font-medium text-rapido-muted">{L("rapido.tunnels.nodeSection")}</span>
              <div className="grid grid-cols-3 gap-2">
                <Field label={L("rapido.tunnels.sshPort")}>
                  <Input dir="ltr" type="number" inputMode="numeric" value={node.port} onChange={(e) => setNode({ ...node, port: e.target.value })} />
                </Field>
                <Field label={L("rapido.tunnels.sshUser")}>
                  <Input dir="ltr" value={node.user} onChange={(e) => setNode({ ...node, user: e.target.value })} />
                </Field>
                <Field label={L("rapido.tunnels.sshPassword")}>
                  <Input dir="ltr" type="password" autoComplete="new-password" value={node.password} onChange={(e) => setNode({ ...node, password: e.target.value })} />
                </Field>
              </div>
            </div>

            {result && <ResultView result={result} />}

            {needsRelay && (
              <div className="flex flex-col gap-2 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3" data-testid="relay-login">
                <span className="text-xs font-medium text-amber-300">{L("rapido.tunnels.relayNotRead")}</span>
                {relayFields}
              </div>
            )}

            {error && (
              <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{error}</div>
            )}

            <div className="mt-1 flex flex-wrap justify-end gap-2">
              <Button variant="secondary" onClick={onClose}>
                {L("cancel")}
              </Button>
              {needsRelay ? (
                <Button variant="primary" disabled={!canDetect || !relayLoginGiven} onClick={() => run(true)}>
                  {discover.isPending ? L("rapido.pleaseWait") : L("rapido.tunnels.detectAgain")}
                </Button>
              ) : canContinue ? (
                <>
                  <Button variant="secondary" disabled={!canDetect} onClick={() => run(false)}>
                    {L("rapido.tunnels.detectAgain")}
                  </Button>
                  <Button variant="primary" onClick={goToStep2}>
                    {L("rapido.tunnels.continue")}
                  </Button>
                </>
              ) : (
                <Button variant="primary" disabled={!canDetect} onClick={() => run(false)}>
                  {discover.isPending ? L("rapido.pleaseWait") : L("rapido.tunnels.detect")}
                </Button>
              )}
            </div>
          </>
        ) : (
          <>
            {result && <ResultView result={result} />}
            <Field label={L("rapido.tunnels.name")}>
              <Input dir="ltr" value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3">
              <span className="text-xs font-medium text-rapido-muted">{L("rapido.tunnels.relaySection")}</span>
              {relayFields}
            </div>
            <p className="text-xs text-rapido-muted">{L("rapido.tunnels.adoptHint")}</p>

            {error && (
              <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{error}</div>
            )}

            <div className="mt-1 flex justify-end gap-2">
              <Button variant="secondary" onClick={() => setStep(1)} disabled={adopt.isPending}>
                {L("rapido.tunnels.back")}
              </Button>
              <Button variant="primary" disabled={!canAdopt} onClick={doAdopt}>
                {adopt.isPending ? L("rapido.pleaseWait") : L("rapido.tunnels.adopt")}
              </Button>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
};
