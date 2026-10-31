import { FC, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import classNames from "classnames";
import {
  useCreateGatewayPeerMutation,
  useDeleteGatewayPeerMutation,
  useGatewayPeersQuery,
  useGatewayRejectedQuery,
  useGatewaySettingsQuery,
  useRetryGatewayRejectedMutation,
  useTestGatewayPeerMutation,
  useUpdateGatewayPeerMutation,
  useUpdateGatewaySettingsMutation,
} from "hooks/useGatewayQuery";
import { GatewayPeer } from "types/Gateway";
import { errorText } from "service/errors";
import { Card, CardSubtitle, CardTitle } from "rapido-ui/Card";
import { Badge } from "rapido-ui/Badge";
import { Button } from "rapido-ui/Button";
import { Input } from "rapido-ui/Input";

// Same shape as NodesAdmin.tsx's own CopyField (not exported from there,
// so duplicated rather than reached across files for one small component -
// matches this codebase's existing convention of small per-file UI
// helpers, e.g. HostsAdmin.tsx's own `field` class string).
const CopyField: FC<{ label: string; value: string }> = ({ label, value }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const copy = () => {
    navigator.clipboard?.writeText(value).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 2000);
      },
      () => undefined
    );
  };

  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs font-medium text-rapido-text">{label}</span>
      <textarea
        readOnly
        dir="ltr"
        spellCheck={false}
        rows={2}
        className="w-full resize-none rounded-lg border border-rapido-border bg-rapido-bg px-3 py-2 font-mono text-xs text-rapido-text focus:outline-none focus:ring-1 focus:ring-rapido-accent"
        value={value}
        onFocus={(e) => e.currentTarget.select()}
      />
      <Button variant="chip" tone={copied ? "accent" : "neutral"} className="self-start" onClick={copy}>
        {copied ? t("usersTable.copied") : t("rapido.copy")}
      </Button>
    </div>
  );
};

// ---------------------------------------------------------------------------

// This panel's own identity - what an admin copies into a PEER panel's
// "add peer" form. Deliberately shown in full (see types/Gateway.ts's own
// doc comment) - masking would defeat the only thing this card exists for.
const ThisPanelCard: FC = () => {
  const { t } = useTranslation();
  const { data: settings, isLoading, isError, refetch } = useGatewaySettingsQuery();
  const [name, setName] = useState("");
  const [confirmRotate, setConfirmRotate] = useState(false);
  const update = useUpdateGatewaySettingsMutation();
  const [error, setError] = useState("");

  useEffect(() => {
    if (settings) setName(settings.name);
  }, [settings]);

  const nameDirty = settings !== undefined && name !== settings?.name;

  const saveName = () => {
    setError("");
    update.mutate({ name }, { onError: (e) => setError(errorText(e, t("rapido.gateway.saveFailed"))) });
  };

  const rotate = () => {
    setError("");
    setConfirmRotate(false);
    update.mutate({ rotate_secret: true }, { onError: (e) => setError(errorText(e, t("rapido.gateway.saveFailed"))) });
  };

  if (isError && !settings) {
    return <div role="alert" className="text-sm text-rapido-bad">
      <p>{t("rapido.gateway.settingsLoadFailed")}</p>
      <Button variant="chip" onClick={() => void refetch()}>{t("rapido.retry")}</Button>
    </div>;
  }
  if (isLoading || !settings) {
    return <p className="text-sm text-rapido-muted">{t("rapido.pleaseWait")}</p>;
  }

  return (
    <Card className="p-4">
      <CardTitle>{t("rapido.gateway.thisPanelTitle")}</CardTitle>
      <CardSubtitle className="mb-3">{t("rapido.gateway.thisPanelDesc")}</CardSubtitle>

      <div className="flex flex-col gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.gateway.panelName")}</span>
          <div className="flex flex-wrap items-center gap-2">
            <Input
              dir="ltr"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("rapido.gateway.panelNamePlaceholder")}
              className="max-w-xs"
            />
            <Button variant="chip" tone="accent" disabled={!nameDirty || update.isPending} onClick={saveName}>
              {t("rapido.gateway.saveName")}
            </Button>
          </div>
        </label>

        <CopyField label={t("rapido.gateway.thisSecret")} value={settings.secret} />

        {confirmRotate ? (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs text-red-400">{t("rapido.gateway.rotateWarning")}</span>
            <Button variant="chip" tone="red" disabled={update.isPending} onClick={rotate}>
              {t("rapido.gateway.rotateConfirm")}
            </Button>
            <Button variant="chip" onClick={() => setConfirmRotate(false)}>
              {t("cancel")}
            </Button>
          </div>
        ) : (
          <Button variant="chip" tone="amber" className="self-start" onClick={() => setConfirmRotate(true)}>
            {t("rapido.gateway.rotateSecret")}
          </Button>
        )}

        {error && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
            {error}
          </div>
        )}
      </div>
    </Card>
  );
};

