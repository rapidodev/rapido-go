import { FC, UIEvent, useLayoutEffect, useMemo, useRef, useState } from "react";
import classNames from "classnames";
import { tokenizeJsonLine } from "./jsonHighlight";

// A JSON editor: a real textarea (so typing, selection, undo and accessibility stay native) laid
// over a coloured copy of its text. Only the lines in view, plus a few either side, are
// tokenized and rendered, so a very large document costs the same as a small one. The colours
// come from theme variables (see .rp-json-* in tailwind.css), so every preset and any custom
// palette is followed, and the light preset stays readable.

const LINE = 20; // px, = leading-5; the textarea and the coloured copy must agree on it
const PAD_Y = 8; // px, = py-2
const BUFFER = 12; // lines drawn beyond each edge of the view

export type JsonEditorProps = {
  value: string;
  onChange?: (value: string) => void;
  readOnly?: boolean;
  /** Line numbers down the left edge. */
  gutter?: boolean;
  /** 1-based line to mark in the gutter (for example where parsing failed). */
  errorLine?: number | null;
  rows?: number;
  ariaLabel?: string;
  className?: string;
  placeholder?: string;
};

export const JsonEditor: FC<JsonEditorProps> = ({
  value,
  onChange,
  readOnly,
  gutter = true,
  errorLine = null,
  rows = 12,
  ariaLabel,
  className,
  placeholder,
}) => {
  const box = useRef<HTMLTextAreaElement>(null);
  const [scroll, setScroll] = useState({ top: 0, left: 0, height: rows * LINE });

  const lines = useMemo(() => value.split("\n"), [value]);

  const measure = (el: HTMLTextAreaElement) => {
    const height = el.clientHeight || rows * LINE;
    setScroll((s) => (s.top === el.scrollTop && s.left === el.scrollLeft && s.height === height ? s : { top: el.scrollTop, left: el.scrollLeft, height }));
  };
  const onScroll = (e: UIEvent<HTMLTextAreaElement>) => measure(e.currentTarget);
  // The box can be dragged taller (resize-y) or the text can get shorter: keep the view in step.
  useLayoutEffect(() => {
    if (box.current) measure(box.current);
  });

  const first = Math.max(0, Math.floor(scroll.top / LINE) - BUFFER);
  const last = Math.min(lines.length, Math.ceil((scroll.top + scroll.height) / LINE) + BUFFER);
  const visible = useMemo(() => {
    const out: { n: number; tokens: ReturnType<typeof tokenizeJsonLine> }[] = [];
    for (let n = first; n < last; n++) out.push({ n, tokens: tokenizeJsonLine(lines[n]) });
    return out;
  }, [lines, first, last]);

  const padLeft = gutter ? "2.75rem" : "0.75rem"; // pl-11 / pl-3

  return (
    <div className={classNames("rp-json relative rounded-lg border border-rapido-border bg-rapido-bg", className)} dir="ltr">
      {/* The coloured copy, clipped to the box and shifted by the textarea's own scroll. */}
      <div aria-hidden="true" className="pointer-events-none absolute inset-0 overflow-hidden rounded-lg">
        <div
          className="rp-json-layer absolute left-0 top-0 font-mono text-xs leading-5"
          style={{
            transform: `translate(${-scroll.left}px, ${-scroll.top}px)`,
            paddingTop: PAD_Y + first * LINE,
            paddingLeft: padLeft,
            whiteSpace: "pre",
            tabSize: 2,
          }}
          data-testid="json-layer"
        >
          {visible.map(({ n, tokens }) => (
            <div key={n} style={{ height: LINE }}>
              {tokens.map((tk, i) => (
                <span key={i} className={tk.type === "plain" ? undefined : `rp-json-${tk.type}`}>
                  {tk.text}
                </span>
              ))}
            </div>
          ))}
        </div>
        {gutter && (
          <div
            className="absolute inset-y-0 left-0 w-9 overflow-hidden rounded-l-lg border-r border-rapido-border bg-rapido-surface text-right font-mono text-xs leading-5 text-rapido-muted"
            data-testid="json-gutter"
          >
            <div style={{ transform: `translateY(${-scroll.top}px)`, paddingTop: PAD_Y + first * LINE }}>
              {visible.map(({ n }) => (
                <div key={n} style={{ height: LINE }} className={n + 1 === errorLine ? "pr-2 font-semibold text-rapido-bad" : "pr-2"}>
                  {n + 1}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
      <textarea
        ref={box}
        dir="ltr"
        spellCheck={false}
        autoCapitalize="off"
        autoCorrect="off"
        wrap="off"
        rows={rows}
        readOnly={readOnly}
        aria-label={ariaLabel}
        placeholder={placeholder}
        onScroll={onScroll}
        value={value}
        onChange={(e) => onChange?.(e.target.value)}
        style={{ tabSize: 2, paddingLeft: padLeft, paddingTop: PAD_Y, paddingBottom: PAD_Y }}
        className="rp-json-input relative block w-full resize-y overflow-auto whitespace-pre rounded-lg bg-transparent pr-3 font-mono text-xs leading-5 focus:outline-none focus:ring-1 focus:ring-rapido-accent"
      />
    </div>
  );
};
