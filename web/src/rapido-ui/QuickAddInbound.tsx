import { FC, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCreateInboundMutation } from "hooks/useInboundsQuery";
import { CreateInboundPayload } from "types/Inbound";
import { errorText } from "service/errors";
import { Button } from "rapido-ui/Button";
import { Input } from "rapido-ui/Input";
import { Modal } from "rapido-ui/Modal";
import { Select } from "rapido-ui/Select";

// Two one-shot ways to get an inbound onto the panel, both built on
// POST /api/inbounds (hooks/useInboundsQuery.ts's useCreateInboundMutation):
// a single form (QuickAddInboundModal) and a "every protocol sing-box
// supports, right now" bulk action (AddAllProtocolsButton). Neither ever
// asks for a certificate or a PSK - the server auto-generates whichever one
// a given protocol needs (internal/httpapi/inbounds.go's
// handleCreateInbound), which is what makes "one click, it just works"
// possible instead of this needing a Reality/TLS-material wizard.
//
// Deliberately excludes Reality: it needs a real key pair and short IDs an
// admin has to actually manage (unlike a self-signed cert or a random PSK,
// there's no "generate and forget" default that still makes sense), so it
// stays a job for the full JSON editor/Xray import on this same page, not
// this quick-create surface.

export type ProtocolOption =
  | "vmess"
  | "vless"
  | "trojan"
  | "shadowsocks"
  | "hysteria2"
  | "tuic"
  | "anytls"
  | "snell"
  | "hysteria"
  | "naive"
  | "shadowtls";

// What security a protocol can carry, and the sensible unattended default
// for the bulk action. hysteria2/tuic/anytls/hysteria are TLS-mandatory at
// the sing-box level (syncInboundEntries's own check); snell has no TLS
// concept at all (internal/nodecore/snell's own doc comment) - both ends of
// this table are "forced", not just "defaulted", which is why PROTOCOLS
// also carries whether the single-create form's own security choice is
// locked. bulkEligible is false only for hysteria (v1): unlike every other
// field this form auto-generates (a cert, a PSK), its required up_mbps/
// down_mbps have no "generate and forget" default that still makes sense -
// a wrong guess makes the protocol connect but be unusably slow/broken, not
// obviously rejected - so the unattended bulk action leaves it out and only
// the single-create form (where the admin types real numbers) offers it.
//
// shadowtls is "none"/locked for the same reason as snell: this table's
// security/tls_* columns describe a certificate the SERVER inbound would
// terminate itself, and ShadowTLS never does that (it relays a real TLS
// handshake instead - see internal/nodecore/shadowtls's own doc comment),
// so there is nothing for this table's own TLS machinery to hold. The
// subscription format's own TLS block (the disguise SNI a client dials)
// comes from the HOST's security instead - a separate, later step on the
// Hosts page, same as picking a real domain for any other protocol.
const PROTOCOLS: { value: ProtocolOption; security: "none" | "tls"; securityLocked: boolean; bulkEligible: boolean }[] = [
  { value: "vmess", security: "tls", securityLocked: false, bulkEligible: true },
  { value: "vless", security: "tls", securityLocked: false, bulkEligible: true },
  { value: "trojan", security: "tls", securityLocked: false, bulkEligible: true },
  { value: "shadowsocks", security: "none", securityLocked: false, bulkEligible: true },
  { value: "hysteria2", security: "tls", securityLocked: true, bulkEligible: true },
  { value: "tuic", security: "tls", securityLocked: true, bulkEligible: true },
  { value: "anytls", security: "tls", securityLocked: true, bulkEligible: true },
  { value: "snell", security: "none", securityLocked: true, bulkEligible: true },
  { value: "hysteria", security: "tls", securityLocked: true, bulkEligible: false },
  { value: "naive", security: "tls", securityLocked: false, bulkEligible: true },
  { value: "shadowtls", security: "none", securityLocked: true, bulkEligible: true },
];

const BULK_PROTOCOLS = PROTOCOLS.filter((p) => p.bulkEligible);

const protocolMeta = (p: ProtocolOption) => PROTOCOLS.find((e) => e.value === p)!;

// ---------------------------------------------------------------------------

type FormValues = {
  tag: string;
  protocol: ProtocolOption;
  port: string;
  security: "none" | "tls";
  upMbps: string;
  downMbps: string;
  shadowtlsInnerMethod: string;
  shadowtlsInnerPassword: string;
};