// ---------------------------------------------------------------------------

const PeerForm: FC<{ initial?: GatewayPeer; onClose: () => void }> = ({ initial, onClose }) => {
  const { t } = useTranslation();
  const editing = !!initial;
  const [name, setName] = useState(initial?.name ?? "");
  const [baseURL, setBaseURL] = useState(initial?.base_url ?? "");
  const [secret, setSecret] = useState(initial?.secret ?? "");
  const [error, setError] = useState("");
  const create = useCreateGatewayPeerMutation();
  const update = useUpdateGatewayPeerMutation();
  const pending = create.isPending || update.isPending;

  const submit = () => {
    setError("");
    // A new peer starts off: switching one on hands it every user's credentials, which
    // deserves its own decision, after a look at what it offers.
    const body = { name, base_url: baseURL, secret, enabled: initial?.enabled ?? false };
    const onError = (e: unknown) => setError(errorText(e, t("rapido.gateway.saveFailed")));
    if (editing) {
      update.mutate({ id: initial!.id, body }, { onSuccess: onClose, onError });
    } else {
      create.mutate(body, { onSuccess: onClose, onError });
    }
  };

  const canSubmit = !!name && !!baseURL && !!secret;

  return (
    <Card className="p-4">
      <CardTitle>{editing ? t("rapido.gateway.editPeer") : t("rapido.gateway.addPeer")}</CardTitle>
      <div className="mt-3 flex flex-col gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.gateway.peerName")}</span>
          <Input dir="ltr" value={name} onChange={(e) => setName(e.target.value)} placeholder="EU Panel" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.gateway.peerBaseUrl")}</span>
          <Input
            dir="ltr"
            value={baseURL}
            onChange={(e) => setBaseURL(e.target.value)}
            placeholder="https://peer-panel.example.com"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.gateway.peerSecret")}</span>
          <Input
            dir="ltr"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder={t("rapido.gateway.peerSecretPlaceholder")}
          />
          <span className="text-xs text-rapido-muted">{t("rapido.gateway.peerSecretHint")}</span>
        </label>

        {error && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
            {error}
          </div>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="chip" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button variant="chip" tone="accent" disabled={!canSubmit || pending} onClick={submit}>
            {pending ? t("rapido.pleaseWait") : t("rapido.gateway.save")}
          </Button>
        </div>
      </div>
    </Card>
  );
};

// ---------------------------------------------------------------------------

// Who the peer refused and why, which only an admin can sort out, usually on that panel.
const RejectedUsers: FC<{ peerId: number }> = ({ peerId }) => {
  const { t } = useTranslation();
  const { data: users, isLoading, isError } = useGatewayRejectedQuery(peerId, true);
  const retry = useRetryGatewayRejectedMutation();
  if (isLoading) return <p className="text-xs text-rapido-muted">{t("rapido.pleaseWait")}</p>;
  if (isError) return <p className="text-xs text-red-400">{t("rapido.gateway.rejectedLoadFailed")}</p>;
  return (
    <div className="flex flex-col gap-2">
      <ul className="flex max-h-48 flex-col gap-1 overflow-y-auto text-xs">
        {(users ?? []).map((u) => (
          <li key={u.username} className="flex flex-wrap gap-x-2">
            <span dir="ltr" className="font-mono">{u.username}</span>
            <span className="text-rapido-muted">{u.detail}</span>
          </li>
        ))}
      </ul>
      <Button variant="chip" className="self-start" disabled={retry.isPending} onClick={() => retry.mutate(peerId)}>
        {t("rapido.gateway.retryRejected")}
      </Button>
    </div>
  );
};

const PeerHosts: FC<{ peer: GatewayPeer }> = ({ peer }) => {
  const { t } = useTranslation();
  if (!peer.hosts) return <p className="text-xs text-rapido-muted">{t("rapido.gateway.hostsUnknown")}</p>;
  if (peer.hosts.length === 0) return <p className="text-xs text-rapido-muted">{t("rapido.gateway.hostsNone")}</p>;
  return (
    <ul className="flex max-h-48 flex-col gap-1 overflow-y-auto text-xs">
      {peer.hosts.map((host, i) => (
        <li key={`${host.remark}-${i}`} className="flex flex-wrap items-center gap-x-2">
          <span className="font-medium">{host.remark}</span>
          <span dir="ltr" className="font-mono text-rapido-muted">
            {host.protocol}/{host.network} {host.security} {host.address}:{host.port}
          </span>
        </li>
      ))}
    </ul>
  );
};

