import { ComponentType, DragEvent, FC, ReactNode, SVGProps, useMemo, useState } from "react";
import {
  Bars3Icon,
  ChevronDownIcon,
  ChevronUpIcon,
  DocumentDuplicateIcon,
  ExclamationTriangleIcon,
  PencilSquareIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { useTranslation } from "react-i18next";
import { useMonitoringQuery } from "hooks/useMonitoringQuery";
import { LOG_LEVELS, OUTBOUND_GROUP_TYPES, RoutingRule } from "types/CoreConfig";
import {
  ConfigIssue,
  Item,
  asList,
  asStrings,
  dnsIssues,
  inboundIssues,
  inboundReferences,
  isGroupOutbound,
  isObject,
  moveItem,
  otherMatcherSummary,
  otherTags,
  outboundIssues,
  outboundReferences,
  outboundTargets,
  renameInboundReferences,
  renameOutboundReferences,
  blockOutboundTags,
  ruleIssues,
  str,
  uniqueTag,
} from "utils/coreConfigRules";
import {
  FieldSpec,
  NEW_BALANCER,
  NEW_DNS,
  NEW_INBOUND,
  NEW_OUTBOUND,
  OUTBOUND_TYPES_PLAIN,
  dnsFields,
  inboundDefaults,
  inboundFields,
  normalizeDns,
  normalizeInbound,
  normalizeOutbound,
  normalizeRule,
  outboundDefaults,
  outboundFields,
  ruleFields,
} from "utils/coreConfigFields";
import { summarizePorts } from "utils/inboundPorts";
import { Badge } from "./Badge";
import { Button } from "./Button";
import { Checkbox } from "./Checkbox";
import { FormChoices, ItemForm, issueMessage } from "./CoreConfigForm";
import { DataTable, DataTableColumn } from "./DataTable";
import { Input } from "./Input";
import { Modal } from "./Modal";
import { Select } from "./Select";

// The Core Config page's tabs: one form-driven view per section of the document,
// each writing back through `onChange`, plus the code view the page hands in.
// There is no second copy of the config here - rows are read straight from the
// document, so what the forms show and what the code view shows cannot disagree.

export type CoreTab = "general" | "rules" | "outbounds" | "balancers" | "dns" | "inbounds" | "advanced";

type Section = "outbounds" | "routing_rules" | "dns_servers" | "inbounds";
type Row = { item: Item; index: number };
type Translate = (key: string, opts?: Record<string, unknown>) => string;

type Editing = {
  section: Section;
  /** Null for an item that isn't in the document yet. */
  index: number | null;
  /** Where a new item goes; the end of the list when absent. */
  insertAt?: number;
  initial: Item;
  balancer?: boolean;
};

const IconAction: FC<{
  label: string;
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  onClick: () => void;
  disabled?: boolean;
  danger?: boolean;
}> = ({ label, icon: Icon, onClick, disabled, danger }) => (
  <button
    type="button"
    aria-label={label}
    title={label}
    disabled={disabled}
    onClick={(e) => {
      e.stopPropagation();
      onClick();
    }}
    className={`rounded p-1 transition-colors disabled:cursor-not-allowed disabled:opacity-30 ${
      danger ? "text-rapido-muted hover:bg-red-500/15 hover:text-red-400" : "text-rapido-muted hover:bg-rapido-raised hover:text-rapido-text"
    }`}
  >
    <Icon className="h-4 w-4" aria-hidden="true" />
  </button>
);

const Mono: FC<{ children: ReactNode; title?: string }> = ({ children, title }) => (
  <span dir="ltr" title={title} className="inline-block max-w-[16rem] truncate align-bottom text-xs">
    {children}
  </span>
);

const Muted: FC<{ children: ReactNode }> = ({ children }) => <span className="text-xs text-rapido-muted">{children}</span>;

const ProblemMark: FC = () => {
  const { t } = useTranslation();
  return (
    <span role="img" aria-label={t("rapido.coreForm.rowHasProblem")}>
      <ExclamationTriangleIcon className="h-3.5 w-3.5 text-red-400" aria-hidden="true" />
    </span>
  );
};

const ItemTable: FC<{
  label: string;
  guide: string;
  rows: Row[];
  columns: DataTableColumn<Row>[];
  issuesOf: (index: number) => ConfigIssue[];
  addLabel: string;
  actions?: ReactNode;
  onAdd: () => void;
  onEdit: (row: Row) => void;
  onDuplicate: (row: Row) => void;
  onDelete: (row: Row) => void;
  /** Present when the order means something; rows can then be dragged and nudged. */
  onMove?: (from: number, to: number) => void;
}> = ({ label, guide, rows, columns, issuesOf, addLabel, actions, onAdd, onEdit, onDuplicate, onDelete, onMove }) => {
  const { t } = useTranslation();
  const [search, setSearch] = useState("");
  const [dragging, setDragging] = useState<number | null>(null);
  const [over, setOver] = useState<number | null>(null);

  const query = search.trim().toLowerCase();
  const shown = query === "" ? rows : rows.filter((row) => JSON.stringify(row.item).toLowerCase().includes(query));
  // Positions only mean something on the whole list.
  const canMove = !!onMove && query === "";
  // Numbered within this list: a section's rows can sit apart in the document,
  // as outbounds and balancers do.
  const positions = new Map(rows.map((row, position) => [row.index, position + 1]));

  const leading: DataTableColumn<Row> = {
    key: "position",
    header: "#",
    className: "w-16 whitespace-nowrap",
    cell: (row) => {
      const problems = issuesOf(row.index);
      return (
        <span className="flex items-center gap-1 text-xs text-rapido-muted">
          {canMove && <Bars3Icon className="h-3.5 w-3.5 cursor-grab" aria-hidden="true" />}
          <span dir="ltr">{positions.get(row.index)}</span>
          {problems.length > 0 && <ProblemMark />}
        </span>
      );
    },
  };
  const trailing: DataTableColumn<Row> = {
    key: "actions",
    header: t("rapido.coreForm.colActions"),
    align: "end",
    className: "w-px whitespace-nowrap",
    cell: (row) => (
      <span className="inline-flex items-center gap-0.5" dir="ltr">
        {onMove && (
          <>
            <IconAction
              label={t("rapido.coreForm.moveUp")}
              icon={ChevronUpIcon}
              disabled={!canMove || row.index === 0}
              onClick={() => onMove(row.index, row.index - 1)}
            />
            <IconAction
              label={t("rapido.coreForm.moveDown")}
              icon={ChevronDownIcon}
              disabled={!canMove || positions.get(row.index) === rows.length}
              onClick={() => onMove(row.index, row.index + 1)}
            />
          </>
        )}
        <IconAction label={t("rapido.edit")} icon={PencilSquareIcon} onClick={() => onEdit(row)} />
        <IconAction label={t("rapido.coreForm.duplicate")} icon={DocumentDuplicateIcon} onClick={() => onDuplicate(row)} />
        <IconAction label={t("rapido.coreForm.delete")} icon={TrashIcon} danger onClick={() => onDelete(row)} />
      </span>
    ),
  };

  const dragProps = (row: Row) =>
    canMove
      ? {
          draggable: true,
          onDragStart: (e: DragEvent<HTMLTableRowElement>) => {
            e.dataTransfer.effectAllowed = "move";
            setDragging(row.index);
          },
          onDragOver: (e: DragEvent<HTMLTableRowElement>) => {
            if (dragging === null) return;
            e.preventDefault();
            setOver(row.index);
          },
          onDrop: (e: DragEvent<HTMLTableRowElement>) => {
            e.preventDefault();
            if (dragging !== null && dragging !== row.index) onMove?.(dragging, row.index);
            setDragging(null);
            setOver(null);
          },
          onDragEnd: () => {
            setDragging(null);
            setOver(null);
          },
          className: over === row.index && dragging !== row.index ? "outline outline-1 -outline-offset-1 outline-rapido-accent" : undefined,
        }
      : {};

  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-rapido-muted">{guide}</p>
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="chip" tone="accent" onClick={onAdd}>
          + {addLabel}
        </Button>
        {actions}
        <div className="ms-auto w-full sm:w-56">
          <Input
            type="search"
            aria-label={t("rapido.coreForm.search")}
            placeholder={t("rapido.coreForm.search")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
      </div>
      <DataTable
        aria-label={label}
        rows={shown}
        rowKey={(row) => row.index}
        columns={[leading, ...columns, trailing]}
        onRowClick={onEdit}
        rowProps={dragProps}
        maxHeight="60vh"
        empty={query === "" ? t("rapido.coreForm.empty") : t("rapido.coreForm.noMatch")}
      />
    </div>
  );
};

const PortsSummary: FC<{ ports: readonly number[] }> = ({ ports }) => {
  const { t } = useTranslation();
  const { text, total, truncated } = summarizePorts(ports);
  return (
    <Muted>
      <span dir="ltr" title={ports.join(", ")}>
        {text}
      </span>
      {truncated && <span className="ms-1">{t("rapido.xrayConfig.portsCount", { count: total })}</span>}
    </Muted>
  );
};

const emptyChoices: FormChoices = { outboundTargets: [], balancerMembers: [], inboundTags: [], portsOf: () => [] };

type TunnelHealth = { up: number; total: number; fallback: number; detail: string[] };

export const CoreConfigPanels: FC<{
  doc: Item | null;
  issues: ConfigIssue[];
  tab: CoreTab;
  onTab: (tab: CoreTab) => void;
  onChange: (next: Item) => void;
  inboundPorts: Record<string, number[]> | undefined;
  /** Inbounds the server already has: their tag is locked, since hosts hang off it. */
  serverInboundTags: ReadonlySet<string>;
  inboundActions?: ReactNode;
  /** The code view. Stays mounted across tabs, so what is being typed there survives a look elsewhere. */
  advanced: ReactNode;
  /** Shown on the form tabs while the document doesn't parse. */
  invalidNotice: ReactNode;
}> = ({ doc, issues, tab, onTab, onChange, inboundPorts, serverInboundTags, inboundActions, advanced, invalidNotice }) => {
  const { t: rawT } = useTranslation();
  const t = rawT as Translate;
  const [editing, setEditing] = useState<Editing | null>(null);
  const [confirming, setConfirming] = useState<{ title: string; refs: string; onConfirm: () => void } | null>(null);

  const outbounds = useMemo(() => asList(doc?.outbounds), [doc]);
  const rows = (key: Section): Row[] =>
    asList(doc?.[key]).flatMap((item, index) => (isObject(item) ? [{ item, index }] : []));
  const plainRows = rows("outbounds").filter((row) => !isGroupOutbound(row.item));
  const groupRows = rows("outbounds").filter((row) => isGroupOutbound(row.item));
  const ruleRows = rows("routing_rules");
  const dnsRows = rows("dns_servers");
  const inboundRows = rows("inbounds");
  const targets = useMemo(() => outboundTargets(doc), [doc]);
  // The same reports the Monitoring page shows: nodes say how each tunnel is doing,
  // which is what an interface-bound outbound depends on.
  const { data: monitoring } = useMonitoringQuery(tab === "outbounds");
  const tunnels = useMemo(() => {
    const byName = new Map<string, TunnelHealth>();
    for (const host of monitoring?.hosts ?? []) {
      for (const tunnel of host.tunnels ?? []) {
        const entry = byName.get(tunnel.name) ?? { up: 0, total: 0, fallback: 0, detail: [] };
        entry.total += 1;
        if (tunnel.up) entry.up += 1;
        if (tunnel.fallback_active) entry.fallback += 1;
        entry.detail.push(`${host.name}: ${tunnel.up ? "up" : "down"}`);
        byName.set(tunnel.name, entry);
      }
    }
    return byName;
  }, [monitoring]);

  const issuesFor = (scope: ConfigIssue["scope"]) => (index: number) => issues.filter((i) => i.scope === scope && i.index === index);
  const tabHasIssue = (key: CoreTab) =>
    issues.some((i) => {
      if (i.scope === "general") return key === "general";
      if (i.scope === "rule") return key === "rules";
      if (i.scope === "dns") return key === "dns";
      if (i.scope === "inbound") return key === "inbounds";
      return key === (isGroupOutbound(outbounds[i.index]) ? "balancers" : "outbounds");
    });

  const commit = (edit: Editing, item: Item) => {
    if (!doc) return;
    const list = [...asList(doc[edit.section])];
    const before = edit.index !== null ? list[edit.index] : null;
    if (edit.index !== null) list[edit.index] = item;
    else if (edit.insertAt !== undefined) list.splice(edit.insertAt, 0, item);
    else list.push(item);
    let next: Item = { ...doc, [edit.section]: list };
    const from = isObject(before) ? str(before.tag) : "";
    const to = str(item.tag);
    if (from !== "" && to !== "" && from !== to) {
      if (edit.section === "outbounds") next = renameOutboundReferences(next, from, to);
      if (edit.section === "inbounds") next = renameInboundReferences(next, from, to);
    }
    onChange(next);
    setEditing(null);
  };

  const removeAt = (section: Section, index: number) => {
    if (!doc) return;
    onChange({ ...doc, [section]: asList(doc[section]).filter((_, i) => i !== index) });
  };

  const askToRemove = (section: Section, row: Row, refs: string[]) => {
    if (refs.length === 0) {
      removeAt(section, row.index);
      return;
    }
    setConfirming({
      title: str(row.item.tag),
      refs: refs.join("، "),
      onConfirm: () => removeAt(section, row.index),
    });
  };

  const removeOutbound = (row: Row) => {
    const { rules, groups } = outboundReferences(doc, str(row.item.tag));
    askToRemove("outbounds", row, [
      ...rules.map((i) => t("rapido.coreForm.refRule", { n: i + 1 })),
      ...groups.map((tag) => t("rapido.coreForm.refBalancer", { tag })),
    ]);
  };

  const removeInbound = (row: Row) =>
    askToRemove(
      "inbounds",
      row,
      inboundReferences(doc, str(row.item.tag)).map((i) => t("rapido.coreForm.refRule", { n: i + 1 }))
    );

  const taken = (key: "outbounds" | "dns_servers" | "inbounds") => otherTags(doc, key, null);

  const addOutbound = () =>
    setEditing({ section: "outbounds", index: null, initial: { ...NEW_OUTBOUND, tag: uniqueTag("outbound", taken("outbounds")) } });
  const addBalancer = () =>
    setEditing({ section: "outbounds", index: null, balancer: true, initial: { ...NEW_BALANCER, tag: uniqueTag("balancer", taken("outbounds")) } });
  const addRule = () =>
    setEditing({ section: "routing_rules", index: null, initial: { outbound_tag: str(plainRows[0]?.item.tag) || "direct" } });
  const addDns = () =>
    setEditing({ section: "dns_servers", index: null, initial: { ...NEW_DNS, tag: uniqueTag("dns", taken("dns_servers")) } });
  const addInbound = () =>
    setEditing({ section: "inbounds", index: null, initial: { ...NEW_INBOUND, tag: uniqueTag("inbound", taken("inbounds")) } });

  const editRow = (section: Section, balancer?: boolean) => (row: Row) =>
    setEditing({ section, index: row.index, initial: row.item, balancer });

  const duplicateRow = (section: Section, balancer?: boolean) => (row: Row) => {
    const copy = { ...row.item };
    if (section !== "routing_rules") {
      copy.tag = uniqueTag(`${str(row.item.tag)}-copy`, taken(section as "outbounds" | "dns_servers" | "inbounds"));
    }
    setEditing({ section, index: null, insertAt: row.index + 1, initial: copy, balancer });
  };

  const openIssue = (issue: ConfigIssue) => {
    if (issue.scope === "general") {
      onTab("general");
      return;
    }
    const section: Section =
      issue.scope === "rule" ? "routing_rules" : issue.scope === "dns" ? "dns_servers" : issue.scope === "inbound" ? "inbounds" : "outbounds";
    const item = asList(doc?.[section])[issue.index];
    if (!isObject(item)) {
      onTab("advanced");
      return;
    }
    const balancer = section === "outbounds" && isGroupOutbound(item);
    onTab(
      section === "routing_rules" ? "rules" : section === "dns_servers" ? "dns" : section === "inbounds" ? "inbounds" : balancer ? "balancers" : "outbounds"
    );
    setEditing({ section, index: issue.index, initial: item, balancer });
  };

  const usedBy = (tag: string) => {
    const { rules, groups } = outboundReferences(doc, tag);
    return rules.length + groups.length;
  };

  const issueWhere = (issue: ConfigIssue) => {
    switch (issue.scope) {
      case "general":
        return t("rapido.xrayConfig.issueWhereGeneral");
      case "rule":
        return t("rapido.xrayConfig.issueWhereRule", { n: issue.index + 1 });
      case "dns":
        return t("rapido.xrayConfig.issueWhereDns", { ref: issue.ref });
      case "inbound":
        return t("rapido.xrayConfig.issueWhereInbound", { ref: issue.ref });
      default:
        return t("rapido.xrayConfig.issueWhereOutbound", { ref: issue.ref });
    }
  };

  const tagColumn: DataTableColumn<Row> = {
    key: "tag",
    header: t("rapido.coreForm.field.tag"),
    cell: ({ item }) => (
      <span dir="ltr" className="text-sm font-semibold">
        {str(item.tag)}
      </span>
    ),
  };

  const outboundColumns: DataTableColumn<Row>[] = [
    tagColumn,
    {
      key: "type",
      header: t("rapido.coreForm.field.type"),
      cell: ({ item }) => (
        <Badge tone="sky" dir="ltr">
          {str(item.type)}
        </Badge>
      ),
    },
    {
      key: "address",
      header: t("rapido.coreForm.colAddress"),
      cell: ({ item }) => {
        const bind = str(item.bind_interface);
        const server = str(item.server);
        return (
          <span className="flex flex-wrap items-center gap-1.5">
            {server && <Mono>{item.server_port ? `${server}:${String(item.server_port)}` : server}</Mono>}
            {bind && (
              <Badge tone="brand" dir="ltr">
                {bind}
              </Badge>
            )}
            {item.direct_fallback === true && <Badge tone="yellow">{t("rapido.xrayConfig.directFallbackBadge")}</Badge>}
            {!server && !bind && <Muted>-</Muted>}
          </span>
        );
      },
    },
    {
      key: "tunnel",
      header: t("rapido.coreForm.colTunnel"),
      cell: ({ item }) => {
        const health = tunnels.get(str(item.bind_interface));
        if (!health) return <Muted>-</Muted>;
        return (
          <span title={health.detail.join("\n")}>
            <Badge tone={health.up === health.total ? "green" : health.up === 0 ? "red" : "yellow"} dir="ltr">
              {health.up}/{health.total}
            </Badge>
          </span>
        );
      },
    },
    { key: "used", header: t("rapido.coreForm.colUsedBy"), cell: ({ item }) => <Muted>{usedBy(str(item.tag))}</Muted> },
  ];

  const balancerColumns: DataTableColumn<Row>[] = [
    tagColumn,
    {
      key: "type",
      header: t("rapido.coreForm.field.type"),
      cell: ({ item }) => (
        <Badge tone="sky" dir="ltr">
          {str(item.type)}
        </Badge>
      ),
    },
    {
      key: "members",
      header: t("rapido.coreForm.field.members"),
      cell: ({ item }) => <Mono title={asStrings(item.outbounds).join(", ")}>{asStrings(item.outbounds).join(", ")}</Mono>,
    },
    { key: "used", header: t("rapido.coreForm.colUsedBy"), cell: ({ item }) => <Muted>{usedBy(str(item.tag))}</Muted> },
  ];

  const ruleColumns: DataTableColumn<Row>[] = [
    {
      key: "inbound",
      header: t("rapido.coreForm.colInbound"),
      cell: ({ item }) => {
        const inbound = asStrings(item.inbound);
        const ports = asList(item.inbound_port).filter((p): p is number => typeof p === "number");
        return (
          <span className="flex flex-wrap items-center gap-1.5">
            {inbound.length > 0 ? <Mono>{inbound.join(", ")}</Mono> : <Muted>{t("rapido.xrayConfig.ruleAnyInbound")}</Muted>}
            {ports.length > 0 && <PortsSummary ports={ports} />}
          </span>
        );
      },
    },
    {
      key: "conditions",
      header: t("rapido.coreForm.colConditions"),
      cell: ({ item }) => {
        const parts = otherMatcherSummary(item as unknown as RoutingRule);
        if (parts.length > 0) {
          return (
            <span className="flex flex-col gap-0.5">
              {parts.map((part) => (
                <Mono key={part} title={part}>
                  {part}
                </Mono>
              ))}
            </span>
          );
        }
        return asStrings(item.inbound).length > 0 ? <Muted>-</Muted> : <Muted>{t("rapido.coreForm.everyConnection")}</Muted>;
      },
    },
    {
      key: "outbound",
      header: t("rapido.coreForm.colOutbound"),
      cell: ({ item }) => (
        <Badge tone="brand" dir="ltr">
          {str(item.outbound_tag)}
        </Badge>
      ),
    },
  ];

  const dnsColumns: DataTableColumn<Row>[] = [
    tagColumn,
    {
      key: "type",
      header: t("rapido.coreForm.field.type"),
      cell: ({ item }) => (
        <Badge tone="sky" dir="ltr">
          {str(item.type)}
        </Badge>
      ),
    },
    {
      key: "address",
      header: t("rapido.coreForm.field.address"),
      cell: ({ item }) => <Mono>{[str(item.address), item.port ? String(item.port) : ""].filter(Boolean).join(":") || "-"}</Mono>,
    },
    { key: "path", header: t("rapido.coreForm.field.path"), cell: ({ item }) => <Mono>{str(item.path) || "-"}</Mono> },
  ];

  const inboundColumns: DataTableColumn<Row>[] = [
    tagColumn,
    {
      key: "protocol",
      header: t("rapido.coreForm.field.protocol"),
      cell: ({ item }) => (
        <Badge tone="sky" dir="ltr">
          {str(item.protocol)}
        </Badge>
      ),
    },
    { key: "network", header: t("rapido.coreForm.field.inboundNetwork"), cell: ({ item }) => <Mono>{str(item.network) || "-"}</Mono> },
    { key: "security", header: t("rapido.coreForm.field.security"), cell: ({ item }) => <Mono>{str(item.security) || "-"}</Mono> },
    {
      key: "ports",
      header: t("rapido.coreForm.colPorts"),
      cell: ({ item }) => {
        const ports = inboundPorts?.[str(item.tag)] ?? [];
        return ports.length > 0 ? <PortsSummary ports={ports} /> : <Muted>-</Muted>;
      },
    },
  ];

  const choices: FormChoices = doc
    ? {
        outboundTargets: targets,
        balancerMembers: targets.filter((tag) => tag !== str(editing?.initial.tag)),
        inboundTags: [...otherTags(doc, "inbounds", null)],
        portsOf: (item) =>
          [...new Set(asStrings(item.inbound).flatMap((tag) => inboundPorts?.[tag] ?? []))].sort((a, b) => a - b),
      }
    : emptyChoices;

  const form = (() => {
    if (!editing) return null;
    const { section, index, balancer } = editing;
    const add = index === null;
    const shared = {
      initial: editing.initial,
      choices,
      onClose: () => setEditing(null),
      onSave: (item: Item) => commit(editing, item),
    };
    switch (section) {
      case "outbounds": {
        const types: readonly string[] = balancer ? OUTBOUND_GROUP_TYPES : OUTBOUND_TYPES_PLAIN;
        const normalize = (d: Item) => normalizeOutbound(d, types);
        return (
          <ItemForm
            {...shared}
            title={t(`rapido.coreForm.${add ? "add" : "edit"}${balancer ? "Balancer" : "Outbound"}`)}
            fields={(d: Item): FieldSpec[] => outboundFields(d, types)}
            normalize={normalize}
            withDefaults={outboundDefaults}
            issues={(d) => outboundIssues(normalize(d), index ?? -1, otherTags(doc, "outbounds", index), targets)}
          />
        );
      }
      case "routing_rules":
        return (
          <ItemForm
            {...shared}
            title={t(`rapido.coreForm.${add ? "add" : "edit"}Rule`)}
            note={t("rapido.coreForm.ruleNote")}
            fields={ruleFields}
            normalize={normalizeRule}
            issues={(d) => ruleIssues(normalizeRule(d), index ?? -1, targets, otherTags(doc, "inbounds", null), blockOutboundTags(doc))}
          />
        );
      case "dns_servers":
        return (
          <ItemForm
            {...shared}
            title={t(`rapido.coreForm.${add ? "add" : "edit"}Dns`)}
            fields={dnsFields}
            normalize={normalizeDns}
            issues={(d) => dnsIssues(normalizeDns(d), index ?? -1, otherTags(doc, "dns_servers", index))}
          />
        );
      case "inbounds": {
        const locked = !add && serverInboundTags.has(str(editing.initial.tag));
        const normalize = (d: Item) => normalizeInbound(d, locked);
        return (
          <ItemForm
            {...shared}
            title={t(`rapido.coreForm.${add ? "add" : "edit"}Inbound`)}
            note={t("rapido.coreForm.inboundNote")}
            fields={(d: Item) => inboundFields(d, locked)}
            normalize={normalize}
            withDefaults={inboundDefaults}
            issues={(d) => inboundIssues(normalize(d), index ?? -1, otherTags(doc, "inbounds", index))}
          />
        );
      }
    }
  })();

  const TABS: { key: CoreTab; label: string; count?: number }[] = [
    { key: "general", label: t("rapido.xrayConfig.tabGeneral") },
    { key: "rules", label: t("rapido.xrayConfig.tabRules"), count: ruleRows.length },
    { key: "outbounds", label: t("rapido.xrayConfig.tabOutbounds"), count: plainRows.length },
    { key: "balancers", label: t("rapido.xrayConfig.tabBalancers"), count: groupRows.length },
    { key: "dns", label: t("rapido.xrayConfig.tabDns"), count: dnsRows.length },
    { key: "inbounds", label: t("rapido.xrayConfig.tabInbounds"), count: inboundRows.length },
    { key: "advanced", label: t("rapido.xrayConfig.tabAdvanced") },
  ];

  return (
    <div>
      <div role="tablist" aria-label={t("rapido.xrayConfig.title")} className="mb-3 flex flex-wrap gap-x-1 border-b border-rapido-border">
        {TABS.map(({ key, label, count }) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={tab === key}
            onClick={() => onTab(key)}
            className={`-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-rapido-accent/60 ${
              tab === key
                ? "border-rapido-accent font-semibold text-rapido-accent"
                : "border-transparent text-rapido-muted hover:text-rapido-text"
            }`}
          >
            {label}
            {count !== undefined && doc && (
              <span className="rounded-full bg-rapido-raised px-1.5 text-[11px] tabular-nums text-rapido-muted">{count}</span>
            )}
            {tabHasIssue(key) && <ProblemMark />}
          </button>
        ))}
      </div>

      {issues.length > 0 && (
        <div role="alert" className="mb-3 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          <p className="mb-1 font-medium">{t("rapido.xrayConfig.issuesTitle")}</p>
          <ul className="max-h-40 list-inside list-disc space-y-0.5 overflow-y-auto text-xs">
            {issues.map((issue, i) => (
              <li key={`${issue.scope}-${issue.index}-${issue.kind}-${i}`}>
                <button type="button" className="text-start hover:underline" onClick={() => openIssue(issue)}>
                  {issueWhere(issue)}: {issueMessage(t, issue)}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}

      {tab !== "advanced" && !doc && invalidNotice}

      {tab === "general" && doc && (
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-1">
            <label htmlFor="core-log-level" className="text-xs font-medium text-rapido-text">
              {t("rapido.coreForm.field.log_level")}
            </label>
            <Select
              id="core-log-level"
              dir="ltr"
              className="w-full"
              value={str(doc.log_level)}
              onChange={(e) => onChange({ ...doc, log_level: e.target.value })}
            >
              {str(doc.log_level) === "" && <option value="" />}
              {str(doc.log_level) !== "" && !(LOG_LEVELS as readonly string[]).includes(str(doc.log_level)) && (
                <option value={str(doc.log_level)}>{str(doc.log_level)}</option>
              )}
              {LOG_LEVELS.map((level) => (
                <option key={level} value={level}>
                  {level}
                </option>
              ))}
            </Select>
            <span className="text-xs text-rapido-muted">{t("rapido.xrayConfig.guideLogLevel")}</span>
          </div>
          <div className="flex flex-col gap-1 sm:pt-5">
            <Checkbox
              checked={doc.sniff_enabled === true}
              label={t("rapido.coreForm.field.sniff_enabled")}
              onChange={(e) => onChange({ ...doc, sniff_enabled: e.target.checked })}
            />
            <span className="text-xs text-rapido-muted">{t("rapido.xrayConfig.guideSniffEnabled")}</span>
          </div>
        </div>
      )}

      {tab === "rules" && doc && (
        <ItemTable
          label={t("rapido.xrayConfig.tabRules")}
          guide={t("rapido.xrayConfig.guideRoutingRules")}
          rows={ruleRows}
          columns={ruleColumns}
          issuesOf={issuesFor("rule")}
          addLabel={t("rapido.coreForm.addRule")}
          onAdd={addRule}
          onEdit={editRow("routing_rules")}
          onDuplicate={duplicateRow("routing_rules")}
          onDelete={(row) => removeAt("routing_rules", row.index)}
          onMove={(from, to) => onChange({ ...doc, routing_rules: moveItem(asList(doc.routing_rules), from, to) })}
        />
      )}

      {tab === "outbounds" && doc && (
        <ItemTable
          label={t("rapido.xrayConfig.tabOutbounds")}
          guide={t("rapido.xrayConfig.guideOutbounds")}
          rows={plainRows}
          columns={outboundColumns}
          issuesOf={issuesFor("outbound")}
          addLabel={t("rapido.coreForm.addOutbound")}
          onAdd={addOutbound}
          onEdit={editRow("outbounds")}
          onDuplicate={duplicateRow("outbounds")}
          onDelete={removeOutbound}
        />
      )}

      {tab === "balancers" && doc && (
        <ItemTable
          label={t("rapido.xrayConfig.tabBalancers")}
          guide={t("rapido.xrayConfig.guideBalancers")}
          rows={groupRows}
          columns={balancerColumns}
          issuesOf={issuesFor("outbound")}
          addLabel={t("rapido.coreForm.addBalancer")}
          onAdd={addBalancer}
          onEdit={editRow("outbounds", true)}
          onDuplicate={duplicateRow("outbounds", true)}
          onDelete={removeOutbound}
        />
      )}

      {tab === "dns" && doc && (
        <ItemTable
          label={t("rapido.xrayConfig.tabDns")}
          guide={t("rapido.xrayConfig.guideDnsServers")}
          rows={dnsRows}
          columns={dnsColumns}
          issuesOf={issuesFor("dns")}
          addLabel={t("rapido.coreForm.addDns")}
          onAdd={addDns}
          onEdit={editRow("dns_servers")}
          onDuplicate={duplicateRow("dns_servers")}
          onDelete={(row) => removeAt("dns_servers", row.index)}
        />
      )}

      {tab === "inbounds" && doc && (
        <ItemTable
          label={t("rapido.xrayConfig.tabInbounds")}
          guide={t("rapido.xrayConfig.guideInbounds")}
          rows={inboundRows}
          columns={inboundColumns}
          issuesOf={issuesFor("inbound")}
          addLabel={t("rapido.coreForm.addInbound")}
          actions={inboundActions}
          onAdd={addInbound}
          onEdit={editRow("inbounds")}
          onDuplicate={duplicateRow("inbounds")}
          onDelete={removeInbound}
        />
      )}

      <div hidden={tab !== "advanced"}>{advanced}</div>

      {form}

      {confirming && (
        <Modal onClose={() => setConfirming(null)} title={t("rapido.coreForm.deleteUsedTitle", { tag: confirming.title })}>
          <p className="mb-3 text-sm text-rapido-text">{t("rapido.coreForm.deleteUsedBody", { refs: confirming.refs })}</p>
          <div className="flex justify-end gap-2">
            <Button variant="chip" onClick={() => setConfirming(null)}>
              {t("cancel")}
            </Button>
            <Button
              variant="chip"
              tone="red"
              onClick={() => {
                confirming.onConfirm();
                setConfirming(null);
              }}
            >
              {t("rapido.coreForm.deleteAnyway")}
            </Button>
          </div>
        </Modal>
      )}
    </div>
  );
};
