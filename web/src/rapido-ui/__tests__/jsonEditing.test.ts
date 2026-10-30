import { describe, expect, it } from "vitest";
import { caretAt, indentEdit, lineOffset, newlineEdit, TextEdit } from "../jsonEditing";

const apply = (value: string, edit: TextEdit) => value.slice(0, edit.start) + edit.text + value.slice(edit.end);

describe("indentEdit", () => {
  it("inserts two spaces at a caret", () => {
    const edit = indentEdit('{\n"a"\n}', 2, 2, false);
    expect(apply('{\n"a"\n}', edit)).toBe('{\n  "a"\n}');
    expect(edit.selectionStart).toBe(4);
    expect(edit.selectionEnd).toBe(4);
  });

  it("indents every selected line instead of replacing the selection", () => {
    const value = "a\nb\nc";
    const edit = indentEdit(value, 0, 3, false);
    expect(apply(value, edit)).toBe("  a\n  b\nc");
    expect(apply(value, edit).slice(edit.selectionStart, edit.selectionEnd)).toBe("a\n  b");
  });

  it("leaves the next line alone when the selection ends right at its start", () => {
    const value = "a\nb\n";
    expect(apply(value, indentEdit(value, 0, 2, false))).toBe("  a\nb\n");
  });

  it("skips empty lines when indenting", () => {
    const value = "a\n\nb";
    expect(apply(value, indentEdit(value, 0, 4, false))).toBe("  a\n\n  b");
  });

  it("outdents the caret's line and keeps the caret on it", () => {
    const value = 'x\n    "a"';
    const edit = indentEdit(value, 4, 4, true);
    expect(apply(value, edit)).toBe('x\n  "a"');
    expect(edit.selectionStart).toBe(2);
    expect(edit.selectionEnd).toBe(2);
  });

  it("outdents every selected line, and a line with less than a level loses what it has", () => {
    const value = "    a\n b\nc";
    expect(apply(value, indentEdit(value, 0, value.length, true))).toBe("  a\nb\nc");
  });

  it("works on the first line of the document", () => {
    expect(apply("  a", indentEdit("  a", 0, 0, true))).toBe("a");
  });
});

describe("newlineEdit", () => {
  it("keeps the current indent", () => {
    const value = '  "a": 1,';
    const edit = newlineEdit(value, value.length, value.length);
    expect(edit.text).toBe("\n  ");
    expect(edit.selectionStart).toBe(value.length + 3);
  });

  it("goes one level deeper after an opening bracket", () => {
    const value = '  "a": {';
    expect(newlineEdit(value, value.length, value.length).text).toBe("\n    ");
    const array = '  "a": [';
    expect(newlineEdit(array, array.length, array.length).text).toBe("\n    ");
  });

  it("moves a closing bracket right after the caret to its own line", () => {
    const value = '  "a": []';
    const edit = newlineEdit(value, 8, 8);
    expect(apply(value, edit)).toBe('  "a": [\n    \n  ]');
    expect(edit.selectionStart).toBe(8 + "\n    ".length);
  });

  it("replaces a selection", () => {
    const value = "  abc";
    expect(apply(value, newlineEdit(value, 3, 4))).toBe("  a\n  c");
  });
});

describe("caretAt and lineOffset", () => {
  const value = "ab\ncde\n\nf";

  it("find a position's line and column", () => {
    expect(caretAt(value, 0)).toEqual({ line: 1, column: 1 });
    expect(caretAt(value, 2)).toEqual({ line: 1, column: 3 });
    expect(caretAt(value, 3)).toEqual({ line: 2, column: 1 });
    expect(caretAt(value, 7)).toEqual({ line: 3, column: 1 });
    expect(caretAt(value, value.length)).toEqual({ line: 4, column: 2 });
  });

  it("find where a line starts, and stop at the end of the text", () => {
    expect(lineOffset(value, 1)).toBe(0);
    expect(lineOffset(value, 2)).toBe(3);
    expect(lineOffset(value, 4)).toBe(8);
    expect(lineOffset(value, 99)).toBe(value.length);
  });
});
