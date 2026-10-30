import { HTMLAttributes, Key, ReactNode, useEffect, useRef } from "react";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { Button } from "./Button";
import { Checkbox } from "./Checkbox";
import { Skeleton } from "./Skeleton";

export type DataTableColumn<Row> = {
  key: string;
  header: ReactNode;
  cell: (row: Row) => ReactNode;
  /** Cell and header alignment. "end" is the inline end (right in LTR, left in RTL). */
  align?: "start" | "end" | "center";
  className?: string;
};

export type DataTableProps<Row> = {
  columns: readonly DataTableColumn<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row) => Key;
  /** Adds a checkbox column. */
  selectable?: boolean;
  selected?: ReadonlySet<Key>;
  onSelectionChange?: (next: Set<Key>) => void;
  /** Rendered in the bar that appears while rows are selected, given those rows. */
  bulkActions?: (selectedRows: Row[]) => ReactNode;
  loading?: boolean;
  empty?: ReactNode;
  onRowClick?: (row: Row) => void;
  /** Extra attributes for a row's <tr>, such as drag handlers. */
  rowProps?: (row: Row) => HTMLAttributes<HTMLTableRowElement>;
  /** The scroll box height; the header stays pinned inside it. */
  maxHeight?: string;
  "aria-label"?: string;
  className?: string;
};

const alignClass = { start: "text-start", end: "text-end", center: "text-center" } as const;

/**
 * A table whose header stays pinned while the body scrolls, with optional
 * row checkboxes and a bulk-action bar. Selection is controlled so a page can
 * keep it across refetches. Everything is logical-direction (text-start,
 * text-end), so it mirrors for RTL.
 */
export function DataTable<Row>({
  columns,
  rows,
  rowKey,
  selectable = false,
  selected,
  onSelectionChange,
  bulkActions,
  loading = false,
  empty,
  onRowClick,
  rowProps,
  maxHeight = "70vh",
  "aria-label": ariaLabel,
  className,
}: DataTableProps<Row>) {
  const { t } = useTranslation();
  const sel = selected ?? new Set<Key>();
  const keys = rows.map(rowKey);
  const selectedCount = keys.filter((k) => sel.has(k)).length;
  const all = rows.length > 0 && selectedCount === rows.length;
  const some = selectedCount > 0 && !all;
  const headerBox = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (headerBox.current) headerBox.current.indeterminate = some;
  }, [some]);

  const emit = (next: Set<Key>) => onSelectionChange?.(next);
  const toggleAll = () => emit(all ? new Set() : new Set(keys));
  const toggleOne = (k: Key) => {
    const next = new Set(sel);
    if (next.has(k)) next.delete(k);
    else next.add(k);
    emit(next);
  };
  const selectedRows = rows.filter((r) => sel.has(rowKey(r)));
  const colCount = columns.length + (selectable ? 1 : 0);

  return (
    <div className={classNames("flex flex-col gap-2", className)}>
      {selectable && selectedCount > 0 && (
        <div
          role="status"
          className="flex flex-wrap items-center gap-3 rounded-lg border border-rapido-accent/30 bg-rapido-accent/10 px-3 py-2 text-sm"
        >
          <span className="font-medium text-rapido-accent">{t("rapido.table.selected", { count: selectedCount })}</span>
          <div className="flex flex-wrap items-center gap-1.5">{bulkActions?.(selectedRows)}</div>
          <Button variant="chip" className="ms-auto" onClick={() => emit(new Set())}>
            {t("rapido.table.clearSelection")}
          </Button>
        </div>
      )}

      <div
        className="overflow-auto rounded-xl2 border border-rapido-border bg-rapido-surface/80 shadow-rp-1"
        style={{ maxHeight }}
      >
        <table aria-label={ariaLabel} className="w-full border-separate border-spacing-0 text-sm">
          <thead>
            <tr>
              {selectable && (
                <th scope="col" className="sticky top-0 z-10 w-10 border-b border-rapido-border bg-rapido-raised px-3 py-2.5">
                  <Checkbox
                    ref={headerBox}
                    aria-label={t("rapido.table.selectAll")}
                    checked={all}
                    onChange={toggleAll}
                    disabled={rows.length === 0}
                  />
                </th>
              )}
              {columns.map((c) => (
                <th
                  key={c.key}
                  scope="col"
                  className={classNames(
                    "sticky top-0 z-10 whitespace-nowrap border-b border-rapido-border bg-rapido-raised px-3 py-2.5 text-xs font-semibold uppercase tracking-wide text-rapido-muted",
                    alignClass[c.align ?? "start"],
                    c.className
                  )}
                >
                  {c.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {loading ? (
              Array.from({ length: 4 }, (_, i) => (
                <tr key={i}>
                  <td colSpan={colCount} className="px-3 py-2.5">
                    <Skeleton className="h-5 w-full" />
                  </td>
                </tr>
              ))
            ) : rows.length === 0 ? (
              <tr>
                <td colSpan={colCount} className="px-3 py-8 text-center text-sm text-rapido-muted">
                  {empty}
                </td>
              </tr>
            ) : (
              rows.map((row) => {
                const k = rowKey(row);
                const on = sel.has(k);
                const extra = rowProps?.(row);
                return (
                  <tr
                    key={k}
                    aria-selected={selectable ? on : undefined}
                    onClick={onRowClick ? () => onRowClick(row) : undefined}
                    {...extra}
                    className={classNames(
                      "transition-colors",
                      on ? "bg-rapido-accent/10" : "hover:bg-rapido-raised/60",
                      onRowClick && "cursor-pointer",
                      extra?.className
                    )}
                  >
                    {selectable && (
                      <td className="w-10 border-b border-rapido-border/60 px-3 py-2.5">
                        <Checkbox
                          aria-label={t("rapido.table.selectRow")}
                          checked={on}
                          onChange={() => toggleOne(k)}
                          onClick={(e) => e.stopPropagation()}
                        />
                      </td>
                    )}
                    {columns.map((c) => (
                      <td
                        key={c.key}
                        className={classNames(
                          "border-b border-rapido-border/60 px-3 py-2.5 text-rapido-text",
                          alignClass[c.align ?? "start"],
                          c.className
                        )}
                      >
                        {c.cell(row)}
                      </td>
                    ))}
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