// The three shadowsocks-2022 AEAD methods this fork's embedded inner layer
// accepts (internal/httpapi/inbounds.go's shadowTLSInnerKeyLength) - AES-GCM
// first since that is also the server's own one-click default (AES-NI is
// available on effectively every modern server CPU).
const SHADOWTLS_INNER_METHODS = ["2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"];

const defaultTag = (protocol: ProtocolOption) => `${protocol}-main`;

export const QuickAddInboundModal: FC<{ onClose: () => void; onCreated: () => void }> = ({
  onClose,
  onCreated,
}) => {
  const { t } = useTranslation();
  const [values, setValues] = useState<FormValues>({
    tag: defaultTag("vmess"),
    protocol: "vmess",
    port: "",
    security: "tls",
    upMbps: "",
    downMbps: "",
    shadowtlsInnerMethod: SHADOWTLS_INNER_METHODS[0],
    shadowtlsInnerPassword: "",
  });
  const [error, setError] = useState("");
  const createInbound = useCreateInboundMutation();

  const setProtocol = (protocol: ProtocolOption) => {
    const meta = protocolMeta(protocol);
    setValues((v) => ({
      ...v,
      protocol,
      // Only replace the tag if it still looks auto-filled (the admin
      // hasn't typed their own) - mirrors how most "slug follows the name"
      // form fields behave.
      tag: v.tag === defaultTag(v.protocol) ? defaultTag(protocol) : v.tag,
      security: meta.security,
    }));
  };

  const port = Number(values.port);
  const upMbps = Number(values.upMbps);
  const downMbps = Number(values.downMbps);
  const isHysteria = values.protocol === "hysteria";
  const isShadowtls = values.protocol === "shadowtls";
  const canSubmit =
    !!values.tag.trim() &&
    Number.isInteger(port) &&
    port > 0 &&
    port <= 65535 &&
    (!isHysteria || (upMbps > 0 && downMbps > 0));

  const submit = () => {
    setError("");
    const body: CreateInboundPayload = {
      tag: values.tag.trim(),
      protocol: values.protocol,
      port,
      security: values.security,
      ...(isHysteria ? { up_mbps: upMbps, down_mbps: downMbps } : {}),
      ...(isShadowtls ? { shadowtls_inner_method: values.shadowtlsInnerMethod } : {}),
      // Left out entirely (not sent as an empty string) when the admin
      // didn't type one, so the server's own random-key generation
      // (handleCreateInbound) still kicks in - same contract the omitted
      // snell_psk/hysteria_obfs_password already rely on.
      ...(isShadowtls && values.shadowtlsInnerPassword.trim()
        ? { shadowtls_inner_password: values.shadowtlsInnerPassword.trim() }
        : {}),
    };
    createInbound.mutate(body, {
      onSuccess: onCreated,
      onError: (e) => setError(errorText(e, t("rapido.inbounds.quickAdd.saveFailed"))),
    });
  };

  const meta = protocolMeta(values.protocol);

  return (
    <Modal onClose={onClose} title={t("rapido.inbounds.quickAdd.addTitle")}>
      <div className="flex flex-col gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.inbounds.protocol")}</span>
          <Select
            className="w-full"
            dir="ltr"
            value={values.protocol}
            onChange={(e) => setProtocol(e.target.value as ProtocolOption)}
          >
            {PROTOCOLS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.value}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.inbounds.tag")}</span>
          <Input dir="ltr" value={values.tag} onChange={(e) => setValues((v) => ({ ...v, tag: e.target.value }))} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.inbounds.quickAdd.port")}</span>
          <Input
            dir="ltr"
            type="number"
            inputMode="numeric"
            value={values.port}
            onChange={(e) => setValues((v) => ({ ...v, port: e.target.value }))}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.inbounds.quickAdd.security")}</span>
          <Select
            className="w-full disabled:cursor-not-allowed disabled:opacity-60"
            dir="ltr"
            disabled={meta.securityLocked}
            value={values.security}
            onChange={(e) => setValues((v) => ({ ...v, security: e.target.value as "none" | "tls" }))}
          >
            <option value="none">{t("rapido.inbounds.quickAdd.securityNone")}</option>
            <option value="tls">{t("rapido.inbounds.quickAdd.securityTls")}</option>
          </Select>
          <span className="text-xs text-rapido-muted">
            {values.security === "tls"
              ? t("rapido.inbounds.quickAdd.tlsAutoHint")
              : values.protocol === "snell"
              ? t("rapido.inbounds.quickAdd.snellAutoHint")
              : isShadowtls
              ? t("rapido.inbounds.quickAdd.shadowtlsAutoHint")
              : null}
          </span>
        </label>

        {isShadowtls && (
          <div className="flex flex-col gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.inbounds.quickAdd.shadowtlsInnerMethod")}</span>
              <Select
                className="w-full"
                dir="ltr"
                value={values.shadowtlsInnerMethod}
                onChange={(e) => setValues((v) => ({ ...v, shadowtlsInnerMethod: e.target.value }))}
              >
                {SHADOWTLS_INNER_METHODS.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </Select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.inbounds.quickAdd.shadowtlsInnerPassword")}</span>
              <Input
                dir="ltr"
                value={values.shadowtlsInnerPassword}
                placeholder={t("rapido.inbounds.quickAdd.shadowtlsInnerPasswordPlaceholder")}
                onChange={(e) => setValues((v) => ({ ...v, shadowtlsInnerPassword: e.target.value }))}
              />
            </label>
          </div>
        )}

        {isHysteria && (
          <div className="grid grid-cols-2 gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.inbounds.quickAdd.upMbps")}</span>
              <Input
                dir="ltr"
                type="number"
                inputMode="numeric"
                value={values.upMbps}
                onChange={(e) => setValues((v) => ({ ...v, upMbps: e.target.value }))}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.inbounds.quickAdd.downMbps")}</span>
              <Input
                dir="ltr"
                type="number"
                inputMode="numeric"
                value={values.downMbps}
                onChange={(e) => setValues((v) => ({ ...v, downMbps: e.target.value }))}
              />
            </label>
            <span className="col-span-2 text-xs text-rapido-muted">{t("rapido.inbounds.quickAdd.bandwidthHint")}</span>
          </div>
        )}

        {error && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
            {error}
          </div>
        )}

        <div className="mt-2 flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} disabled={createInbound.isPending}>
            {t("cancel")}
          </Button>
          <Button variant="primary" disabled={createInbound.isPending || !canSubmit} onClick={submit}>
            {createInbound.isPending ? t("rapido.pleaseWait") : t("rapido.inbounds.quickAdd.addTitle")}
          </Button>
        </div>
      </div>
    </Modal>
  );
};

