import { visualColumn } from "./indent.ts";

export type DiagCode =
  | "missing-colon"
  | "empty-key"
  | "unexpected-indent"
  | "bad-dedent"
  | "ambiguous-indent";

export interface Diagnostic {
  code: DiagCode;
  message: string;
  /** 1-based line in the parsed source. */
  line: number;
  /** 1-based column of the offending range start. */
  col: number;
  endLine: number;
  endCol: number;
}

export type LineKind = "blank" | "comment" | "entry" | "error";

/**
 * One physical line of the concrete syntax tree. `raw` always holds the
 * original bytes (including the line terminator), so an unmodified tree
 * reprints byte-for-byte. Entry lines additionally carry parsed fields so
 * a modified line can be re-serialized on its own.
 */
export interface CstLine {
  index: number;
  raw: string;
  body: string;
  newline: string;
  indent: string;
  kind: LineKind;
  errorCode: DiagCode | null;
  key: string;
  keyRaw: string;
  sepRaw: string;
  valueRaw: string;
  hasValue: boolean;
  value: string;
  depth: number;
  parent: number;
  children: number[];
}

export interface CstFile {
  src: string;
  lines: CstLine[];
}

export interface ParseResult {
  file: CstFile;
  diagnostics: Diagnostic[];
}

/** Split into physical lines, preserving each line's original terminator. */
export function splitLines(src: string): { body: string; newline: string }[] {
  const out: { body: string; newline: string }[] = [];
  let i = 0;
  while (i < src.length) {
    let j = i;
    while (j < src.length && src[j] !== "\n" && src[j] !== "\r") j++;
    let newline = "";
    if (src[j] === "\r" && src[j + 1] === "\n") newline = "\r\n";
    else if (j < src.length) newline = src[j] as string;
    out.push({ body: src.slice(i, j), newline });
    i = j + newline.length;
  }
  return out;
}

function classify(body: string): Partial<CstLine> {
  const indent = (body.match(/^[ \t]*/) as RegExpMatchArray)[0];
  const rest = body.slice(indent.length);
  if (rest === "") return { kind: "blank", indent };
  if (rest.startsWith("#")) return { kind: "comment", indent };
  const ci = rest.indexOf(":");
  if (ci < 0) return { kind: "error", errorCode: "missing-colon", indent };
  const keyRaw = rest.slice(0, ci);
  const key = keyRaw.trim();
  if (key === "") return { kind: "error", errorCode: "empty-key", indent };
  const after = rest.slice(ci + 1);
  const ws = (after.match(/^[ \t]*/) as RegExpMatchArray)[0];
  const sepRaw = ":" + ws;
  const valueRaw = after.slice(ws.length);
  const hasValue = valueRaw.length > 0;
  return {
    kind: "entry",
    indent,
    key,
    keyRaw,
    sepRaw,
    valueRaw,
    hasValue,
    value: valueRaw.trimEnd(),
  };
}

/**
 * Parse a document into a full-fidelity CST plus diagnostics.
 *
 * Structure is driven by an explicit indentation stack. Blank lines and
 * comments carry no structure. A dedent must land exactly on an enclosing
 * level; two prefixes with the same visual column but different characters
 * are ambiguous. Any error skips lines until the next top-level (column 0)
 * logical line, where parsing resumes.
 */
export function parse(src: string): ParseResult {
  const lines: CstLine[] = splitLines(src).map((r, index) => {
    const base: CstLine = {
      index,
      raw: r.body + r.newline,
      body: r.body,
      newline: r.newline,
      indent: "",
      kind: "blank",
      errorCode: null,
      key: "",
      keyRaw: "",
      sepRaw: "",
      valueRaw: "",
      hasValue: false,
      value: "",
      depth: -1,
      parent: -1,
      children: [],
    };
    return Object.assign(base, classify(r.body));
  });

  const diagnostics: Diagnostic[] = [];
  const stack: { col: number; prefix: string }[] = [{ col: 0, prefix: "" }];
  const seen = new Map<number, { prefix: string; line: number }>([
    [0, { prefix: "", line: 0 }],
  ]);
  const entryAtDepth: number[] = [];
  let prevOpener = false;
  let recovering = false;

  const report = (line: CstLine, code: DiagCode, message: string, col: number, endCol: number) => {
    diagnostics.push({ code, message, line: line.index + 1, col, endLine: line.index + 1, endCol });
  };

  for (const line of lines) {
    if (line.kind === "blank" || line.kind === "comment") continue;
    const col = visualColumn(line.indent);
    if (recovering) {
      if (col !== 0) continue;
      recovering = false;
    }
    if (line.kind === "error") {
      const code = line.errorCode as DiagCode;
      const msg =
        code === "missing-colon" ? "expected 'key:' or 'key: value'" : "empty key before ':'";
      report(line, code, msg, 1, Math.max(1, line.body.length) + 1);
      recovering = true;
      continue;
    }
    const known = seen.get(col);
    if (known !== undefined && known.prefix !== line.indent) {
      report(
        line,
        "ambiguous-indent",
        `indentation prefix ${JSON.stringify(line.indent)} (column ${col}) conflicts with ` +
          `prefix ${JSON.stringify(known.prefix)} (column ${col}) first used on line ${known.line}`,
        1,
        line.indent.length + 1,
      );
      recovering = true;
      continue;
    }
    const top = stack[stack.length - 1]!;
    if (col > top.col) {
      if (!prevOpener) {
        report(
          line,
          "unexpected-indent",
          `indentation to column ${col} does not follow a block opener ('key:' with no value)`,
          1,
          line.indent.length + 1,
        );
        recovering = true;
        continue;
      }
      stack.push({ col, prefix: line.indent });
      seen.set(col, { prefix: line.indent, line: line.index + 1 });
    } else if (col < top.col) {
      while (stack[stack.length - 1]!.col > col) stack.pop();
      if (stack[stack.length - 1]!.col !== col) {
        report(
          line,
          "bad-dedent",
          `dedent to column ${col} does not match any enclosing indentation level`,
          1,
          line.indent.length + 1,
        );
        recovering = true;
        continue;
      }
    }
    const depth = stack.length - 1;
    line.depth = depth;
    const parentIdx = depth > 0 ? entryAtDepth[depth - 1]! : -1;
    line.parent = parentIdx;
    if (parentIdx >= 0) lines[parentIdx]!.children.push(line.index);
    entryAtDepth[depth] = line.index;
    entryAtDepth.length = depth + 1;
    prevOpener = !line.hasValue;
  }
  return { file: { src, lines }, diagnostics };
}

/** Byte-for-byte reprint of a (possibly unmodified) CST. */
export function reprint(file: CstFile): string {
  let out = "";
  for (const line of file.lines) out += line.raw;
  return out;
}

/**
 * Re-serialize a single entry line with a new value, preserving its
 * indentation, key spelling, separator style and line terminator.
 */
export function serializeEntry(line: CstLine, value: string): string {
  const sep = line.hasValue ? line.sepRaw : ": ";
  return line.indent + line.keyRaw + sep + value + line.newline;
}
