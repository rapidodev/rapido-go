import { FC, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  DroppedCredentials,
  droppedCredentials,
  useSaveXrayConfigMutation,
  useXrayConfigQuery,
} from "hooks/useXrayConfigQuery";
import { useImportXrayConfigMutation, useInboundPortsQuery } from "hooks/useInboundsQuery";
import { XrayConfig, XrayConfigWritePayload } from "types/XrayConfig";
import { XrayImportResult } from "types/XrayImport";
import { errorText } from "service/errors";
import { locateJSONSyntaxError } from "utils/jsonSyntaxLocator";
import { Item, asList, parseDocObject, topLevelKeyLines, validateCoreConfigDoc } from "utils/coreConfigRules";
import { Card } from "rapido-ui/Card";
import { Badge } from "rapido-ui/Badge";
import { Button } from "rapido-ui/Button";
import { CoreConfigPanels, CoreTab } from "rapido-ui/CoreConfigPanels";
import { Modal } from "rapido-ui/Modal";
import { JsonEditor, JsonEditorHandle } from "rapido-ui/JsonEditor";
import { Segmented } from "rapido-ui/Segmented";
import { ltrIsolate } from "rapido-ui/bidi";
import { AddAllProtocolsButton, QuickAddInboundModal } from "rapido-ui/QuickAddInbound";

// parseXrayConfigJSON is deliberately lenient about the core-config fields
// (same reasoning as coreConfigHelpers.ts's own parseFullConfigJSON: only
// guarantee the list fields are real arrays so nothing downstream crashes
// on a hand-edited blob, everything else passes through to the server's
// own validateCoreConfig) but strict about `inbounds` being an array - the
// server always sends it, and silently defaulting a missing/malformed one
// to [] would mean "delete every inbound" is one typo away from "no
// inbounds section" on Apply. It isn't actually destructive server-side
// (syncInboundEntries only ever upserts, see xrayconfig.go's own doc
// comment), but failing loudly here is still the more honest response to
// a broken paste than silently proceeding with zero inbounds.
const parseXrayConfigJSON = (text: string): XrayConfigWritePayload => {
  const parsed = JSON.parse(text);
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    throw new Error("rapido.xrayConfig.jsonMustBeObject");
  }
  if (!Array.isArray(parsed.inbounds)) {
    throw new Error("rapido.xrayConfig.jsonInboundsMustBeArray");
  }
  return {
    log_level: typeof parsed.log_level === "string" ? parsed.log_level : "warn",
    sniff_enabled: !!parsed.sniff_enabled,
    outbounds: Array.isArray(parsed.outbounds) ? parsed.outbounds : [],
    routing_rules: Array.isArray(parsed.routing_rules) ? parsed.routing_rules : [],
    dns_servers: Array.isArray(parsed.dns_servers) ? parsed.dns_servers : [],
    inbounds: parsed.inbounds,
  };
};

// Turns a caught error from parseXrayConfigJSON into what the editor shows:
// a genuine JSON.parse SyntaxError gets run back through locateJSONSyntaxError
// (see that module's own doc comment on why - native error messages aren't a
// reliable source of a line number) and, when that succeeds, a line-numbered
// message plus the line itself (so the gutter can highlight it too); the two
// structural checks parseXrayConfigJSON throws itself (not-an-object,
// inbounds-not-an-array) describe the whole document rather than one line, so
// they intentionally surface with no line number.
const describeJSONError = (
  text: string,
  error: unknown,
  t: (key: string, opts?: Record<string, unknown>) => string
): { message: string; line: number | null } => {
  if (error instanceof SyntaxError) {
    const loc = locateJSONSyntaxError(text);
    if (loc) {
      return { message: t("rapido.xrayConfig.jsonLineError", { line: loc.line, message: ltrIsolate(loc.message) }), line: loc.line };
    }
    return { message: error.message, line: null };
  }
  return { message: error instanceof Error ? t(error.message) : String(error), line: null };
};

type CodeSection = "outbounds" | "routing_rules" | "dns_servers" | "inbounds";
type CodeScope = "full" | CodeSection;

const JUMP_KEYS = ["log_level", "sniff_enabled", "outbounds", "routing_rules", "dns_servers", "inbounds"];

const errorBox = "rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400";