// ---------------------------------------------------------------------------

// Starting port for the bulk action's 8 inbounds - chosen clear of every
// port range already in real use on this fleet's own "main" VLESS inbound
// and its location hosts (20000-20134 as of this session), so a bulk click
// never collides with an existing host on a panel that already has them.
const BULK_BASE_PORT = 20300;

export const AddAllProtocolsButton: FC<{ onDone: () => void }> = ({ onDone }) => {
  const { t } = useTranslation();
  const createInbound = useCreateInboundMutation();
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<{ created: string[]; failed: { protocol: string; message: string }[] } | null>(
    null
  );

  const run = async () => {
    setRunning(true);
    setResult(null);
    const created: string[] = [];
    const failed: { protocol: string; message: string }[] = [];
    // Sequential, not Promise.all: each create is its own independent
    // request, and running them one at a time keeps a transient failure on
    // one protocol from racing the others - plus it means the result list
    // below fills in the same order the protocols are declared in, not
    // whatever order responses happened to land in.
    for (let i = 0; i < BULK_PROTOCOLS.length; i++) {
      const p = BULK_PROTOCOLS[i];
      const body: CreateInboundPayload = {
        tag: defaultTag(p.value),
        protocol: p.value,
        port: BULK_BASE_PORT + i,
        security: p.security,
      };
      try {
        await createInbound.mutateAsync(body);
        created.push(p.value);
      } catch (e) {
        failed.push({ protocol: p.value, message: errorText(e, t("rapido.inbounds.quickAdd.saveFailed")) });
      }
    }
    setResult({ created, failed });
    setRunning(false);
    if (created.length > 0) onDone();
  };

  return (
    <div className="flex flex-col items-end gap-2">
      <Button variant="chip" tone="accent" disabled={running} onClick={run}>
        {running ? t("rapido.pleaseWait") : `+ ${t("rapido.inbounds.quickAdd.addAll")}`}
      </Button>
      {result && (
        <div className="max-w-xs text-end text-xs">
          {result.created.length > 0 && (
            <div className="text-emerald-400">
              {t("rapido.inbounds.quickAdd.addAllCreated", { count: result.created.length, protocols: result.created.join(", ") })}
            </div>
          )}
          {result.failed.map((f) => (
            <div key={f.protocol} className="text-red-400" dir="ltr">
              {f.protocol}: {f.message}
            </div>
          ))}
        </div>
      )}
    </div>
  );
};