const PeerStatus: FC<{ peer: GatewayPeer }> = ({ peer }) => {
  const { t } = useTranslation();
  const [show, setShow] = useState<false | "hosts" | "rejected">(false);
  const toggle = (what: "hosts" | "rejected") => setShow((s) => (s === what ? false : what));
  const percent = peer.total_users > 0 ? Math.min(100, Math.round((peer.synced_users * 100) / peer.total_users)) : 0;
  const settled = peer.synced_users + peer.rejected_users >= peer.total_users;

  return (
    <div className="mt-3 flex flex-col gap-2">
      {peer.last_error && (
        <div role="alert" className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">
          {peer.last_error}
        </div>
      )}
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-rapido-muted">
        <span>
          {peer.last_seen_at
            ? t("rapido.gateway.lastSeen", { time: new Date(peer.last_seen_at).toLocaleString() })
            : t("rapido.gateway.neverSeen")}
        </span>
        {peer.crowdedness !== null && <span>{t("rapido.gateway.crowdedness", { value: peer.crowdedness })}</span>}
      </div>
      {(peer.enabled || peer.synced_users > 0) && (
        <div className="flex flex-col gap-1">
          <div className="flex flex-wrap justify-between gap-2 text-xs">
            <span>
              {t("rapido.gateway.syncProgress", { synced: peer.synced_users, total: peer.total_users })}
              {peer.enabled && !settled ? ` · ${t("rapido.gateway.syncing")}` : ""}
            </span>
            {peer.rejected_users > 0 && (
              <span className="text-amber-400">{t("rapido.gateway.rejectedCount", { count: peer.rejected_users })}</span>
            )}
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-rapido-border">
            <div
              role="progressbar"
              aria-valuenow={percent}
              aria-valuemin={0}
              aria-valuemax={100}
              className="h-full bg-rapido-accent"
              style={{ width: `${percent}%` }}
            />
          </div>
        </div>
      )}
      <div className="flex flex-wrap gap-1.5">
        <Button variant="chip" onClick={() => toggle("hosts")}>
          {t("rapido.gateway.showHosts", { count: peer.hosts?.length ?? 0 })}
        </Button>
        {peer.rejected_users > 0 && (
          <Button variant="chip" tone="amber" onClick={() => toggle("rejected")}>
            {t("rapido.gateway.showRejected")}
          </Button>
        )}
      </div>
      {show === "hosts" && <PeerHosts peer={peer} />}
      {show === "rejected" && <RejectedUsers peerId={peer.id} />}
    </div>
  );
};

