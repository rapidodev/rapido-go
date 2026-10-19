import { FC, ReactNode, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { useNodesQuery, useRestartNodeMutation } from "hooks/useNodesQuery";
import { Node, NodeRestartMode, NodeRestartResult } from "types/Node";
import { errorText } from "service/errors";
import { REBOOT_TIMEOUT_MS, formatTook, rebootWaitState, restartConfirmed } from "utils/nodeRestart";
import { Button } from "./Button";
import { Input } from "./Input";
import { Modal } from "./Modal";
import { StatusPill } from "./StatusPill";

// Restart a node: two clearly different choices - restart the agent (short,
// low risk) or reboot the whole server (long, high risk, red, and gated by
// typing the node's name). SSH details are asked for only when the panel holds
// no login for this node, and are used once. After a reboot the dialog keeps
// polling the node list until the node reports back or a timeout passes.

const Choice: FC<{
  selected: boolean;
  danger?: boolean;
  title: string;
  risk: string;
  desc: string;
  onPick: () => void;
}> = ({ selected, danger, title, risk, desc, onPick }) => (
  <button
    type="button"
    role="radio"
    aria-checked={selected}
    onClick={onPick}
    className={classNames(
      "flex flex-col gap-1.5 rounded-xl2 border p-3 text-start transition-colors focus:outline-none focus-visible:ring-2",
      danger ? "focus-visible:ring-rapido-bad/50" : "focus-visible:ring-rapido-accent/50",
      selected
        ? danger
          ? "border-rapido-bad bg-rapido-bad/10"
          : "border-rapido-accent bg-rapido-accent/10"
        : "border-rapido-border hover:border-rapido-muted"
    )}
  >
    <span className="flex flex-wrap items-center gap-2">
      <span className={classNames("text-sm font-semibold", danger ? "text-rapido-bad" : "text-rapido-text")}>{title}</span>
      <StatusPill tone={danger ? "red" : "green"} className="px-2 py-0.5 text-[11px]">
        {risk}
      </StatusPill>
    </span>
    <span className="text-xs leading-relaxed text-rapido-muted">{desc}</span>
  </button>
);

const Field: FC<{ label: string; children: ReactNode }> = ({ label, children }) => (
  <label className="flex min-w-0 flex-col gap-1">
    <span className="text-xs text-rapido-muted">{label}</span>
    {children}
  </label>
);

export const RestartNodeModal: FC<{ node: Pick<Node, "id" | "name" | "ssh_known">; onClose: () => void }> = ({ node, onClose }) => {
  const { t } = useTranslation();
  const L = (k: string, o?: Record<string, unknown>) => String(o ? t(k, o) : t(k));
  const restart = useRestartNodeMutation();
  const [mode, setMode] = useState<NodeRestartMode>("service");
  const [typed, setTyped] = useState("");
  const [ssh, setSsh] = useState({ user: "root", port: "22", password: "" });
  const [error, setError] = useState("");
  const [result, setResult] = useState<NodeRestartResult | null>(null);

  const needsSsh = node.ssh_known === false;
  const confirmed = restartConfirmed(mode, node.name, typed);
  const sshOk = !needsSsh || (!!ssh.user.trim() && !!ssh.password);
  const rebooting = result?.ok === true && mode === "reboot";

  // After a reboot: poll the node list until it reports back.
  const startedAt = useRef(0);
  const [elapsed, setElapsed] = useState(0);
  const sawDown = useRef(false);
  const { data: nodes, refetch } = useNodesQuery();
  const status = nodes?.find((n) => n.id === node.id)?.status;
  if (rebooting && status && status !== "connected") sawDown.current = true;
  const wait = rebooting ? rebootWaitState({ elapsedMs: elapsed, status, sawDown: sawDown.current }) : "waiting";

  useEffect(() => {
    if (!rebooting || wait !== "waiting") return;
    if (!startedAt.current) startedAt.current = Date.now();
    const timer = window.setInterval(() => {
      setElapsed(Date.now() - startedAt.current);
      void refetch();
    }, 4000);
    return () => window.clearInterval(timer);
  }, [rebooting, wait, refetch]);

  const submit = () => {
    setError("");
    restart.mutate(
      {
        id: node.id,
        body: {
          mode,
          ...(mode === "reboot" ? { confirm_name: typed } : {}),
          ...(needsSsh
            ? { ssh_user: ssh.user.trim(), ssh_port: Number(ssh.port) || 22, ssh_password: ssh.password }
            : {}),
        },
      },
      {
        onSuccess: (r) => {
          setResult(r);
          if (!r.ok) setError(r.detail || L("rapido.nodes.restart.failed"));
        },
        onError: (e) => setError(errorText(e, L("rapido.nodes.restart.failed"))),
      }
    );
  };

  const done = result?.ok === true;
  const busy = restart.isPending;

  return (
    <Modal onClose={busy ? () => {} : onClose} title={L("rapido.nodes.restart.title", { name: node.name })} className="max-w-xl">
      <div className="flex max-h-[72vh] flex-col gap-4 overflow-y-auto pe-1">
        {!done && (
          <>
            <div role="radiogroup" aria-label={L("rapido.nodes.restart.chooseMode")} className="grid gap-2.5 sm:grid-cols-2">
              <Choice
                selected={mode === "service"}
                title={L("rapido.nodes.restart.service.title")}
                risk={L("rapido.nodes.restart.service.risk")}
                desc={L("rapido.nodes.restart.service.desc")}
                onPick={() => setMode("service")}
              />
              <Choice
                danger
                selected={mode === "reboot"}
                title={L("rapido.nodes.restart.reboot.title")}
                risk={L("rapido.nodes.restart.reboot.risk")}
                desc={L("rapido.nodes.restart.reboot.desc")}
                onPick={() => setMode("reboot")}
              />
            </div>

            {needsSsh && (
              <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3" data-testid="restart-ssh">
                <span className="text-xs font-medium text-rapido-muted">{L("rapido.nodes.restart.ssh.title")}</span>
                <p className="text-xs text-rapido-muted">{L("rapido.nodes.restart.ssh.hint")}</p>
                <div className="grid grid-cols-3 gap-2">
                  <Field label={L("rapido.nodes.restart.ssh.port")}>
                    <Input dir="ltr" type="number" inputMode="numeric" value={ssh.port} onChange={(e) => setSsh({ ...ssh, port: e.target.value })} />
                  </Field>
                  <Field label={L("rapido.nodes.restart.ssh.user")}>
                    <Input dir="ltr" value={ssh.user} onChange={(e) => setSsh({ ...ssh, user: e.target.value })} />
                  </Field>
                  <Field label={L("rapido.nodes.restart.ssh.password")}>
                    <Input dir="ltr" type="password" autoComplete="new-password" value={ssh.password} onChange={(e) => setSsh({ ...ssh, password: e.target.value })} />
                  </Field>
                </div>
              </div>
            )}

            {mode === "reboot" && (
              <Field label={L("rapido.nodes.restart.confirmName", { name: node.name })}>
                <Input
                  dir="ltr"
                  hasError={typed !== "" && typed !== node.name}
                  placeholder={L("rapido.nodes.restart.confirmNamePlaceholder")}
                  value={typed}
                  onChange={(e) => setTyped(e.target.value)}
                />
              </Field>
            )}
          </>
        )}

        {busy && (
          <div role="status" className="flex items-center gap-2 rounded-lg border border-rapido-border bg-rapido-raised/40 px-3 py-2 text-sm text-rapido-muted">
            <span aria-hidden="true" className="h-4 w-4 animate-spin rounded-full border-2 border-rapido-accent/30 border-t-rapido-accent" />
            {L("rapido.nodes.restart.working")}
          </div>
        )}

        {done && (
          <div role="status" className="flex flex-col gap-2 rounded-lg border border-rapido-good/40 bg-rapido-good/10 px-3 py-3 text-sm text-rapido-good">
            <span className="font-medium">
              {mode === "service"
                ? L("rapido.nodes.restart.resultService", { time: formatTook(result?.took_ms) })
                : L("rapido.nodes.restart.resultReboot")}
            </span>
            {result?.detail && (
              <span className="text-xs text-rapido-muted" dir="ltr">
                {result.detail}
              </span>
            )}
            {mode === "service" && result?.active !== undefined && (
              <span className="text-xs">
                {result.active ? L("rapido.nodes.restart.resultActive") : L("rapido.nodes.restart.resultNotActive")}
              </span>
            )}
          </div>
        )}

        {rebooting && (
          <div
            role="status"
            data-testid="reboot-wait"
            data-wait={wait}
            className={classNames(
              "flex items-center gap-2 rounded-lg border px-3 py-2 text-sm",
              wait === "back" ? "border-rapido-good/40 text-rapido-good" : wait === "timeout" ? "border-rapido-warn/40 text-rapido-warn" : "border-rapido-border text-rapido-muted"
            )}
          >
            {wait === "waiting" && (
              <span aria-hidden="true" className="h-4 w-4 shrink-0 animate-spin rounded-full border-2 border-rapido-accent/30 border-t-rapido-accent" />
            )}
            {wait === "waiting" && L("rapido.nodes.restart.waiting", { time: `${Math.floor(elapsed / 1000)} s` })}
            {wait === "back" && L("rapido.nodes.restart.back")}
            {wait === "timeout" && L("rapido.nodes.restart.timeout", { minutes: Math.round(REBOOT_TIMEOUT_MS / 60000) })}
          </div>
        )}

        {error && (
          <div role="alert" className="rounded-lg border border-rapido-bad/30 bg-rapido-bad/10 px-3 py-2 text-sm text-rapido-bad">
            {error}
          </div>
        )}

        <div className="mt-1 flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            {done ? L("rapido.nodes.restart.close") : L("cancel")}
          </Button>
          {!done && (
            <Button
              variant="primary"
              disabled={busy || !confirmed || !sshOk}
              onClick={submit}
              className={mode === "reboot" ? "!bg-rapido-bad !bg-none !text-white !shadow-none" : undefined}
            >
              {busy ? L("rapido.nodes.restart.working") : mode === "reboot" ? L("rapido.nodes.restart.confirmReboot") : L("rapido.nodes.restart.confirmService")}
            </Button>
          )}
        </div>
      </div>
    </Modal>
  );
};

/** The "Restart node" action: a chip button that opens the dialog (portalled, so a tilted card cannot trap it). */
export const RestartNodeButton: FC<{ node: Pick<Node, "id" | "name" | "ssh_known">; className?: string }> = ({ node, className }) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="chip" tone="amber" className={className} onClick={() => setOpen(true)}>
        {String(t("rapido.nodes.restart.button"))}
      </Button>
      {open && createPortal(<RestartNodeModal node={node} onClose={() => setOpen(false)} />, document.body)}
    </>
  );
};
