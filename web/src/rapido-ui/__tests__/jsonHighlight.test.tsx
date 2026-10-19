import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { JsonEditor } from "../JsonEditor";
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
});
