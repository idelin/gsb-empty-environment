import type { JsonValue } from "./json.ts";
import { escapePointerSegment } from "./json.ts";
import { TemplateError } from "./errors.ts";
import { parseExpr, parsePath } from "./expr.ts";
import type { Expr } from "./expr.ts";

export type TextPart = { kind: "str"; text: string } | { kind: "expr"; expr: Expr };

/**
 * Template AST node. `id` is a JSON-pointer-like location of the node inside
 * the template ("" for the root, "@partial/<name>/..." for partials), used in
 * errors and provenance.
 */
export type TNode =
  | { kind: "literal"; id: string; value: JsonValue }
  | { kind: "text"; id: string; parts: TextPart[] }
  | { kind: "value"; id: string; expr: Expr }
  | { kind: "object"; id: string; entries: Array<[string, TNode]> }
  | { kind: "array"; id: string; items: TNode[] }
  | { kind: "for"; id: string; path: string[]; as: string; indexAs: string | null; body: TNode }
  | { kind: "if"; id: string; expr: Expr; then: TNode; else: TNode | null }
  | { kind: "ref"; id: string; name: string };

export interface ParsedTemplate {
  root: TNode;
  partials: Map<string, TNode>;
}

export function parseTemplate(
  template: JsonValue,
  fnNames: ReadonlySet<string>,
  partials?: Record<string, JsonValue>,
): ParsedTemplate {
  const partialMap = new Map<string, TNode>();
  if (partials !== undefined) {
    for (const name of Object.keys(partials)) {
      partialMap.set(name, parseNode(partials[name], `@partial/${escapePointerSegment(name)}`, fnNames));
    }
  }
  const root = parseNode(template, "", fnNames);
  return { root, partials: partialMap };
}

function parseNode(node: JsonValue, id: string, fnNames: ReadonlySet<string>): TNode {
  if (node === null || typeof node === "boolean" || typeof node === "number") {
    return { kind: "literal", id, value: node };
  }
  if (typeof node === "string") return parseString(node, id, fnNames);
  if (Array.isArray(node)) {
    const items = node.map((item, i) => parseNode(item, `${id}/${i}`, fnNames));
    return { kind: "array", id, items };
  }
  const keys = Object.keys(node);
  const hasDirective = keys.some((k) => k.startsWith("$"));
  if (hasDirective) {
    if ("$for" in node) return parseFor(node, id, fnNames);
    if ("$if" in node) return parseIf(node, id, fnNames);
    if ("$ref" in node) return parseRef(node, id);
    const bad = keys.find((k) => k.startsWith("$"));
    throw new TemplateError(`unknown directive "${String(bad)}"`, id);
  }
  const entries: Array<[string, TNode]> = keys.map((k) => [
    k,
    parseNode(node[k], `${id}/${escapePointerSegment(k)}`, fnNames),
  ]);
  return { kind: "object", id, entries };
}

function assertKeys(node: object, allowed: string[], directive: string, id: string): void {
  for (const k of Object.keys(node)) {
    if (!allowed.includes(k)) {
      throw new TemplateError(`unknown key "${k}" in ${directive} (allowed: ${allowed.join(", ")})`, id);
    }
  }
}

function parseFor(node: { [k: string]: JsonValue }, id: string, fnNames: ReadonlySet<string>): TNode {
  assertKeys(node, ["$for", "$as", "$index", "$body"], "$for", id);
  const src = node["$for"];
  if (typeof src !== "string") throw new TemplateError("$for must be a path string", id);
  const path = parsePath(src, id);
  const asRaw = node["$as"] === undefined ? "item" : node["$as"];
  if (typeof asRaw !== "string" || asRaw.length === 0) {
    throw new TemplateError("$as must be a non-empty string", id);
  }
  const indexRaw = node["$index"] === undefined ? null : node["$index"];
  if (indexRaw !== null && (typeof indexRaw !== "string" || indexRaw.length === 0)) {
    throw new TemplateError("$index must be a non-empty string", id);
  }
  if (!("$body" in node)) throw new TemplateError("$for requires $body", id);
  const body = parseNode(node["$body"], `${id}/$body`, fnNames);
  return { kind: "for", id, path, as: asRaw, indexAs: indexRaw as string | null, body };
}

function parseIf(node: { [k: string]: JsonValue }, id: string, fnNames: ReadonlySet<string>): TNode {
  assertKeys(node, ["$if", "$then", "$else"], "$if", id);
  const src = node["$if"];
  if (typeof src !== "string") throw new TemplateError("$if must be an expression string", id);
  const expr = parseExpr(src, id, fnNames);
  if (!("$then" in node)) throw new TemplateError("$if requires $then", id);
  const then = parseNode(node["$then"], `${id}/$then`, fnNames);
  const otherwise = "$else" in node ? parseNode(node["$else"], `${id}/$else`, fnNames) : null;
  return { kind: "if", id, expr, then, else: otherwise };
}

function parseRef(node: { [k: string]: JsonValue }, id: string): TNode {
  assertKeys(node, ["$ref"], "$ref", id);
  const name = node["$ref"];
  if (typeof name !== "string" || name.length === 0) {
    throw new TemplateError("$ref must be a non-empty partial name", id);
  }
  return { kind: "ref", id, name };
}

/** Read a `${ ... }` body starting at `start` (just after "{"), quote-aware. */
function readExpr(src: string, start: number, id: string): { exprSrc: string; end: number } {
  let quote: string | null = null;
  for (let i = start; i < src.length; i++) {
    const c = src[i];
    if (quote !== null) {
      if (c === "\\") {
        i++;
        continue;
      }
      if (c === quote) quote = null;
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      continue;
    }
    if (c === "}") return { exprSrc: src.slice(start, i), end: i };
  }
  throw new TemplateError('unterminated "${" in template string', id);
}

/**
 * Tokenize a template string. `\${` keeps a literal `${`. A string that is
 * exactly one expression becomes a typed "value" node (type preserved).
 */
function parseString(src: string, id: string, fnNames: ReadonlySet<string>): TNode {
  const parts: TextPart[] = [];
  let buf = "";
  let i = 0;
  while (i < src.length) {
    const c = src[i];
    if (c === "\\" && i + 1 < src.length && src[i + 1] === "$") {
      buf += "$";
      i += 2;
      continue;
    }
    if (c === "$" && i + 1 < src.length && src[i + 1] === "{") {
      const { exprSrc, end } = readExpr(src, i + 2, id);
      if (buf.length > 0) {
        parts.push({ kind: "str", text: buf });
        buf = "";
      }
      parts.push({ kind: "expr", expr: parseExpr(exprSrc, id, fnNames) });
      i = end + 1;
      continue;
    }
    buf += c;
    i++;
  }
  if (buf.length > 0) parts.push({ kind: "str", text: buf });
  if (parts.length === 0) return { kind: "literal", id, value: "" };
  if (parts.length === 1 && parts[0].kind === "expr") {
    return { kind: "value", id, expr: parts[0].expr };
  }
  return { kind: "text", id, parts };
}
