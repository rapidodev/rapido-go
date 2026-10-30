// The keyboard behaviour a hand-edited JSON document needs from a plain
// textarea, as pure functions over its text and selection: what to replace
// (start..end), with what (text), and where the selection lands afterwards.

export type TextEdit = {
  start: number;
  end: number;
  text: string;
  selectionStart: number;
  selectionEnd: number;
};

const INDENT = "  ";

const lineStartOf = (value: string, at: number) => (at === 0 ? 0 : value.lastIndexOf("\n", at - 1) + 1);

/** Tab: two spaces at a caret, one level on every selected line. Shift+Tab: one level off. */
export const indentEdit = (value: string, start: number, end: number, outdent: boolean): TextEdit => {
  if (!outdent && start === end) {
    const caret = start + INDENT.length;
    return { start, end, text: INDENT, selectionStart: caret, selectionEnd: caret };
  }
  const from = lineStartOf(value, start);
  // A selection ending right at the start of a line doesn't take that line with it.
  const lastLineAt = end > start && value[end - 1] === "\n" ? end - 1 : end;
  const nextBreak = value.indexOf("\n", lastLineAt);
  const to = nextBreak === -1 ? value.length : nextBreak;
  const lines = value.slice(from, to).split("\n");
  const changed = lines.map((line) => {
    if (outdent) return line.replace(/^( {1,2}|\t)/, "");
    return line === "" ? line : INDENT + line;
  });
  const text = changed.join("\n");
  const selectionStart = Math.max(from, start + changed[0].length - lines[0].length);
  const selectionEnd = start === end ? selectionStart : Math.max(selectionStart, end + text.length - (to - from));
  return { start: from, end: to, text, selectionStart, selectionEnd };
};

/** Enter: the new line keeps the current indent, one level deeper after an opening bracket, and
 * a closing bracket right after the caret moves to a line of its own. */
export const newlineEdit = (value: string, start: number, end: number): TextEdit => {
  const before = value.slice(lineStartOf(value, start), start);
  const indent = /^[ \t]*/.exec(before)?.[0] ?? "";
  const opens = /[{[][ \t]*$/.test(before);
  const inner = `\n${indent}${opens ? INDENT : ""}`;
  const closes = opens && /^[ \t]*[}\]]/.test(value.slice(end));
  const caret = start + inner.length;
  return { start, end, text: closes ? `${inner}\n${indent}` : inner, selectionStart: caret, selectionEnd: caret };
};

/** The 1-based line and column of an offset. */
export const caretAt = (value: string, at: number): { line: number; column: number } => {
  let line = 1;
  let lineStart = 0;
  for (let i = value.indexOf("\n"); i !== -1 && i < at; i = value.indexOf("\n", i + 1)) {
    line++;
    lineStart = i + 1;
  }
  return { line, column: at - lineStart + 1 };
};

/** The offset where a 1-based line starts, or the end of the text past its last line. */
export const lineOffset = (value: string, line: number): number => {
  let at = 0;
  for (let n = 1; n < line; n++) {
    const next = value.indexOf("\n", at);
    if (next === -1) return value.length;
    at = next + 1;
  }
  return at;
};
