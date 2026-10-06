import { parse, serializeEntry, splitLines } from "./parser.ts";
import type { CstFile, Diagnostic } from "./parser.ts";

/** A parsed document that edits can be applied to. */
export interface Document {
  src: string;
  file: CstFile;
  diagnostics: Diagnostic[];
}

export function parseDocument(src: string): Document {
  const r = parse(src);
  return { src, file: r.file, diagnostics: r.diagnostics };
}

export interface ByteRange {
  start: number;
  end: number;
}

export type EditResult =
  | {
      ok: true;
      doc: Document;
      /** Byte range of the changed region in the new text. */
      range: ByteRange;
      /** Corresponding byte range in the original text. */
      originalRange: ByteRange;
    }
  | {
      ok: false;
      /** Diagnostics located in the ORIGINAL document's coordinates. */
      diagnostics: Diagnostic[];
    };

/** Line index of the entry at `path` (keys from the top level), or -1. */
export function findEntry(file: CstFile, path: string[]): number {
  let candidates = file.lines.filter((l) => l.kind === "entry" && l.depth === 0);
  let found = -1;
  for (const key of path) {
    const next = candidates.find((l) => l.key === key);
    if (!next) return -1;
    found = next.index;
    candidates = next.children.map((i) => file.lines[i]!);
  }
  return found;
}

/** Last physical line index owned by the block rooted at `idx`. */
export function subtreeEnd(file: CstFile, idx: number): number {
  let end = idx;
  const walk = (i: number) => {
    if (i > end) end = i;
    for (const c of file.lines[i]!.children) walk(c);
  };
  walk(idx);
  return end;
}

function dominantNewline(file: CstFile): string {
  let crlf = 0;
  let lf = 0;
  for (const l of file.lines) {
    if (l.newline === "\r\n") crlf++;
    else if (l.newline === "\n") lf++;
  }
  return crlf > lf ? "\r\n" : "\n";
}

function indentUnit(file: CstFile): string {
  for (const l of file.lines) if (l.kind === "entry" && l.depth === 1) return l.indent;
  return "    ";
}

/**
 * lineMap encoding per candidate line:
 *   >= 0  identity: original line index
 *   -1    inserted line, no anchor (empty original)
 *   <= -2 inserted line, anchored at original line (-v - 2)
 */
function mapDiagnostic(d: Diagnostic, lineMap: number[], doc: Document): Diagnostic {
  const mapped = lineMap[d.line - 1];
  if (mapped === undefined || mapped === -1) {
    return { ...d, line: 1, col: 1, endLine: 1, endCol: 1, message: d.message + " (in inserted text)" };
  }
  if (mapped >= 0) return { ...d, line: mapped + 1, endLine: mapped + 1 };
  const anchor = -mapped - 2;
  const body = doc.file.lines[anchor]!.body;
  return {
    ...d,
    line: anchor + 1,
    col: 1,
    endLine: anchor + 1,
    endCol: body.length + 1,
    message: d.message + " (in inserted text)",
  };
}

function applyCandidate(doc: Document, newRaws: string[], lineMap: number[]): EditResult {
  const text = newRaws.join("");
  const reparsed = parseDocument(text);
  if (reparsed.diagnostics.length > 0) {
    // Atomic rejection: the original document is returned untouched and
    // every diagnostic is mapped back into original-file coordinates.
    return { ok: false, diagnostics: reparsed.diagnostics.map((d) => mapDiagnostic(d, lineMap, doc)) };
  }
  const oldRaws = doc.file.lines.map((l) => l.raw);
  let p = 0;
  while (p < oldRaws.length && p < newRaws.length && oldRaws[p] === newRaws[p]) p++;
  let s = 0;
  while (
    s < oldRaws.length - p &&
    s < newRaws.length - p &&
    oldRaws[oldRaws.length - 1 - s] === newRaws[newRaws.length - 1 - s]
  ) {
    s++;
  }
  const offset = (raws: string[], n: number) => {
    let o = 0;
    for (let i = 0; i < n; i++) o += raws[i]!.length;
    return o;
  };
  return {
    ok: true,
    doc: reparsed,
    range: { start: offset(newRaws, p), end: offset(newRaws, newRaws.length - s) },
    originalRange: { start: offset(oldRaws, p), end: offset(oldRaws, oldRaws.length - s) },
  };
}

/** Set the value of a leaf entry (`key: value`). */
export function setValue(doc: Document, path: string[], value: string): EditResult {
  if (doc.diagnostics.length > 0) return { ok: false, diagnostics: doc.diagnostics };
  if (value.includes("\n") || value.includes("\r")) {
    throw new Error("value must be a single line");
  }
  const idx = findEntry(doc.file, path);
  if (idx < 0) throw new Error(`no entry at path: ${path.join(" / ")}`);
  const raws = doc.file.lines.map((l) => l.raw);
  raws[idx] = serializeEntry(doc.file.lines[idx]!, value);
  return applyCandidate(doc, raws, raws.map((_, i) => i));
}

/** Remove the entry at `path` together with its whole nested block. */
export function removeBlock(doc: Document, path: string[]): EditResult {
  if (doc.diagnostics.length > 0) return { ok: false, diagnostics: doc.diagnostics };
  const idx = findEntry(doc.file, path);
  if (idx < 0) throw new Error(`no entry at path: ${path.join(" / ")}`);
  const end = subtreeEnd(doc.file, idx);
  const raws = doc.file.lines.map((l) => l.raw);
  const newRaws = raws.slice(0, idx).concat(raws.slice(end + 1));
  const lineMap = newRaws.map((_, i) => (i < idx ? i : i + (end + 1 - idx)));
  return applyCandidate(doc, newRaws, lineMap);
}

/**
 * Insert a block (given at relative indentation, first line at column 0)
 * as the last child of the entry at `path`, or at the top level when
 * `path` is empty. Inserted lines are re-indented under the parent.
 */
export function addBlock(doc: Document, path: string[], blockText: string): EditResult {
  if (doc.diagnostics.length > 0) return { ok: false, diagnostics: doc.diagnostics };
  const nl = dominantNewline(doc.file);
  let parentIdx = -1;
  if (path.length > 0) {
    parentIdx = findEntry(doc.file, path);
    if (parentIdx < 0) throw new Error(`no entry at path: ${path.join(" / ")}`);
  }
  let childPrefix: string;
  if (parentIdx >= 0) {
    const parent = doc.file.lines[parentIdx]!;
    childPrefix =
      parent.children.length > 0
        ? doc.file.lines[parent.children[0]!]!.indent
        : parent.indent + indentUnit(doc.file);
  } else {
    childPrefix = "";
  }
  const inserted = splitLines(blockText).map(({ body }) =>
    body.trim() === "" ? nl : childPrefix + body + nl,
  );
  const raws = doc.file.lines.map((l) => l.raw);
  let at: number;
  let anchor: number;
  if (parentIdx >= 0) {
    at = subtreeEnd(doc.file, parentIdx) + 1;
    anchor = parentIdx;
  } else {
    at = raws.length;
    anchor = raws.length - 1;
  }
  if (at === raws.length && raws.length > 0) {
    const last = doc.file.lines[raws.length - 1]!;
    if (last.newline === "") raws[raws.length - 1] = last.raw + nl;
  }
  const newRaws = raws.slice(0, at).concat(inserted, raws.slice(at));
  const lineMap = newRaws.map((_, i) => {
    if (i < at) return i;
    if (i < at + inserted.length) return anchor >= 0 ? -(anchor + 2) : -1;
    return i - inserted.length;
  });
  return applyCandidate(doc, newRaws, lineMap);
}
