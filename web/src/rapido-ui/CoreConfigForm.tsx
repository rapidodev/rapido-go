import { FC, Fragment, ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ConfigIssue, Item, asList, asStrings, isObject, str } from "utils/coreConfigRules";
import { FieldSpec, splitList } from "utils/coreConfigFields";
import {
  generatePassword,
  generateRealityKeyPair,
  generateShortId,
  generateSnellPsk,
  generateSs2022Key,
} from "utils/coreConfigGenerate";
import { formatInboundPorts, parseInboundPortInput } from "utils/inboundPorts";
import { SHADOWTLS_INNER_KEY_BYTES } from "types/Inbound";
import { Button } from "./Button";
import { Checkbox } from "./Checkbox";
import { Input } from "./Input";
import { JsonEditor } from "./JsonEditor";
import { Modal } from "./Modal";
import { Segmented } from "./Segmented";
import { Select } from "./Select";
import { ltrIsolate } from "./bidi";

// One add/edit dialog for every section of the core config, driven by the field
// lists in utils/coreConfigFields. It works on its own copy of the item and only
// hands it back on Save, so nothing half-typed ever reaches the document; the
// JSON tab is the same copy as text, for whatever a form doesn't cover.

type Translate = (key: string, opts?: Record<string, unknown>) => string;

export const issueMessage = (t: Translate, issue: ConfigIssue): string =>
  t(`rapido.xrayConfig.issue${issue.kind[0].toUpperCase()}${issue.kind.slice(1)}`, {
    value: ltrIsolate(issue.value ?? ""),
  });

const textareaClass =
  "w-full rounded-lg border bg-rapido-bg px-3 py-2 font-mono text-xs text-rapido-text placeholder:text-rapido-muted shadow-rp-inset transition-shadow focus:border-rapido-accent/60 focus:outline-none focus:ring-2 focus:ring-rapido-accent/40";

const errorBorder = (bad: boolean) => (bad ? "border-red-500" : "border-rapido-border");

const EASTERN_DIGITS = /[۰-۹٠-٩]/g;
const westernDigit = (d: string) => String("۰۱۲۳۴۵۶۷۸۹".indexOf(d) >= 0 ? "۰۱۲۳۴۵۶۷۸۹".indexOf(d) : "٠١٢٣٤٥٦٧٨٩".indexOf(d));

const ListField: FC<{
  spec: FieldSpec;
  value: unknown;
  bad: boolean;
  id: string;
  onChange: (next: string[] | undefined) => void;
}> = ({ spec, value, bad, id, onChange }) => {
  const [text, setText] = useState(() => asStrings(value).join("\n"));
  return (
    <textarea
      id={id}
      dir="ltr"
      rows={2}
      spellCheck={false}
      value={text}
      placeholder={spec.placeholder}
      className={`${textareaClass} ${errorBorder(bad)}`}
      onChange={(e) => {
        setText(e.target.value);
        const items = splitList(e.target.value);
        onChange(items.length > 0 ? items : undefined);
      }}
    />
  );
};

const PortsField: FC<{
  spec: FieldSpec;
  value: unknown;
  known: readonly number[];
  bad: boolean;
  id: string;
  onChange: (next: number[] | undefined) => void;
  onValidity: (ok: boolean) => void;
}> = ({ spec, value, known, bad, id, onChange, onValidity }) => {
  const { t } = useTranslation();
  const [text, setText] = useState(() => formatInboundPorts(asList(value).filter((p): p is number => typeof p === "number")));
  const parsed = parseInboundPortInput(text);
  const commit = (next: string) => {
    setText(next);
    const result = parseInboundPortInput(next);
    onValidity(result.ok);
    if (result.ok) onChange(result.ports.length > 0 ? result.ports : undefined);
  };
  const current = parsed.ok ? parsed.ports : [];
  const toggle = (port: number) =>
    commit(formatInboundPorts(current.includes(port) ? current.filter((p) => p !== port) : [...current, port]));
  const error = parsed.ok ? null : parsed.error;
  return (
    <>
      <Input
        id={id}
        dir="ltr"
        inputMode="numeric"
        placeholder={spec.placeholder ?? "443, 8443"}
        hasError={bad || !!error}
        aria-invalid={bad || !!error}
        value={text}
        onChange={(e) => commit(e.target.value)}
      />
      {error && (
        <span role="alert" className="text-xs text-red-400">
          {t(`rapido.xrayConfig.portError${error.kind[0].toUpperCase()}${error.kind.slice(1)}`, { value: ltrIsolate(error.value) })}
        </span>
      )}
      {known.length > 0 && (
        <div className="flex max-h-24 flex-wrap gap-1 overflow-y-auto" dir="ltr">
          {known.map((port) => (
            <button
              key={port}
              type="button"
              aria-pressed={current.includes(port)}
              onClick={() => toggle(port)}
              className={`rounded-md border px-1.5 py-0.5 text-[11px] tabular-nums transition-colors ${
                current.includes(port)
                  ? "border-rapido-accent bg-rapido-accent/15 text-rapido-accent"
                  : "border-rapido-border text-rapido-muted hover:border-rapido-accent/50"
              }`}
            >
              {port}
            </button>
          ))}
        </div>
      )}
    </>
  );
};

