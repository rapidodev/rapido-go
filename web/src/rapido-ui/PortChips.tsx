import { FC, KeyboardEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import { parseInboundPortInput } from "utils/inboundPorts";
import { Button } from "./Button";
import { Input } from "./Input";

/** Read-only port chips, left-to-right so the numbers never reorder. */
export const PortList: FC<{ ports: readonly number[] }> = ({ ports }) => (
  <div className="flex flex-wrap gap-1" dir="ltr">
    {ports.map((port) => (
      <span
        key={port}
        className="rounded-md bg-rapido-raised px-1.5 py-0.5 text-[11px] tabular-nums text-rapido-text ring-1 ring-inset ring-white/10"
      >
        {port}
      </span>
    ))}
  </div>
);

/**
 * Edits a list of ports as chips: type one or several (comma/space separated)
 * and press Enter or Add; click a chip's x to drop it. Always sorted and
 * de-duplicated.
 */
export const PortChips: FC<{
  ports: readonly number[];
  onChange: (next: number[]) => void;
  disabled?: boolean;
}> = ({ ports, onChange, disabled }) => {
  const { t } = useTranslation();
  const [draft, setDraft] = useState("");
  const [bad, setBad] = useState(false);

  const add = () => {
    if (!draft.trim()) return;
    const parsed = parseInboundPortInput(draft);
    if (!parsed.ok) {
      setBad(true);
      return;
    }
    setBad(false);
    setDraft("");
    onChange([...new Set([...ports, ...parsed.ports])].sort((a, b) => a - b));
  };

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      add();
    }
  };

  return (
    <div className="flex flex-col gap-2">
      <div className="flex min-h-[2rem] flex-wrap gap-1.5 rounded-lg border border-rapido-border bg-rapido-bg/60 p-2 shadow-rp-inset" dir="ltr">
        {ports.length === 0 && <span className="px-1 text-xs text-rapido-muted">{String(t("rapido.tunnels.portsNone"))}</span>}
        {ports.map((port) => (
          <span
            key={port}
            className="inline-flex items-center gap-1 rounded-md bg-rapido-accent/15 py-0.5 ps-2 pe-1 text-xs font-medium tabular-nums text-rapido-accent ring-1 ring-inset ring-rapido-accent/25"
          >
            {port}
            <button
              type="button"
              disabled={disabled}
              aria-label={String(t("rapido.tunnels.portRemove", { port }))}
              onClick={() => onChange(ports.filter((p) => p !== port))}
              className="flex h-4 w-4 items-center justify-center rounded text-rapido-accent/80 hover:bg-rapido-accent/25 hover:text-rapido-accent disabled:opacity-40"
            >
              <span aria-hidden="true">×</span>
            </button>
          </span>
        ))}
      </div>
      <div className="flex gap-2">
        <Input
          dir="ltr"
          disabled={disabled}
          hasError={bad}
          placeholder="443, 8443"
          aria-label={String(t("rapido.tunnels.portAddLabel"))}
          value={draft}
          onChange={(e) => {
            setDraft(e.target.value);
            setBad(false);
          }}
          onKeyDown={onKey}
        />
        <Button variant="secondary" disabled={disabled || !draft.trim()} onClick={add}>
          {String(t("rapido.tunnels.portAdd"))}
        </Button>
      </div>
      {bad && <span className="text-xs text-red-400">{String(t("rapido.tunnels.customPortsInvalid"))}</span>}
    </div>
  );
};
