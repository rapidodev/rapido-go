import classNames from "classnames";

/** A row of mutually exclusive choices (radio group), in the panel's chip style. */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap gap-1.5">
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={on}
            onClick={() => onChange(o.value)}
            className={classNames(
              "rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-rapido-accent/60",
              on
                ? "border-rapido-accent bg-rapido-accent/15 text-rapido-accent shadow-rp-glow"
                : "border-rapido-border text-rapido-muted hover:border-rapido-accent/50 hover:text-rapido-text"
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}
