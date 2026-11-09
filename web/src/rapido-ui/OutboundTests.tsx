import { FC, useEffect, useRef, useState } from "react";
import { BoltIcon } from "@heroicons/react/24/outline";
import { useTranslation } from "react-i18next";
import { errorText } from "service/errors";
import { fetch } from "service/http";
import { Node } from "types/Node";
import { OutboundTestResponse, OutboundTestResult } from "types/OutboundTest";
import { Button } from "./Button";
import { Select } from "./Select";

// The outbound test: the nodes dial through each outbound and time one request
// (POST /core/outbounds/test). Results are per node, because the same outbound
// is a tunnel on one server and a direct fallback on another.

/** Outbounds per request when testing them all; the panel takes at most 16. */
export const OUTBOUND_TEST_BATCH = 8;

export type OutboundTestState =
  | { running: true }
  | { running: false; results: OutboundTestResult[] }
  | { running: false; error: string };

type Translate = (key: string, opts?: Record<string, unknown>) => string;

export const useOutboundTests = () => {
  const { t } = useTranslation();
  const [byTag, setByTag] = useState<Record<string, OutboundTestState>>({});
  const [testingAll, setTestingAll] = useState(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const update = (tags: string[], state: (tag: string) => OutboundTestState) => {
    if (!mounted.current) return;
    setByTag((prev) => {
      const next = { ...prev };
      for (const tag of tags) next[tag] = state(tag);
      return next;
    });
  };

  /** nodeId 0 tests on every connected node. */
  const test = async (tags: string[], nodeId: number) => {
    update(tags, () => ({ running: true }));
    try {
      const resp = await fetch<OutboundTestResponse>("/core/outbounds/test", {
        method: "POST",
        body: nodeId ? { tags, node_id: nodeId } : { tags },
      });
      const results = Array.isArray(resp?.results) ? resp.results : [];
      update(tags, (tag) => ({ running: false, results: results.filter((r) => r.tag === tag) }));
    } catch (cause) {
      const error = errorText(cause, t("rapido.coreForm.testFailed"));
      update(tags, () => ({ running: false, error }));
    }
  };

  // One batch at a time, so a long list never has the nodes testing more than a
  // batch of it at once.
  const testAll = async (tags: string[], nodeId: number) => {
    if (tags.length === 0) return;
    setTestingAll(true);
    update(tags, () => ({ running: true }));
    for (let i = 0; i < tags.length && mounted.current; i += OUTBOUND_TEST_BATCH) {
      await test(tags.slice(i, i + OUTBOUND_TEST_BATCH), nodeId);
    }
    if (mounted.current) setTestingAll(false);
  };

  return { byTag, testingAll, test, testAll };
};

export const OutboundTestControls: FC<{
  nodes: readonly Node[];
  nodeId: number;
  onNode: (id: number) => void;
  onTestAll: () => void;
  testing: boolean;
  disabled?: boolean;
}> = ({ nodes, nodeId, onNode, onTestAll, testing, disabled }) => {
  const { t } = useTranslation();
  return (
    <>
      <Button variant="chip" tone="accent" onClick={onTestAll} disabled={testing || disabled} className="inline-flex items-center gap-1">
        <BoltIcon className="h-3.5 w-3.5" aria-hidden="true" />
        {testing ? t("rapido.coreForm.testRunning") : t("rapido.coreForm.testAll")}
      </Button>
      <Select
        aria-label={t("rapido.coreForm.testFrom")}
        value={nodeId}
        onChange={(e) => onNode(Number(e.target.value))}
        className="py-1 text-xs"
      >
        <option value={0}>{t("rapido.coreForm.testAllNodes")}</option>
        {nodes
          .filter((n) => n.status !== "disabled")
          .map((n) => (
            <option key={n.id} value={n.id}>
              {n.name}
            </option>
          ))}
      </Select>
    </>
  );
};

const ResultText: FC<{ result: OutboundTestResult; t: Translate }> = ({ result: r, t }) => {
  const foreign = !!r.interface && !r.interface_here;
  if (r.skipped) {
    return <span className="text-rapido-muted">{t(`rapido.coreForm.testSkip.${r.skipped}`, { defaultValue: r.skipped })}</span>;
  }
  if (r.error) {
    // A pinned exit whose tunnel is another server's fails for that reason,
    // whatever the dial said.
    const text = foreign ? t("rapido.coreForm.testNoTunnel", { iface: r.interface }) : r.error;
    return (
      <span dir="auto" className="inline-block max-w-[14rem] truncate align-bottom text-red-400" title={r.error}>
        {text}
      </span>
    );
  }
  const good = r.status_code >= 200 && r.status_code < 400;
  return (
    <>
      <span dir="ltr" className={`font-medium tabular-nums ${good ? "text-emerald-300" : "text-yellow-300"}`}>
        {r.delay_ms}ms ({r.status_code})
      </span>
      {r.fallback && (
        <span
          className="rounded bg-yellow-500/15 px-1 text-[11px] text-yellow-300"
          title={t(foreign ? "rapido.coreForm.testFallbackForeign" : "rapido.coreForm.testFallbackDown", { iface: r.interface ?? "" })}
        >
          {t("rapido.coreForm.testViaDirect")}
        </span>
      )}
    </>
  );
};

export const OutboundTestCell: FC<{ state?: OutboundTestState }> = ({ state }) => {
  const { t: rawT } = useTranslation();
  const t = rawT as Translate;
  if (!state) return <span className="text-xs text-rapido-muted">-</span>;
  if (state.running) {
    return (
      <span role="status" className="animate-pulse text-xs text-rapido-muted">
        {t("rapido.coreForm.testRunning")}
      </span>
    );
  }
  if ("error" in state) {
    return (
      <span dir="auto" className="inline-block max-w-[14rem] truncate align-bottom text-xs text-red-400" title={state.error}>
        {state.error}
      </span>
    );
  }
  if (state.results.length === 0) return <span className="text-xs text-rapido-muted">-</span>;
  // One node needs no name; several do, since each can answer differently.
  const named = state.results.length > 1;
  return (
    <ul className="flex flex-col gap-0.5">
      {state.results.map((r) => (
        <li key={r.node_id} className="flex items-center gap-1.5 whitespace-nowrap text-xs">
          {named && <span className="text-rapido-muted">{r.node}</span>}
          <ResultText result={r} t={t} />
        </li>
      ))}
    </ul>
  );
};
