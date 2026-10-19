// A small JSON tokenizer for syntax colouring. It works one line at a time: a JSON string
// cannot contain a raw newline, so no state has to carry over from one line to the next, and
// an editor only ever needs to tokenize the lines that are on screen.
//
// It colours; it does not validate. Anything it cannot place (a stray word, an unterminated
// string) comes back as "plain" or as a string running to the end of the line, and the
// concatenation of every token's text is always exactly the input line.

export type JsonTokenType = "key" | "string" | "number" | "literal" | "punct" | "plain";

export type JsonToken = { type: JsonTokenType; text: string };

const NUMBER = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/;
const LITERAL = /^(?:true|false|null)(?![\w$])/;

// Past this a single line (a minified blob pasted into the editor) is left uncoloured so typing
// in it stays responsive.
export const MAX_COLOURED_LINE = 4000;

export const tokenizeJsonLine = (line: string): JsonToken[] => {
  if (line.length > MAX_COLOURED_LINE) return [{ type: "plain", text: line }];
  const out: JsonToken[] = [];
  const push = (type: JsonTokenType, text: string) => {
    const last = out[out.length - 1];
    if (last && last.type === type && (type === "plain" || type === "punct")) last.text += text;
    else out.push({ type, text });
  };
  let i = 0;
  while (i < line.length) {
    const c = line[i];
    if (c === '"') {
      let j = i + 1;
      while (j < line.length && line[j] !== '"') j += line[j] === "\\" ? 2 : 1;
      const end = Math.min(j + 1, line.length);
      let k = end;
      while (k < line.length && (line[k] === " " || line[k] === "\t")) k++;
      push(j < line.length && line[k] === ":" ? "key" : "string", line.slice(i, end));
      i = end;
    } else if (c === " " || c === "\t") {
      let j = i;
      while (j < line.length && (line[j] === " " || line[j] === "\t")) j++;
      push("plain", line.slice(i, j));
      i = j;
    } else if (c === "{" || c === "}" || c === "[" || c === "]" || c === "," || c === ":") {
      push("punct", c);
      i++;
    } else {
      const rest = line.slice(i, i + 64);
      const num = NUMBER.exec(rest);
      const lit = num ? null : LITERAL.exec(rest);
      if (num) {
        push("number", num[0]);
        i += num[0].length;
      } else if (lit) {
        push("literal", lit[0]);
        i += lit[0].length;
      } else {
        push("plain", c);
        i++;
      }
    }
  }
  return out;
};

/** Tokenize a whole document, one entry per line. */
export const tokenizeJson = (text: string): JsonToken[][] => text.split("\n").map(tokenizeJsonLine);