const PeerRow: FC<{ peer: GatewayPeer; onEdit: () => void; onCopiesLeft: (name: string) => void }> = ({ peer, onEdit, onCopiesLeft }) => {
  const { t } = useTranslation();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmToggle, setConfirmToggle] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [deleteError, setDeleteError] = useState("");
  const [toggleError, setToggleError] = useState("");
  const deletePeer = useDeleteGatewayPeerMutation();
  const testPeer = useTestGatewayPeerMutation();
  const updatePeer = useUpdateGatewayPeerMutation();

  const remove = () => {
    setDeleteError("");
    deletePeer.mutate(peer.id, {
      onSuccess: (result) => {
        if (result?.copies_left) onCopiesLeft(peer.name);
      },
      onError: (e) => {
        setDeleteError(errorText(e, t("rapido.gateway.deleteFailed")));
        setConfirmDelete(false);
      },
    });
  };

  const toggle = () => {
    setToggleError("");
    setConfirmToggle(false);
    updatePeer.mutate(
      { id: peer.id, body: { name: peer.name, base_url: peer.base_url, secret: peer.secret, enabled: !peer.enabled } },
      { onError: (e) => setToggleError(errorText(e, t("rapido.gateway.saveFailed"))) }
    );
  };

  const runTest = () => {
    setTestResult(null);
    testPeer.mutate(peer.id, {
      onSuccess: (result) => {
        if (!result.ok) {
          setTestResult({ ok: false, text: t("rapido.gateway.testFailed", { detail: result.detail || "" }) });
          return;
        }
        let text = t("rapido.gateway.testOk", { name: result.panel_name || peer.name });
        // crowdedness/host_count are best-effort (sub-phase 3's status call
        // riding along with the ping) - absent, not zero, when that second
        // call itself failed, so only append them when actually present.
        if (result.crowdedness !== undefined && result.host_count !== undefined) {
          text += " " + t("rapido.gateway.testStatus", { crowdedness: result.crowdedness, hosts: result.host_count });
        }
        setTestResult({ ok: true, text });
      },
      onError: (e) => setTestResult({ ok: false, text: errorText(e, t("rapido.gateway.testFailed", { detail: "" })) }),
    });
  };

  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="truncate text-sm font-semibold">{peer.name}</span>
          <Badge tone="sky" dir="ltr">
            {peer.base_url}
          </Badge>
          {!peer.enabled && <Badge tone="gray">{t("rapido.gateway.disabled")}</Badge>}
        </div>
      </div>

      <PeerStatus peer={peer} />

      {testResult && (
        <div
          className={classNames(
            "mt-2 rounded-lg border px-3 py-2 text-xs",
            testResult.ok
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-400"
              : "border-red-500/30 bg-red-500/10 text-red-400"
          )}
        >
          {testResult.text}
        </div>
      )}

      {(deleteError || toggleError) && (
        <div className="mt-2 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">
          {deleteError || toggleError}
        </div>
      )}

      {confirmToggle && (
        <div className="mt-3 flex flex-col gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs">
          <span>
            {peer.enabled
              ? t("rapido.gateway.disableWarning")
              : t("rapido.gateway.enableWarning", { count: peer.total_users, hosts: peer.hosts?.length ?? 0 })}
          </span>
          <div className="flex flex-wrap gap-1.5">
            <Button variant="chip" tone={peer.enabled ? "amber" : "accent"} disabled={updatePeer.isPending} onClick={toggle}>
              {peer.enabled ? t("rapido.gateway.disableConfirm") : t("rapido.gateway.enableConfirm")}
            </Button>
            <Button variant="chip" onClick={() => setConfirmToggle(false)}>
              {t("cancel")}
            </Button>
          </div>
        </div>
      )}

      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        {!confirmToggle && (
          <Button variant="chip" tone={peer.enabled ? "amber" : "accent"} onClick={() => setConfirmToggle(true)}>
            {peer.enabled ? t("rapido.gateway.disable") : t("rapido.gateway.enable")}
          </Button>
        )}
        <Button variant="chip" disabled={testPeer.isPending} onClick={runTest}>
          {testPeer.isPending ? t("rapido.pleaseWait") : t("rapido.gateway.testConnection")}
        </Button>
        <Button variant="chip" onClick={onEdit}>
          {t("rapido.gateway.edit")}
        </Button>
        {confirmDelete ? (
          <>
            <span className="text-xs text-red-400">{t("rapido.gateway.deleteWarning")}</span>
            <Button variant="chip" tone="red" disabled={deletePeer.isPending} onClick={remove}>
              {t("delete")}
            </Button>
            <Button variant="chip" onClick={() => setConfirmDelete(false)}>
              {t("cancel")}
            </Button>
          </>
        ) : (
          <Button variant="chip" tone="red" onClick={() => setConfirmDelete(true)}>
            {t("delete")}
          </Button>
        )}
      </div>
    </Card>
  );
};

// ---------------------------------------------------------------------------

export const GatewayAdmin: FC = () => {
  const { t } = useTranslation();
  const { data: peers, isLoading, isError } = useGatewayPeersQuery();
  const [formTarget, setFormTarget] = useState<false | "new" | GatewayPeer>(false);
  const [copiesLeftOn, setCopiesLeftOn] = useState("");

  return (
    <div className="flex flex-col gap-4">
      <ThisPanelCard />

      {copiesLeftOn && (
        <div role="alert" className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-400">
          {t("rapido.gateway.copiesLeft", { name: copiesLeftOn })}
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-sm text-rapido-muted">{t("rapido.gateway.peersSummary", { count: peers?.length ?? 0 })}</div>
        <Button variant="chip" tone="accent" onClick={() => setFormTarget("new")}>
          + {t("rapido.gateway.addPeer")}
        </Button>
      </div>

      {isError && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {t("rapido.gateway.loadFailed")}
        </div>
      )}

      {/* The key: the form copies its target into state once, so moving from
          one peer's Edit to another's must start a fresh form, or saving would
          write the first peer's values onto the second. */}
      {formTarget !== false && (
        <PeerForm
          key={formTarget === "new" ? "new" : formTarget.id}
          initial={formTarget === "new" ? undefined : formTarget}
          onClose={() => setFormTarget(false)}
        />
      )}

      {isLoading ? (
        <p className="text-sm text-rapido-muted">{t("rapido.pleaseWait")}</p>
      ) : isError ? null : (peers ?? []).length === 0 ? (
        <Card className="p-6 text-center text-sm text-rapido-muted">{t("rapido.gateway.empty")}</Card>
      ) : (
        <div className="flex flex-col gap-3">
          {(peers ?? []).map((p) => (
            <PeerRow key={p.id} peer={p} onEdit={() => setFormTarget(p)} onCopiesLeft={setCopiesLeftOn} />
          ))}
        </div>
      )}
    </div>
  );
};

export default GatewayAdmin;