const NumberField: FC<{
  spec: FieldSpec;
  value: unknown;
  bad: boolean;
  id: string;
  onChange: (next: number | undefined) => void;
}> = ({ spec, value, bad, id, onChange }) => (
  <Input
    id={id}
    dir="ltr"
    inputMode="numeric"
    placeholder={spec.placeholder}
    hasError={bad}
    aria-invalid={bad}
    value={typeof value === "number" ? String(value) : ""}
    onChange={(e) => {
      const raw = e.target.value.replace(EASTERN_DIGITS, westernDigit).trim();
      if (raw === "") onChange(undefined);
      else if (/^\d+$/.test(raw)) onChange(Number(raw));
    }}
  />
);

export type FormChoices = {
  outboundTargets: readonly string[];
  balancerMembers: readonly string[];
  inboundTags: readonly string[];
  /** The listen ports of the inbounds a rule has selected, offered as one-click choices. */
  portsOf: (item: Item) => readonly number[];
};

const PickField: FC<{
  spec: FieldSpec;
  value: unknown;
  choices: FormChoices;
  bad: boolean;
  onChange: (next: unknown[] | undefined) => void;
}> = ({ spec, value, choices, bad, onChange }) => {
  const { t } = useTranslation();
  const selected = asList(value);
  const base: readonly unknown[] = spec.options ?? (spec.source && spec.source !== "ownMembers" ? choices[spec.source] : []);
  // Whatever the item already names stays listed, so it can be unticked even if
  // the document no longer has it.
  const all = [...base, ...selected.filter((v) => !base.includes(v))];
  const toggle = (option: unknown) => {
    const next = all.filter((v) => (v === option ? !selected.includes(v) : selected.includes(v)));
    onChange(next.length > 0 ? next : undefined);
  };
  if (all.length === 0) return <p className="text-xs text-rapido-muted">{t("rapido.coreForm.noChoices")}</p>;
  return (
    <div
      className={`flex max-h-40 flex-wrap gap-x-4 gap-y-1.5 overflow-y-auto rounded-lg border p-2 ${errorBorder(bad)}`}
      role="group"
    >
      {all.map((option) => (
        <Checkbox
          key={String(option)}
          checked={selected.includes(option)}
          onChange={() => toggle(option)}
          label={<span dir="ltr">{String(option)}</span>}
        />
      ))}
    </div>
  );
};

export type ItemFormProps = {
  title: string;
  initial: Item;
  fields: (draft: Item) => FieldSpec[];
  /** Everything wrong with the item as it stands, for the fields and the Save button. */
  issues: (draft: Item) => ConfigIssue[];
  /** Tidies the item on Save: drops what its type doesn't use. */
  normalize: (draft: Item) => Item;
  /** Fills in what an item of its new type needs, whenever a field changes. */
  withDefaults?: (draft: Item) => Item;
  choices: FormChoices;
  /** A line under the title, for what the whole section needs explained. */
  note?: ReactNode;
  onSave: (item: Item) => void;
  onClose: () => void;
};

