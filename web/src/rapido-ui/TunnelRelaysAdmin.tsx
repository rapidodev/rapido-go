import { FC, useState } from "react";
import { useTranslation } from "react-i18next";
import classNames from "classnames";
import {
  useCreateTunnelRelayMutation,
  useDeleteTunnelRelayMutation,
  useTunnelRelaysQuery,
} from "hooks/useTunnelRelaysQuery";
import { TunnelRelay, TunnelRelayWritePayload } from "types/TunnelRelay";
import { errorText } from "service/errors";
import { Card } from "rapido-ui/Card";
import { Badge, BadgeTone } from "rapido-ui/Badge";
import { Button } from "rapido-ui/Button";
import { Input } from "rapido-ui/Input";
import { Modal } from "rapido-ui/Modal";

// Full create+delete (no edit - the backend has no PUT for a tunnel relay,
// only POST/DELETE) for external GRE+FRP relay boxes, following the same
// list-of-cards + modal shape as NodesAdmin.tsx/HostsAdmin.tsx. Unlike
// those, status here is read-only and lives on the server: relayhealth's
// probe loop (internal/relayhealth) publishes up/down/checked_at, this page
// just displays it and polls at the same 30s cadence the prober itself uses.

const cardToneClasses: Partial<Record<BadgeTone, string>> = {
  green: "!border-emerald-500/60 bg-emerald-500/[0.04]",
  red: "!border-red-500/60 bg-red-500/[0.04]",
};

const agoLabelKey = {
  seconds: "rapido.monitoring.agoSeconds",
  minutes: "rapido.monitoring.agoMinutes",
  hours: "rapido.monitoring.agoHours",
  days: "rapido.monitoring.agoDays",
} as const;

// Reuses the exact "N <unit> ago" phrasing/translation keys the Monitoring
// page already established for tunnel handshake age - the shape of the
// fact (how long since something was last confirmed) is the same, just
// measured from an ISO timestamp here instead of a seconds-elapsed field.
const agoText = (t: (key: string, opts?: Record<string, unknown>) => string, checkedAt: string): string => {
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(checkedAt).getTime()) / 1000));
  if (seconds < 60) return t(agoLabelKey.seconds, { value: seconds });
  if (seconds < 3600) return t(agoLabelKey.minutes, { value: Math.floor(seconds / 60) });
  if (seconds < 86400) return t(agoLabelKey.hours, { value: Math.floor(seconds / 3600) });
  return t(agoLabelKey.days, { value: Math.floor(seconds / 86400) });
};

const relayTone = (relay: TunnelRelay): BadgeTone =>
  relay.up === null ? "gray" : relay.up ? "green" : "red";

const relayStatusLabel = (
  t: (key: string, opts?: Record<string, unknown>) => string,
  relay: TunnelRelay
): string => {
  if (relay.up === null) return t("rapido.tunnelRelays.statusUnknown");
  return t(relay.up ? "rapido.tunnelRelays.statusUp" : "rapido.tunnelRelays.statusDown");
};

// ---------------------------------------------------------------------------

type FormValues = { name: string; host: string; port: string };
const emptyForm = (): FormValues => ({ name: "", host: "", port: "" });

const TunnelRelayFormModal: FC<{ onClose: () => void; onCreated: () => void }> = ({
  onClose,
  onCreated,
}) => {
  const { t } = useTranslation();
  const [values, setValues] = useState<FormValues>(emptyForm());
  const [error, setError] = useState("");
  const createRelay = useCreateTunnelRelayMutation();

  const set = (patch: Partial<FormValues>) => setValues((v) => ({ ...v, ...patch }));

  const port = Number(values.port);
  const canSubmit =
    !!values.name.trim() && !!values.host.trim() && Number.isInteger(port) && port > 0 && port <= 65535;

  const submit = () => {
    setError("");
    const body: TunnelRelayWritePayload = {
      name: values.name.trim(),
      host: values.host.trim(),
      port,
    };
    createRelay.mutate(body, {
      onSuccess: onCreated,
      onError: (e) => setError(errorText(e, t("rapido.tunnelRelays.saveFailed"))),
    });
  };

  return (
    <Modal onClose={onClose} title={t("rapido.tunnelRelays.addTitle")}>
      <div className="flex flex-col gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnelRelays.name")}</span>
          <Input
            dir="ltr"
            placeholder={t("rapido.tunnelRelays.namePlaceholder")}
            value={values.name}
            onChange={(e) => set({ name: e.target.value })}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnelRelays.host")}</span>
          <Input dir="ltr" value={values.host} onChange={(e) => set({ host: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnelRelays.port")}</span>
          <Input
            dir="ltr"
            type="number"
            inputMode="numeric"
            value={values.port}
            onChange={(e) => set({ port: e.target.value })}
          />
          <span className="text-xs text-rapido-muted">{t("rapido.tunnelRelays.portHint")}</span>
        </label>

        {error && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
            {error}
          </div>
        )}

        <div className="mt-2 flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} disabled={createRelay.isPending}>
            {t("cancel")}
          </Button>
          <Button variant="primary" disabled={createRelay.isPending || !canSubmit} onClick={submit}>
            {createRelay.isPending ? t("rapido.pleaseWait") : t("rapido.tunnelRelays.addTitle")}
          </Button>
        </div>
      </div>
    </Modal>
  );
};

