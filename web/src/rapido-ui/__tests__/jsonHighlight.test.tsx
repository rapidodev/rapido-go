import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { Profiler, createRef, useState } from "react";
import { describe, expect, it } from "vitest";
import { JsonEditor, JsonEditorHandle } from "../JsonEditor";
import { MAX_COLOURED_LINE, tokenizeJson, tokenizeJsonLine } from "../jsonHighlight";

const kinds = (line: string) => tokenizeJsonLine(line).filter((t) => t.type !== "plain").map((t) => `${t.type}:${t.text}`);

describe("JSON tokenizer", () => {
  it("tells keys from string values", () => {
    expect(kinds('  "port": 443,')).toEqual(["key:\"port\"", "punct::", "number:443", "punct:,"]);
    expect(kinds('"tag" : "direct"')).toEqual(['key:"tag"', "punct::", 'string:"direct"']);
    expect(kinds('["a", "b"]')).toEqual(["punct:[", 'string:"a"', "punct:,", 'string:"b"', "punct:]"]);
  });

  it("colours numbers, booleans and null", () => {
    expect(kinds('"a": -1.5e+3, "b": true, "c": false, "d": null')).toEqual([
      'key:"a"', "punct::", "number:-1.5e+3", "punct:,",
      'key:"b"', "punct::", "literal:true", "punct:,",
      'key:"c"', "punct::", "literal:false", "punct:,",
      'key:"d"', "punct::", "literal:null",
    ]);
  });

  it("keeps escaped quotes inside a string", () => {
    expect(kinds('"path": "C:\\dir\\"x"')).toEqual(['key:"path"', "punct::", 'string:"C:\\dir\\"x"']);
  });

  it("does not take the start of a word for a literal or a number", () => {
    expect(kinds("nullable")).toEqual([]);
    expect(kinds("truest")).toEqual([]);
  });

  it("copes with an unterminated string and stray characters, and never loses text", () => {
    const lines = ['"open: 1', "}}} ?? @", '{"a": [1, 2, {"b": nul}]}', "\t\"tab\":\t1", ""];
    for (const l of lines) expect(tokenizeJsonLine(l).map((t) => t.text).join("")).toBe(l);
    expect(kinds('"open: 1')).toEqual(['string:"open: 1']);
  });

  it("leaves an enormous line uncoloured", () => {
    const long = '"k": "' + "x".repeat(MAX_COLOURED_LINE) + '"';
    expect(tokenizeJsonLine(long)).toEqual([{ type: "plain", text: long }]);
  });

  it("tokenizes a whole document per line", () => {
    const doc = JSON.stringify({ a: 1, b: [true, null, "x"] }, null, 2);
    const lines = tokenizeJson(doc);
    expect(lines).toHaveLength(doc.split("\n").length);
    expect(lines.map((l) => l.map((t) => t.text).join("")).join("\n")).toBe(doc);
  });
});