// One section of the document as code of its own. It writes back as soon as what
// is typed parses; until then it keeps the text to itself and holds Apply back, so
// a half-typed list can never replace the section it came from.
const SectionCode: FC<{
  value: unknown;
  ariaLabel: string;
  onCommit: (list: unknown[]) => void;
  onBlocked: (blocked: boolean) => void;
}> = ({ value, ariaLabel, onCommit, onBlocked }) => {
  const { t } = useTranslation();
  const canonical = useMemo(() => JSON.stringify(asList(value), null, 2), [value]);
  const [draft, setDraft] = useState(canonical);
  const [problem, setProblem] = useState<{ message: string; line: number | null } | null>(null);
  const editor = useRef<JsonEditorHandle>(null);
  // A change from elsewhere (a form, an applied document) replaces the text; the
  // echo of what was just typed here must not reformat it under the caret.
  const lastCommitted = useRef(canonical);
  useEffect(() => {
    if (canonical === lastCommitted.current) return;
    lastCommitted.current = canonical;
    setDraft(canonical);
    setProblem(null);
    onBlocked(false);
  }, [canonical, onBlocked]);
  useEffect(() => () => onBlocked(false), [onBlocked]);

  const change = (next: string) => {
    setDraft(next);
    try {
      const parsed = JSON.parse(next);
      if (!Array.isArray(parsed)) throw new Error("rapido.xrayConfig.sectionMustBeList");
      setProblem(null);
      onBlocked(false);
      lastCommitted.current = JSON.stringify(parsed, null, 2);
      onCommit(parsed);
    } catch (e) {
      setProblem(describeJSONError(next, e, t));
      onBlocked(true);
    }
  };

  return (
    <>
      <JsonEditor ref={editor} codeKeys rows={22} value={draft} errorLine={problem?.line ?? null} ariaLabel={ariaLabel} onChange={change} />
      {problem && (
        <div role="alert" className={`mt-2 flex flex-wrap items-center justify-between gap-2 ${errorBox}`}>
          <span>{problem.message}</span>
          {problem.line !== null && (
            <Button variant="chip" tone="red" onClick={() => editor.current?.revealLine(problem.line as number)}>
              {t("rapido.xrayConfig.goToLine", { line: problem.line })}
            </Button>
          )}
        </div>
      )}
    </>
  );
};

