import { KeyboardEvent, UIEvent, forwardRef, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from "react";
import classNames from "classnames";
import { TextEdit, caretAt, indentEdit, lineOffset, newlineEdit } from "./jsonEditing";
import { tokenizeJsonLine } from "./jsonHighlight";

// A JSON editor: a real textarea (so typing, selection, undo and accessibility stay native) laid
// over a coloured copy of its text. Only the lines in view, plus a few either side, are
// tokenized and rendered, so a very large document costs the same as a small one. The colours
// come from theme variables (see .rp-json-* in tailwind.css), so every preset and any custom
// palette is followed, and the light preset stays readable.

// The coloured copy is placed by arithmetic on this number, so the textarea has to really use it:
// it is set inline on both, never through a class. A class can lose (`[dir="rtl"] .text-xs` relaxes
// the line height on the Persian UI) or scale (rem follows the root font size), and a fraction of
// a pixel per line slides the visible text whole screens away from the caret on a long document.
const LINE = 20; // px
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
  /** Tab indents and Enter keeps the indent, for a document edited by hand rather than pasted.
   * Tab then stays in the box, so Escape followed by Tab is the keyboard's way out. */
  codeKeys?: boolean;
  /** Reports the caret's 1-based position whenever it moves. */
  onCaretChange?: (caret: { line: number; column: number }) => void;
};

export type JsonEditorHandle = {
  /** Scrolls a 1-based line into view and puts the caret at its start. */
  revealLine: (line: number) => void;
};

// Through the browser's own insert command, so Ctrl+Z still walks back over the edit, and React
// hears about it from the input event that command raises.
const applyEdit = (el: HTMLTextAreaElement, edit: TextEdit, onChange?: (value: string) => void) => {
  el.setSelectionRange(edit.start, edit.end);
  if (el.value.slice(edit.start, edit.end) !== edit.text) {
    const inserted =
      typeof document.execCommand === "function" &&
      (edit.text === "" ? document.execCommand("delete") : document.execCommand("insertText", false, edit.text));
    if (!inserted) {
      el.setRangeText(edit.text, edit.start, edit.end, "end");
      onChange?.(el.value);
    }
  }
  el.setSelectionRange(edit.selectionStart, edit.selectionEnd);
};

export const JsonEditor = forwardRef<JsonEditorHandle, JsonEditorProps>(
  (
    { value, onChange, readOnly, gutter = true, errorLine = null, rows = 12, ariaLabel, className, placeholder, codeKeys = false, onCaretChange },
    ref
  ) => {
    const box = useRef<HTMLTextAreaElement>(null);
    const leaving = useRef(false);
    const [scroll, setScroll] = useState({ top: 0, left: 0, height: rows * LINE });
    const measured = useRef(scroll);

    const lines = useMemo(() => value.split("\n"), [value]);

    // This runs after every commit, so an unchanged view must not even reach setScroll: an update
    // that changes nothing can still re-render, and then it would come straight back here.
    const measure = (el: HTMLTextAreaElement) => {
      const height = el.clientHeight || rows * LINE;
      const last = measured.current;
      if (last.top === el.scrollTop && last.left === el.scrollLeft && last.height === height) return;
      measured.current = { top: el.scrollTop, left: el.scrollLeft, height };
      setScroll(measured.current);
    };
    const onScroll = (e: UIEvent<HTMLTextAreaElement>) => measure(e.currentTarget);
    // The text can get shorter: keep the view in step.
    useLayoutEffect(() => {
      if (box.current) measure(box.current);
    });
    // Dragging the box taller (resize-y) renders nothing by itself, but exposes rows to draw.
    useEffect(() => {
      const el = box.current;
      if (!el) return;
      const observer = new ResizeObserver(() => measure(el));
      observer.observe(el);
      return () => observer.disconnect();
    }, []);

    const reportCaret = () => {
      if (box.current) onCaretChange?.(caretAt(box.current.value, box.current.selectionStart));
    };
    useLayoutEffect(reportCaret, [value]);

    useImperativeHandle(ref, () => ({
      revealLine: (line) => {
        const el = box.current;
        if (!el) return;
        const at = lineOffset(el.value, line);
        el.focus({ preventScroll: true });
        el.setSelectionRange(at, at);
        el.scrollTop = Math.max(0, (line - 1) * LINE - el.clientHeight / 3);
        el.scrollLeft = 0;
        el.scrollIntoView?.({ block: "nearest" });
        measure(el);
        reportCaret();
      },
    }));

    const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === "Shift" || e.ctrlKey || e.altKey || e.metaKey || e.nativeEvent.isComposing) return;
      const wasLeaving = leaving.current;
      leaving.current = e.key === "Escape";
      const el = e.currentTarget;
      if (e.key === "Tab" && !wasLeaving) {
        e.preventDefault();
        applyEdit(el, indentEdit(el.value, el.selectionStart, el.selectionEnd, e.shiftKey), onChange);
      } else if (e.key === "Enter") {
        e.preventDefault();
        applyEdit(el, newlineEdit(el.value, el.selectionStart, el.selectionEnd), onChange);
      }
    };

    const first = Math.max(0, Math.floor(scroll.top / LINE) - BUFFER);
    const last = Math.min(lines.length, Math.ceil((scroll.top + scroll.height) / LINE) + BUFFER);
    const visible = useMemo(() => {
      const out: { n: number; tokens: ReturnType<typeof tokenizeJsonLine> }[] = [];
      for (let n = first; n < last; n++) out.push({ n, tokens: tokenizeJsonLine(lines[n]) });
      return out;
    }, [lines, first, last]);

    const padLeft = gutter ? "2.75rem" : "0.75rem"; // pl-11 / pl-3
    const lineHeight = `${LINE}px`;

    return (
      <div className={classNames("rp-json relative rounded-lg border border-rapido-border bg-rapido-bg", className)} dir="ltr">
        {/* The coloured copy, clipped to the box and shifted by the textarea's own scroll. */}
        <div aria-hidden="true" className="pointer-events-none absolute inset-0 overflow-hidden rounded-lg">
          <div
            className="rp-json-layer absolute left-0 top-0 font-mono text-xs"
            style={{
              transform: `translate(${-scroll.left}px, ${-scroll.top}px)`,
              paddingTop: PAD_Y + first * LINE,
              paddingLeft: padLeft,
              lineHeight,
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
              className="absolute inset-y-0 left-0 w-9 overflow-hidden rounded-l-lg border-r border-rapido-border bg-rapido-surface text-right font-mono text-xs text-rapido-muted"
              style={{ lineHeight }}
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
          onSelect={reportCaret}
          onKeyDown={codeKeys && !readOnly ? onKeyDown : undefined}
          value={value}
          onChange={(e) => onChange?.(e.target.value)}
          style={{ lineHeight, tabSize: 2, paddingLeft: padLeft, paddingTop: PAD_Y, paddingBottom: PAD_Y }}
          className="rp-json-input relative block w-full resize-y overflow-auto whitespace-pre rounded-lg bg-transparent pr-3 font-mono text-xs focus:outline-none focus:ring-1 focus:ring-rapido-accent"
        />
      </div>
    );
  }
);
JsonEditor.displayName = "JsonEditor";