describe("JsonEditor", () => {
  it("draws a coloured copy under a real textarea and reports edits", () => {
    let latest = "";
    render(<JsonEditor value={'{\n  "a": 1\n}'} ariaLabel="Config" onChange={(v) => (latest = v)} />);
    const layer = screen.getByTestId("json-layer");
    expect(layer.querySelector(".rp-json-key")?.textContent).toBe('"a"');
    expect(layer.querySelector(".rp-json-number")?.textContent).toBe("1");
    const box = screen.getByLabelText("Config") as HTMLTextAreaElement;
    expect(box.value).toBe('{\n  "a": 1\n}');
    fireEvent.change(box, { target: { value: "{}" } });
    expect(latest).toBe("{}");
  });

  it("numbers the lines and marks the failing one", () => {
    render(<JsonEditor value={"{\n  1\n}"} errorLine={2} ariaLabel="Config" />);
    const gutter = within(screen.getByTestId("json-gutter"));
    expect(gutter.getByText("1")).toBeInTheDocument();
    expect(gutter.getByText("2").className).toContain("text-rapido-bad");
    expect(gutter.getByText("3")).toBeInTheDocument();
  });

  it("only draws the lines in view for a very large document", () => {
    const big = Array.from({ length: 20000 }, (_, i) => `  "k${i}": ${i},`).join("\n");
    render(<JsonEditor value={big} rows={20} ariaLabel="Big" />);
    const drawn = screen.getByTestId("json-layer").children.length;
    expect(drawn).toBeGreaterThan(10);
    expect(drawn).toBeLessThan(80);
  });

  it("can be read-only and gutterless", () => {
    render(<JsonEditor readOnly gutter={false} value="{}" ariaLabel="Out" />);
    expect(screen.queryByTestId("json-gutter")).toBeNull();
    expect(screen.getByLabelText("Out")).toHaveAttribute("readonly");
  });

  // The coloured copy is placed by arithmetic on a pixel line height, so the textarea has to use
  // exactly that one: on the Persian UI `[dir="rtl"] .text-xs` outranks any leading-* class, which
  // left the visible text 35 lines away from the caret on an 855-line document.
  it("gives the textarea and both layers the same line height, inline where no class can beat it", () => {
    render(<JsonEditor value={"{\n}"} ariaLabel="Config" />);
    const box = screen.getByLabelText("Config");
    const layer = screen.getByTestId("json-layer");
    const gutter = screen.getByTestId("json-gutter");
    for (const el of [box, layer, gutter]) {
      expect(el.style.lineHeight).toBe("20px");
      expect(el.className).not.toMatch(/\bleading-/);
    }
  });

  // Measuring runs after every commit. Under a forwardRef, an update that changes nothing can
  // still re-render, which came straight back to measuring until React gave up: the page died
  // the moment the code tab was opened.
  it("doesn't re-render itself when measuring finds nothing new", () => {
    let commits = 0;
    const tree = (value: string) => (
      <Profiler id="editor" onRender={() => commits++}>
        <JsonEditor value={value} ariaLabel="Config" />
      </Profiler>
    );
    const { rerender } = render(tree("{}"));
    const settled = commits;
    rerender(tree("{}"));
    expect(commits).toBe(settled + 1);
    rerender(tree("{ }"));
    expect(commits).toBe(settled + 2);
  });

  it("re-draws when it goes from hidden to shown, then settles", () => {
    let commits = 0;
    const tree = (
      <Profiler id="editor" onRender={() => commits++}>
        <JsonEditor value={"{}"} rows={10} ariaLabel="Config" />
      </Profiler>
    );
    const { rerender } = render(tree);
    const box = screen.getByLabelText("Config");
    Object.defineProperty(box, "clientHeight", { configurable: true, value: 216 });
    const before = commits;
    rerender(
      <Profiler id="editor" onRender={() => commits++}>
        <JsonEditor value={"{}"} rows={10} ariaLabel="Config" />
      </Profiler>
    );
    // The render that noticed, and the one with the new height.
    expect(commits - before).toBeLessThanOrEqual(2);
  });

  it("reveals a line: caret at its start, scrolled into view, and reported", () => {
    const value = Array.from({ length: 200 }, (_, i) => `line${i + 1}`).join("\n");
    const ref = createRef<JsonEditorHandle>();
    const carets: { line: number; column: number }[] = [];
    render(<JsonEditor ref={ref} value={value} rows={10} ariaLabel="Config" onCaretChange={(c) => carets.push(c)} />);
    const box = screen.getByLabelText("Config") as HTMLTextAreaElement;
    Object.defineProperty(box, "clientHeight", { configurable: true, value: 216 });

    act(() => ref.current?.revealLine(100));

    expect(value.slice(box.selectionStart, box.selectionStart + 7)).toBe("line100");
    expect(box.scrollTop).toBe(99 * 20 - 72);
    expect(document.activeElement).toBe(box);
    expect(carets.at(-1)).toEqual({ line: 100, column: 1 });
  });

  describe("code keys", () => {
    const Harness = ({ initial, readOnly = false }: { initial: string; readOnly?: boolean }) => {
      const [value, setValue] = useState(initial);
      return <JsonEditor codeKeys readOnly={readOnly} value={value} ariaLabel="Config" onChange={setValue} />;
    };
    const box = () => screen.getByLabelText("Config") as HTMLTextAreaElement;
    const caret = (at: number) => box().setSelectionRange(at, at);

    it("Tab indents and stays in the box", () => {
      render(<Harness initial={"a"} />);
      caret(0);
      const notPrevented = fireEvent.keyDown(box(), { key: "Tab" });
      expect(notPrevented).toBe(false);
      expect(box().value).toBe("  a");
      expect(box().selectionStart).toBe(2);
    });

    it("Shift+Tab outdents", () => {
      render(<Harness initial={"    a"} />);
      caret(4);
      fireEvent.keyDown(box(), { key: "Tab", shiftKey: true });
      expect(box().value).toBe("  a");
    });

    it("Enter keeps the indent", () => {
      render(<Harness initial={'  "a": {'} />);
      caret(8);
      fireEvent.keyDown(box(), { key: "Enter" });
      expect(box().value).toBe('  "a": {\n    ');
      expect(box().selectionStart).toBe(13);
    });

    it("Escape then Tab leaves the box, including Shift+Tab with the Shift key pressed first", () => {
      render(<Harness initial={"a"} />);
      caret(0);
      fireEvent.keyDown(box(), { key: "Escape" });
      expect(fireEvent.keyDown(box(), { key: "Tab" })).toBe(true);
      expect(box().value).toBe("a");

      fireEvent.keyDown(box(), { key: "Escape" });
      fireEvent.keyDown(box(), { key: "Shift", shiftKey: true });
      expect(fireEvent.keyDown(box(), { key: "Tab", shiftKey: true })).toBe(true);
      expect(box().value).toBe("a");

      // Only the very next key: after that, Tab indents again.
      fireEvent.keyDown(box(), { key: "Escape" });
      fireEvent.keyDown(box(), { key: "ArrowLeft" });
      expect(fireEvent.keyDown(box(), { key: "Tab" })).toBe(false);
    });

    it("leaves browser shortcuts and read-only documents alone", () => {
      render(<Harness initial={"a"} />);
      expect(fireEvent.keyDown(box(), { key: "Tab", ctrlKey: true })).toBe(true);
      expect(fireEvent.keyDown(box(), { key: "Enter", ctrlKey: true })).toBe(true);
      expect(box().value).toBe("a");
    });

    it("does nothing when read-only", () => {
      render(<Harness initial={"a"} readOnly />);
      expect(fireEvent.keyDown(box(), { key: "Tab" })).toBe(true);
      expect(fireEvent.keyDown(box(), { key: "Enter" })).toBe(true);
      expect(box().value).toBe("a");
    });

    it("is off by default, so a pasted-into box keeps normal Tab order", () => {
      render(<JsonEditor value="a" ariaLabel="Plain" onChange={() => {}} />);
      expect(fireEvent.keyDown(screen.getByLabelText("Plain"), { key: "Tab" })).toBe(true);
    });
  });
});