const XrayConfigEditor: FC<{ config: XrayConfig }> = ({ config }) => {
  const { t } = useTranslation();
  const [text, setText] = useState(() => JSON.stringify(config, null, 2));
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  const [justApplied, setJustApplied] = useState(false);
  const [caret, setCaret] = useState({ line: 1, column: 1 });
  const editor = useRef<JsonEditorHandle>(null);
  const [previousText, setPreviousText] = useState<string | null>(null);
  const [tab, setTab] = useState<CoreTab>("general");
  const [scope, setScope] = useState<CodeScope>("full");
  const [sectionBlocked, setSectionBlocked] = useState(false);
  // Remounts a section's code editor when the document is replaced wholesale,
  // so nothing half-typed outlives Discard or Apply.
  const [generation, setGeneration] = useState(0);
  const [pendingLine, setPendingLine] = useState<number | null>(null);
  const [importing, setImporting] = useState(false);
  const [quickAdding, setQuickAdding] = useState(false);
  const { data: inboundPorts } = useInboundPortsQuery();
  const doc = useMemo(() => parseDocObject(text), [text]);
  const issues = useMemo(() => validateCoreConfigDoc(doc), [doc]);
  // Checked as the text changes rather than on Apply, so a typo is pointed at
  // while the admin is still on that line.
  const syntax = useMemo(() => {
    try {
      parseXrayConfigJSON(text);
      return null;
    } catch (e) {
      return describeJSONError(text, e, t);
    }
  }, [text, t]);
  const lineCount = useMemo(() => text.split("\n").length, [text]);
  const keyLines = useMemo(() => topLevelKeyLines(text), [text]);
  const serverInboundTags = useMemo(() => new Set(config.inbounds.map((i) => i.tag)), [config]);
  const blocked = issues.length > 0 || sectionBlocked || syntax !== null;
  // Set only while a parsed pending payload is genuinely about to delete one
  // or more inbounds (omitted from the JSON - see xrayconfig.go's own doc
  // comment on why that's real deletion here, not a no-op like the old
  // per-tag upsert-only sync was) - holds the tags themselves so the
  // confirmation can name them, and the already-parsed payload so
  // confirming doesn't re-parse text that may have changed underneath it.
  const [pendingRemoval, setPendingRemoval] = useState<{ tags: string[]; payload: XrayConfigWritePayload } | null>(
    null
  );
  // Set when the server refused an apply because it would delete credentials,
  // and holds what it said would go.
  const [pendingDrop, setPendingDrop] = useState<{ dropped: DroppedCredentials[]; payload: XrayConfigWritePayload } | null>(
    null
  );
  const save = useSaveXrayConfigMutation();

  // Re-seed from the server's own copy whenever it changes AND there's no
  // unapplied edit sitting in the document - a background refetch (e.g. the
  // quick-add buttons creating a new inbound) must never clobber something
  // the admin is mid-way through changing here.
  useEffect(() => {
    if (!dirty) setText(JSON.stringify(config, null, 2));
  }, [config, dirty]);

  // Once the code tab is showing, the line asked for can take focus.
  useEffect(() => {
    if (tab !== "advanced" || scope !== "full" || pendingLine === null) return;
    editor.current?.revealLine(pendingLine);
    setPendingLine(null);
  }, [tab, scope, pendingLine]);

  const doApply = (payload: XrayConfigWritePayload, confirmDropCredentials = false) => {
    const before = JSON.stringify(config, null, 2);
    save.mutate(
      { body: payload, confirmDropCredentials },
      {
        onSuccess: () => {
          setPreviousText(before);
          setDirty(false);
          setJustApplied(true);
          setPendingRemoval(null);
          setPendingDrop(null);
          setGeneration((n) => n + 1);
        },
        onError: (e) => {
          const dropped = droppedCredentials(e);
          if (dropped) {
            setPendingRemoval(null);
            setPendingDrop({ dropped, payload });
            return;
          }
          setError(errorText(e, t("rapido.xrayConfig.applyFailed")));
        },
      }
    );
  };

  // Every edit to the document - typed as code or made through a form - lands here.
  const changeText = (next: string) => {
    setText(next);
    setDirty(true);
    setJustApplied(false);
    setError("");
    setPendingRemoval(null);
    setPendingDrop(null);
  };
  const changeDoc = (next: Item) => changeText(JSON.stringify(next, null, 2));

  const apply = () => {
    if (blocked) return;
    setError("");
    setPendingRemoval(null);
    setPendingDrop(null);
    const payload = parseXrayConfigJSON(text);
    const currentTags = new Set(config.inbounds.map((i) => i.tag));
    const nextTags = new Set(payload.inbounds.map((i) => i.tag));
    const removed = [...currentTags].filter((tag) => !nextTags.has(tag));
    if (removed.length > 0) {
      setPendingRemoval({ tags: removed, payload });
      return;
    }
    doApply(payload);
  };

  const discard = () => {
    setDirty(false);
    setError("");
    setPendingRemoval(null);
    setPendingDrop(null);
    setGeneration((n) => n + 1);
    setText(JSON.stringify(config, null, 2));
  };

  const revert = () => {
    if (!previousText) return;
    setError("");
    try {
      const payload = parseXrayConfigJSON(previousText);
      save.mutate(
        { body: payload },
        {
          onSuccess: () => {
            setPreviousText(null);
            setDirty(false);
            setJustApplied(true);
            setGeneration((n) => n + 1);
          },
          onError: (e) => {
            const dropped = droppedCredentials(e);
            if (dropped) {
              setPendingDrop({ dropped, payload });
              return;
            }
            setError(errorText(e, t("rapido.xrayConfig.revertFailed")));
          },
        }
      );
    } catch (e) {
      setError(describeJSONError(previousText, e, t).message);
    }
  };

  const showCodeLine = (line: number | null) => {
    setScope("full");
    setTab("advanced");
    setPendingLine(line);
  };

  const sectionLabel: Record<CodeSection, string> = {
    outbounds: t("rapido.xrayConfig.tabOutbounds"),
    routing_rules: t("rapido.xrayConfig.tabRules"),
    dns_servers: t("rapido.xrayConfig.tabDns"),
    inbounds: t("rapido.xrayConfig.tabInbounds"),
  };

  const advanced = (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-rapido-muted">{t("rapido.xrayConfig.advancedDesc")}</p>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Segmented
          label={t("rapido.xrayConfig.tabAdvanced")}
          value={scope}
          options={[
            { value: "full", label: t("rapido.xrayConfig.scopeFull") },
            ...(Object.keys(sectionLabel) as CodeSection[]).map((value) => ({ value: value as CodeScope, label: sectionLabel[value] })),
          ]}
          onChange={setScope}
        />
        <Button
          variant="chip"
          disabled={dirty}
          title={dirty ? t("rapido.coreForm.applyFirst") : undefined}
          onClick={() => setImporting(true)}
        >
          {t("rapido.inbounds.xrayImportTitle")}
        </Button>
      </div>
      {scope === "full" ? (
        <>
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-xs text-rapido-muted">{t("rapido.xrayConfig.jumpTo")}</span>
            {JUMP_KEYS.map((key) => (
              <Button key={key} variant="chip" disabled={!keyLines[key]} onClick={() => editor.current?.revealLine(keyLines[key])}>
                <span dir="ltr">{key}</span>
              </Button>
            ))}
          </div>
          <JsonEditor
            ref={editor}
            value={text}
            rows={22}
            codeKeys
            errorLine={syntax?.line ?? null}
            ariaLabel={String(t("rapido.xrayConfig.jsonTitle"))}
            onCaretChange={setCaret}
            onChange={changeText}
          />
          <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-xs text-rapido-muted">
            <span>{t("rapido.xrayConfig.caretStatus", { line: caret.line, total: lineCount, column: caret.column })}</span>
            <span>{t("rapido.xrayConfig.editorKeysHint")}</span>
          </div>
        </>
      ) : doc ? (
        <SectionCode
          key={`${scope}-${generation}`}
          value={doc[scope]}
          ariaLabel={sectionLabel[scope]}
          onCommit={(list) => changeDoc({ ...doc, [scope]: list })}
          onBlocked={setSectionBlocked}
        />
      ) : (
        <p className="text-xs text-amber-300">{t("rapido.xrayConfig.structureInvalidJson")}</p>
      )}
      <p className="text-xs text-rapido-muted">{t("rapido.xrayConfig.guideNote")}</p>
    </div>
  );

  return (
    <Card className="p-4">
      <p className="text-xs text-rapido-muted">{t("rapido.xrayConfig.jsonDesc")}</p>
      <p className="mb-3 mt-1 text-xs text-rapido-muted">{t("rapido.xrayConfig.applyEffect")}</p>

      <div className="sticky top-2 z-20 mb-3 flex flex-wrap items-center justify-between gap-2 rounded-lg border border-rapido-border bg-rapido-surface/95 px-3 py-2 backdrop-blur">
        <span className="flex flex-wrap items-center gap-2 text-xs text-rapido-muted">
          {dirty && <Badge tone="yellow">{t("rapido.xrayConfig.unapplied")}</Badge>}
          {dirty
            ? t("rapido.xrayConfig.unappliedHint")
            : justApplied
            ? t("rapido.xrayConfig.appliedNote")
            : t("rapido.xrayConfig.inSync")}
        </span>
        <div className="flex gap-2">
          <Button variant="chip" disabled={!dirty} onClick={discard}>
            {t("rapido.xrayConfig.discard")}
          </Button>
          {pendingDrop ? (
            <Button
              variant="chip"
              tone="red"
              disabled={save.isPending || blocked}
              onClick={() => doApply(pendingDrop.payload, true)}
            >
              {save.isPending ? t("rapido.pleaseWait") : t("rapido.xrayConfig.applyConfirmDrop")}
            </Button>
          ) : pendingRemoval ? (
            <Button
              variant="chip"
              tone="amber"
              disabled={save.isPending || blocked}
              onClick={() => doApply(pendingRemoval.payload)}
            >
              {save.isPending ? t("rapido.pleaseWait") : t("rapido.xrayConfig.applyConfirmRemoval")}
            </Button>
          ) : (
            <Button variant="chip" tone="accent" disabled={!dirty || save.isPending || blocked} onClick={apply}>
              {save.isPending ? t("rapido.pleaseWait") : t("rapido.xrayConfig.apply")}
            </Button>
          )}
        </div>
      </div>

      {syntax && (
        <div role="alert" className={`mb-3 flex flex-wrap items-center justify-between gap-2 ${errorBox}`}>
          <span>{syntax.message}</span>
          {syntax.line !== null && (
            <Button variant="chip" tone="red" onClick={() => showCodeLine(syntax.line)}>
              {t("rapido.xrayConfig.goToLine", { line: syntax.line })}
            </Button>
          )}
        </div>
      )}
      {sectionBlocked && tab !== "advanced" && (
        <div role="alert" className={`mb-3 ${errorBox}`}>
          {t("rapido.xrayConfig.sectionCodeBlocked")}
        </div>
      )}
      {error && <div className={`mb-3 ${errorBox}`}>{error}</div>}
      {pendingRemoval && (
        <div className="mb-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-300">
          {t("rapido.xrayConfig.removalWarning", { tags: pendingRemoval.tags.join(", ") })}
        </div>
      )}
      {pendingDrop && (
        <div role="alert" className={`mb-3 ${errorBox}`}>
          <p>{t("rapido.xrayConfig.dropCredentialsWarning")}</p>
          <ul className="mt-1 list-inside list-disc">
            {pendingDrop.dropped.map((item) => (
              <li key={item.protocol}>
                {t("rapido.xrayConfig.dropCredentialsItem", { protocol: item.protocol, users: item.users })}
              </li>
            ))}
          </ul>
        </div>
      )}

      <CoreConfigPanels
        doc={doc}
        issues={issues}
        tab={tab}
        onTab={setTab}
        onChange={changeDoc}
        inboundPorts={inboundPorts}
        serverInboundTags={serverInboundTags}
        inboundActions={
          <>
            <Button
              variant="chip"
              disabled={dirty}
              title={dirty ? t("rapido.coreForm.applyFirst") : t("rapido.coreForm.quickAddHint")}
              onClick={() => setQuickAdding(true)}
            >
              + {t("rapido.coreForm.quickAdd")}
            </Button>
            <AddAllProtocolsButton disabled={dirty} onDone={() => {}} />
          </>
        }
        advanced={advanced}
        invalidNotice={<p className="text-xs text-amber-300">{t("rapido.xrayConfig.structureInvalidJson")}</p>}
      />

      {!dirty && previousText && (
        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 rounded-lg border border-rapido-border p-2">
          <div>
            <p className="text-xs text-rapido-text">{t("rapido.xrayConfig.previousVersionAvailable")}</p>
            <p className="text-xs text-rapido-muted">{t("rapido.xrayConfig.revertNote")}</p>
          </div>
          <Button variant="chip" disabled={save.isPending} onClick={revert}>
            {t("rapido.xrayConfig.revert")}
          </Button>
        </div>
      )}

      {importing && <XrayImportModal onClose={() => setImporting(false)} onApplied={() => {}} />}
      {quickAdding && (
        <QuickAddInboundModal onClose={() => setQuickAdding(false)} onCreated={() => setQuickAdding(false)} />
      )}
    </Card>
  );
};