// ---------------------------------------------------------------------------

const TunnelRelayCard: FC<{ relay: TunnelRelay }> = ({ relay }) => {
  const { t } = useTranslation();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const deleteRelay = useDeleteTunnelRelayMutation();
  const tone = relayTone(relay);

  const remove = () => {
    setMsg(null);
    deleteRelay.mutate(relay.id, {
      onError: (e) => {
        setMsg(errorText(e, t("rapido.tunnelRelays.deleteFailed")));
        setConfirmDelete(false);
      },
    });
  };

  return (
    <Card className={classNames("p-4", cardToneClasses[tone])}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-semibold">{relay.name}</span>
            <Badge tone={tone} title={relay.error || undefined}>
              {relayStatusLabel(t, relay)}
            </Badge>
          </div>
        </div>

        <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-rapido-muted">
          <span>
            {t("rapido.tunnelRelays.host")}:{" "}
            <span className="text-rapido-text" dir="ltr">
              {relay.host}
            </span>
          </span>
          <span>
            {t("rapido.tunnelRelays.port")}:{" "}
            <span className="text-rapido-text" dir="ltr">
              {relay.port}
            </span>
          </span>
          {relay.checked_at && (
            <span>
              {t("rapido.tunnelRelays.lastChecked", { time: agoText(t, relay.checked_at) })}
            </span>
          )}
        </div>

        {relay.error && (
          <div className="break-words text-xs text-rapido-muted" title={relay.error}>
            <span dir="ltr" className="text-red-300/80">
              {relay.error}
            </span>
          </div>
        )}

        {msg && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">
            {msg}
          </div>
        )}

        <div className="flex flex-wrap items-center gap-1.5">
          {confirmDelete ? (
            <>
              <span className="text-xs text-red-400">{t("rapido.tunnelRelays.confirmDelete")}</span>
              <Button variant="chip" tone="red" disabled={deleteRelay.isPending} onClick={remove}>
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
      </div>
    </Card>
  );
};

// ---------------------------------------------------------------------------

export const TunnelRelaysAdmin: FC = () => {
  const { t } = useTranslation();
  const { data: relays, isLoading, isError } = useTunnelRelaysQuery();
  const [adding, setAdding] = useState(false);

  const rows = relays ?? [];
  const up = rows.filter((r) => r.up === true).length;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-sm text-rapido-muted">
          {t("rapido.tunnelRelays.summary", { up, total: rows.length })}
        </div>
        <Button variant="chip" tone="accent" onClick={() => setAdding(true)}>
          + {t("rapido.tunnelRelays.addNew")}
        </Button>
      </div>

      {isError && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {t("rapido.tunnelRelays.loadFailed")}
        </div>
      )}

      {isLoading ? (
        <p className="text-sm text-rapido-muted">{t("rapido.tickets.loading")}</p>
      ) : rows.length === 0 ? (
        <Card className="p-6 text-center text-sm text-rapido-muted">{t("rapido.tunnelRelays.empty")}</Card>
      ) : (
        <div className="grid gap-3 lg:grid-cols-2">
          {rows.map((relay) => (
            <TunnelRelayCard key={relay.id} relay={relay} />
          ))}
        </div>
      )}

      {adding && (
        <TunnelRelayFormModal onClose={() => setAdding(false)} onCreated={() => setAdding(false)} />
      )}
    </div>
  );
};

export default TunnelRelaysAdmin;