export const ItemForm: FC<ItemFormProps> = ({ title, initial, fields, issues, normalize, withDefaults, choices, note, onSave, onClose }) => {
  const { t: rawT } = useTranslation();
  const t = rawT as Translate;
  const [draft, setDraft] = useState<Item>(initial);
  const [mode, setMode] = useState<"form" | "json">("form");
  const [jsonText, setJsonText] = useState("");
  const [jsonError, setJsonError] = useState("");
  const [attempted, setAttempted] = useState(false);
  const [badPorts, setBadPorts] = useState<Record<string, boolean>>({});
  // Bumped when a button changes a field that keeps its own text, so that field re-reads the value.
  const [epoch, setEpoch] = useState(0);
  const [realityPublic, setRealityPublic] = useState<string | null>(null);
  const [realityFailed, setRealityFailed] = useState(false);

  const specs = useMemo(() => fields(draft), [fields, draft]);
  const problems = useMemo(() => issues(draft), [issues, draft]);
  const shownKeys = new Set(specs.map((s) => s.key));
  const fieldProblems = (key: string) => (attempted ? problems.filter((p) => p.field === key) : []);
  const looseProblems = attempted ? problems.filter((p) => mode === "json" || !p.field || !shownKeys.has(p.field)) : [];

  const set = (key: string, value: unknown) =>
    setDraft((prev) => {
      const next = { ...prev };
      if (value === undefined) delete next[key];
      else next[key] = value;
      return withDefaults ? withDefaults(next) : next;
    });

  const parseJson = (): Item | null => {
    try {
      const parsed = JSON.parse(jsonText);
      if (!isObject(parsed)) throw new Error(t("rapido.xrayConfig.jsonMustBeObject"));
      setJsonError("");
      return parsed;
    } catch (e) {
      setJsonError(e instanceof Error ? e.message : String(e));
      return null;
    }
  };

  const switchMode = (next: "form" | "json") => {
    if (next === mode) return;
    if (next === "json") {
      setJsonText(JSON.stringify(draft, null, 2));
      setJsonError("");
    } else {
      const parsed = parseJson();
      if (!parsed) return;
      setDraft(parsed);
      setBadPorts({});
      setEpoch((n) => n + 1);
    }
    setMode(next);
  };

  const save = () => {
    const current = mode === "json" ? parseJson() : draft;
    if (!current) return;
    if (mode === "json") setDraft(current);
    if (issues(current).length > 0 || (mode === "form" && Object.values(badPorts).some(Boolean))) {
      setAttempted(true);
      return;
    }
    onSave(normalize(current));
  };

  const generate = async (spec: FieldSpec) => {
    switch (spec.generate) {
      case "password":
        set(spec.key, generatePassword());
        break;
      case "psk":
        set(spec.key, generateSnellPsk());
        break;
      case "ss2022Key":
        set(spec.key, generateSs2022Key(SHADOWTLS_INNER_KEY_BYTES[str(draft.shadowtls_inner_method)] ?? 32));
        break;
      case "shortId":
        set(spec.key, [...asStrings(draft[spec.key]), generateShortId()]);
        setEpoch((n) => n + 1);
        break;
      case "realityKey": {
        const pair = await generateRealityKeyPair();
        setRealityFailed(pair === null);
        if (pair) {
          set(spec.key, pair.privateKey);
          setRealityPublic(pair.publicKey);
        }
        break;
      }
    }
  };

  const renderControl = (spec: FieldSpec, id: string, bad: boolean) => {
    const value = draft[spec.key];
    switch (spec.kind) {
      case "bool":
        return (
          <Checkbox
            id={id}
            checked={value === true}
            disabled={spec.disabled}
            label={t(`rapido.coreForm.field.${spec.label ?? spec.key}`)}
            onChange={(e) => set(spec.key, e.target.checked ? true : undefined)}
          />
        );
      case "select": {
        const options = spec.options ?? (spec.source === "ownMembers" ? asStrings(draft.outbounds) : spec.source ? choices[spec.source] : []);
        const current = value === undefined || value === null ? "" : String(value);
        const known = options.map(String);
        return (
          <Select
            id={id}
            dir="ltr"
            className="w-full"
            disabled={spec.disabled}
            aria-invalid={bad}
            value={current}
            onChange={(e) => {
              const raw = e.target.value;
              if (raw === "") set(spec.key, undefined);
              else set(spec.key, typeof options[0] === "number" ? Number(raw) : raw);
            }}
          >
            {(spec.optional || !known.includes(current)) && <option value="">{spec.optional ? t("rapido.coreForm.none") : ""}</option>}
            {current !== "" && !known.includes(current) && <option value={current}>{current}</option>}
            {known.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </Select>
        );
      }
      case "number":
        return <NumberField spec={spec} value={value} bad={bad} id={id} onChange={(v) => set(spec.key, v)} />;
      case "list":
        return <ListField key={epoch} spec={spec} value={value} bad={bad} id={id} onChange={(v) => set(spec.key, v)} />;
      case "ports":
        return (
          <PortsField
            key={epoch}
            spec={spec}
            value={value}
            known={spec.key === "inbound_port" ? choices.portsOf(draft) : []}
            bad={bad}
            id={id}
            onChange={(v) => set(spec.key, v)}
            onValidity={(ok) => setBadPorts((prev) => ({ ...prev, [spec.key]: !ok }))}
          />
        );
      case "pick":
        return <PickField spec={spec} value={value} choices={choices} bad={bad} onChange={(v) => set(spec.key, v)} />;
      case "pem":
        return (
          <textarea
            id={id}
            dir="ltr"
            rows={4}
            spellCheck={false}
            value={str(value)}
            placeholder={"-----BEGIN ...-----"}
            className={`${textareaClass} ${errorBorder(bad)}`}
            onChange={(e) => set(spec.key, e.target.value === "" ? undefined : e.target.value)}
          />
        );
      default:
        return (
          <Input
            id={id}
            dir="ltr"
            autoComplete="off"
            spellCheck={false}
            disabled={spec.disabled}
            placeholder={spec.placeholder}
            hasError={bad}
            aria-invalid={bad}
            value={str(value)}
            onChange={(e) => set(spec.key, e.target.value === "" ? undefined : e.target.value)}
          />
        );
    }
  };

  return (
    <Modal
      className="max-w-3xl"
      onClose={() => {
        // A click outside must not throw away what was typed.
        if (draft === initial && mode === "form") onClose();
      }}
    >
      <h2 className="mb-1 text-lg font-semibold">{title}</h2>
      {note && <p className="mb-2 text-xs text-rapido-muted">{note}</p>}
      <div className="mb-3">
        <Segmented
          label={title}
          value={mode}
          options={[
            { value: "form", label: t("rapido.coreForm.modeForm") },
            { value: "json", label: t("rapido.coreForm.modeJson") },
          ]}
          onChange={switchMode}
        />
      </div>

      {mode === "form" ? (
        <div className="grid gap-3 sm:grid-cols-2">
          {specs.map((spec, position) => {
            const id = `core-field-${spec.key}`;
            const errors = fieldProblems(spec.key);
            const bad = errors.length > 0;
            const startsGroup = !!spec.group && specs[position - 1]?.group !== spec.group;
            return (
              <Fragment key={spec.key}>
                {startsGroup && (
                  <div className="mt-1 border-t border-rapido-border pt-3 sm:col-span-2">
                    <h3 className="text-xs font-semibold text-rapido-accent">{t(`rapido.coreForm.group.${spec.group}`)}</h3>
                    {spec.groupHint && <p className="mt-0.5 text-xs text-rapido-muted">{t(`rapido.coreForm.hint.${spec.groupHint}`)}</p>}
                  </div>
                )}
              <div className={`flex min-w-0 flex-col gap-1 ${spec.wide || spec.kind === "pem" ? "sm:col-span-2" : ""}`}>
                {spec.kind !== "bool" && (
                  <label htmlFor={id} className="text-xs font-medium text-rapido-text">
                    {t(`rapido.coreForm.field.${spec.label ?? spec.key}`)}
                    {spec.required && (
                      <span className="ms-0.5 text-red-400" aria-hidden="true">
                        *
                      </span>
                    )}
                  </label>
                )}
                <div className="flex items-start gap-1.5">
                  <div className="flex min-w-0 flex-1 flex-col gap-1">{renderControl(spec, id, bad)}</div>
                  {spec.generate && (
                    <Button variant="chip" tone="accent" onClick={() => void generate(spec)}>
                      {t("rapido.coreForm.generate")}
                    </Button>
                  )}
                </div>
                {spec.hint && <span className="text-xs text-rapido-muted">{t(`rapido.coreForm.hint.${spec.hint}`)}</span>}
                {spec.key === "reality_private_key" && realityPublic && (
                  <span className="text-xs text-emerald-400">
                    {t("rapido.coreForm.realityPublicKey")}{" "}
                    <code dir="ltr" className="select-all break-all">
                      {realityPublic}
                    </code>
                  </span>
                )}
                {spec.key === "reality_private_key" && realityFailed && (
                  <span role="alert" className="text-xs text-red-400">
                    {t("rapido.coreForm.realityUnsupported")}
                  </span>
                )}
                {errors.map((error) => (
                  <span key={`${error.kind}-${error.value ?? ""}`} role="alert" className="text-xs text-red-400">
                    {issueMessage(t, error)}
                  </span>
                ))}
              </div>
              </Fragment>
            );
          })}
        </div>
      ) : (
        <JsonEditor
          codeKeys
          rows={14}
          value={jsonText}
          ariaLabel={`${title} (JSON)`}
          onChange={(v) => {
            setJsonText(v);
            setJsonError("");
          }}
        />
      )}

      {(jsonError || looseProblems.length > 0) && (
        <div role="alert" className="mt-3 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">
          {jsonError && <p dir="ltr">{jsonError}</p>}
          {looseProblems.map((issue) => (
            <p key={`${issue.kind}-${issue.field ?? ""}-${issue.value ?? ""}`}>{issueMessage(t, issue)}</p>
          ))}
        </div>
      )}

      <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-rapido-border pt-3">
        <span className="text-xs text-rapido-muted">{t("rapido.coreForm.saveNote")}</span>
        <div className="flex gap-2">
          <Button variant="chip" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button variant="chip" tone="accent" onClick={save}>
            {t("rapido.coreForm.save")}
          </Button>
        </div>
      </div>
    </Modal>
  );
};