// ---------------------------------------------------------------------------

// XrayImportModal is a two-step preview-then-apply flow around POST
// /api/inbounds/import-xray: "Preview" (confirm:false) always runs first
// and shows the parsed counts/warnings without writing anything, and only
// once that's happened does "Apply" (confirm:true) - re-running the exact
// same parse server-side, not reusing the preview's client-side result -
// become available. Re-editing the pasted text after a preview resets
// back to preview-only, so Apply can never fire against text the admin
// hasn't actually previewed. Moved here unchanged from the old
// InboundsAdmin.tsx when that page merged into this one - still the same
// real-world "paste your existing Xray config, migrate it" entry point.
const XrayImportModal: FC<{ onClose: () => void; onApplied: () => void }> = ({ onClose, onApplied }) => {
  const { t } = useTranslation();
  const [config, setConfig] = useState("");
  const [preview, setPreview] = useState<XrayImportResult | null>(null);
  const [applied, setApplied] = useState<XrayImportResult | null>(null);
  const [error, setError] = useState("");
  const importXray = useImportXrayConfigMutation();

  const runPreview = () => {
    setError("");
    setApplied(null);
    importXray.mutate(
      { config, confirm: false },
      {
        onSuccess: (result) => setPreview(result),
        onError: (e) => setError(errorText(e, t("rapido.inbounds.xrayImportFailed"))),
      }
    );
  };

  const runApply = () => {
    setError("");
    importXray.mutate(
      { config, confirm: true },
      {
        onSuccess: (result) => {
          setApplied(result);
          onApplied();
        },
        onError: (e) => setError(errorText(e, t("rapido.inbounds.xrayImportFailed"))),
      }
    );
  };

  const summary = applied ?? preview;

  return (
    <Modal onClose={onClose} className="max-w-2xl">
      <h2 className="mb-1 text-lg font-semibold">{t("rapido.inbounds.xrayImportTitle")}</h2>
      <p className="mb-4 text-xs text-rapido-muted">{t("rapido.inbounds.xrayImportSubtitle")}</p>
      <div className="flex flex-col gap-3">
        <JsonEditor
          gutter={false}
          rows={12}
          value={config}
          ariaLabel={String(t("rapido.inbounds.xrayImportTitle"))}
          onChange={(v) => {
            setConfig(v);
            setPreview(null);
            setApplied(null);
          }}
          placeholder={'{ "inbounds": [...], "outbounds": [...], "routing": {...} }'}
        />

        {summary && (
          <div className="rounded-lg border border-rapido-border p-3 text-xs">
            <div className="mb-2 flex flex-wrap gap-x-4 gap-y-1 font-medium">
              <span>{t("rapido.inbounds.xrayImportInbounds", { count: summary.inbounds_created })}</span>
              <span>{t("rapido.inbounds.xrayImportOutbounds", { count: summary.outbounds_saved })}</span>
              <span>{t("rapido.inbounds.xrayImportRules", { count: summary.routing_rules_saved })}</span>
              <span>{t("rapido.inbounds.xrayImportDns", { count: summary.dns_servers_saved })}</span>
            </div>
            {summary.warnings.length > 0 && (
              <ul className="list-inside list-disc space-y-0.5 text-amber-400">
                {summary.warnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            )}
            {applied && <p className="mt-2 text-emerald-400">{t("rapido.inbounds.xrayImportApplied")}</p>}
          </div>
        )}

        {error && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
            {error}
          </div>
        )}

        <div className="mt-2 flex justify-end gap-2">
          <Button variant="chip" onClick={onClose}>
            {applied ? t("rapido.close") : t("cancel")}
          </Button>
          {!applied && (
            <>
              <Button variant="chip" tone="accent" disabled={!config.trim() || importXray.isPending} onClick={runPreview}>
                {importXray.isPending && !preview ? t("rapido.pleaseWait") : t("rapido.inbounds.xrayImportPreview")}
              </Button>
              {preview && (
                <Button variant="chip" tone="accent" disabled={importXray.isPending} onClick={runApply}>
                  {importXray.isPending ? t("rapido.pleaseWait") : t("rapido.inbounds.xrayImportApply")}
                </Button>
              )}
            </>
          )}
        </div>
      </div>
    </Modal>
  );
};

// ---------------------------------------------------------------------------

// The sing-box config as one document (log level, sniffing, outbounds, routing
// rules, DNS servers, AND inbounds), edited through forms or as code with one
// Apply, replacing what used to be the separate Core Config and Inbounds pages.
// Hosts deliberately stays its own standalone page (rapido-ui/HostsAdmin.tsx via
// the "hosts" nav item) rather than being embedded here too - an earlier version
// of this page embedded HostsAdmin directly below the JSON editor, but the user
// asked for that removed: one place for hosts, not two.
export const XrayConfigAdmin: FC = () => {
  const { t } = useTranslation();
  const { data, isLoading, isError } = useXrayConfigQuery();

  if (isError) {
    return (
      <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
        {t("rapido.xrayConfig.loadFailed")}
      </div>
    );
  }
  if (isLoading || !data) {
    return <p className="text-sm text-rapido-muted">{t("rapido.pleaseWait")}</p>;
  }

  return <XrayConfigEditor config={data} />;
};

export default XrayConfigAdmin;
