import { FC, useEffect, useRef, useState } from "react";
import { BoltIcon } from "@heroicons/react/24/outline";
import { useTranslation } from "react-i18next";
import { errorText } from "service/errors";
import { fetch } from "service/http";
import { Node } from "types/Node";
import { OutboundTestResponse, OutboundTestResult } from "types/OutboundTest";
import { Item } from "utils/coreConfigRules";
import { Button } from "./Button";
import { Select } from "./Select";

// The outbound test: the nodes dial through each outbound and time one request
// (POST /core/outbounds/test). Results are per node, because the same outbound
// is a tunnel on one server and a direct fallback on another. An outbound the
// nodes don't run as it stands - new, or changed since the last Apply - goes as
// a draft instead: sent whole, and tested alone in a core of its own.

/** Outbounds per request when testing them all; the panel takes at most 16 tags and 8 drafts. */
export const OUTBOUND_TEST_BATCH = 8;

/** What to test: the tag of an outbound the nodes run, or a draft sent whole under its tag. */
export type OutboundTestTarget = string | { tag: string; draft: Item };

const targetTag = (target: OutboundTestTarget): string => (typeof target === "string" ? target : target.tag);

/** `draft` is set when the outbound went as a draft. */
export type OutboundTestState =
  | { running: true; draft?: true }
  | { running: false; draft?: true; results: OutboundTestResult[] }
  | { running: false; draft?: true; error: string };

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

  /** Every state of a target says whether it went as a draft. */
  const marked = (targets: OutboundTestTarget[], state: (tag: string) => OutboundTestState) => {
    const drafts = new Set(targets.filter((target) => typeof target !== "string").map(targetTag));
    update(targets.map(targetTag), (tag) => (drafts.has(tag) ? { ...state(tag), draft: true } : state(tag)));
  };

  /** nodeId 0 tests on every connected node. */
  const test = async (targets: OutboundTestTarget[], nodeId: number) => {
    const tags = targets.filter((target): target is string => typeof target === "string");
    const drafts = targets.flatMap((target) => (typeof target === "string" ? [] : [target.draft]));
    marked(targets, () => ({ running: true }));
    try {
      const resp = await fetch<OutboundTestResponse>("/core/outbounds/test", {
        method: "POST",
        body: {
          ...(tags.length > 0 || drafts.length === 0 ? { tags } : {}),
          ...(drafts.length > 0 ? { outbounds: drafts } : {}),
          ...(nodeId ? { node_id: nodeId } : {}),
        },
      });
      const results = Array.isArray(resp?.results) ? resp.results : [];
      marked(targets, (tag) => ({ running: false, results: results.filter((r) => r.tag === tag) }));
    } catch (cause) {
      const error = errorText(cause, t("rapido.coreForm.testFailed"));
      marked(targets, () => ({ running: false, error }));
    }
  };

  /** Shows why targets weren't sent at all, such as a draft the server would refuse. */
  const refuse = (targets: OutboundTestTarget[], error: string) => marked(targets, () => ({ running: false, error }));

  // One batch at a time, so a long list never has the nodes testing more than a
  // batch of it at once.
  const testAll = async (targets: OutboundTestTarget[], nodeId: number) => {
    if (targets.length === 0) return;
    setTestingAll(true);
    marked(targets, () => ({ running: true }));
    for (let i = 0; i < targets.length && mounted.current; i += OUTBOUND_TEST_BATCH) {
      await test(targets.slice(i, i + OUTBOUND_TEST_BATCH), nodeId);
    }
    if (mounted.current) setTestingAll(false);
  };

  return { byTag, testingAll, test, testAll, refuse };
};

/** Which node a test runs from: every connected one, or the one picked. */
export const OutboundTestNodeSelect: FC<{ nodes: readonly Node[]; nodeId: number; onNode: (id: number) => void }> = ({
  nodes,
  nodeId,
  onNode,
}) => {
  const { t } = useTranslation();
  return (
    <Select aria-label={t("rapido.coreForm.testFrom")} value={nodeId} onChange={(e) => onNode(Number(e.target.value))} className="py-1 text-xs">
      <option value={0}>{t("rapido.coreForm.testAllNodes")}</option>
      {nodes
        .filter((n) => n.status !== "disabled")
        .map((n) => (
          <option key={n.id} value={n.id}>
            {n.name}
          </option>
        ))}
    </Select>
  );
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
      <OutboundTestNodeSelect nodes={nodes} nodeId={nodeId} onNode={onNode} />
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

/** Says a result is a draft's: the outbound alone, not as the nodes run it. */
const DraftBadge: FC<{ t: Translate }> = ({ t }) => (
  <span className="rounded bg-sky-500/15 px-1 text-[11px] text-sky-300" title={t("rapido.coreForm.testDraftBadgeTitle")}>
    {t("rapido.coreForm.testDraftBadge")}
  </span>
);

export const OutboundTestCell: FC<{ state?: OutboundTestState }> = ({ state }) => {
  const { t: rawT } = useTranslation();
  const t = rawT as Translate;
  if (!state) return <span className="text-xs text-rapido-muted">-</span>;
  const badge = state.draft ? <DraftBadge t={t} /> : null;
  if (state.running) {
    return (
      <span className="inline-flex items-center gap-1.5">
        {badge}
        <span role="status" className="animate-pulse text-xs text-rapido-muted">
          {t("rapido.coreForm.testRunning")}
        </span>
      </span>
    );
  }
  if ("error" in state) {
    return (
      <span className="inline-flex items-center gap-1.5">
        {badge}
        <span dir="auto" className="inline-block max-w-[14rem] truncate align-bottom text-xs text-red-400" title={state.error}>
          {state.error}
        </span>
      </span>
    );
  }
  if (state.results.length === 0) return <span className="text-xs text-rapido-muted">-</span>;
  // One node needs no name; several do, since each can answer differently.
  const named = state.results.length > 1;
  return (
    <span className="inline-flex items-start gap-1.5">
      {badge}
      <ul className="flex flex-col gap-0.5">
        {state.results.map((r) => (
          <li key={r.node_id} className="flex items-center gap-1.5 whitespace-nowrap text-xs">
            {named && <span className="text-rapido-muted">{r.node}</span>}
            <ResultText result={r} t={t} />
          </li>
        ))}
      </ul>
    </span>
  );
};
