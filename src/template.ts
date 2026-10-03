import type { JsonValue } from "./json.ts";
import { parseExpr, type Expr } from "./expr.ts";

/** How to behave when an input path does not exist (distinct from explicit null). */
export type MissingPolicy = "error" | "null" | "omit" | "empty" | "keep";
export const MISSING_POLICIES: MissingPolicy[] = ["error", "null", "omit", "empty", "keep"];

export class TemplateCompileError extends Error {
  readonly tpath: string;
  constructor(msg: string, tpath: string) {
    super(`${msg} (template ${tpath || "/"})`);
    this.tpath = tpath;
  }
}

export type StrPart = { s: string } | { e: Expr; raw: string };

export interface TBase {
  tpath: string;
}
export interface TValue extends TBase {
  kind: "value";
  v: JsonValue;
}
export interface TString extends TBase {
  kind: "string";
  parts: StrPart[];
  /** true when the whole string is exactly one ${...} expression (type preserved). */
  single: boolean;
}
export interface TObject extends TBase {
  kind: "object";
  entries: { key: string; val: TNode }[];
}
export interface TArray extends TBase {
  kind: "array";
  items: TNode[];
}
export interface TFor extends TBase {
  kind: "for";
  expr: Expr; // must be a path expression
  as: string;
  indexAs: string | null;
  body: TNode;
  missing: MissingPolicy | null;
  usesIndex: boolean;
}
export interface TIf extends TBase {
  kind: "if";
  cond: Expr;
  then: TNode;
  otherwise: TNode | null;
  missing: MissingPolicy | null;
}
export interface TGet extends TBase {
  kind: "get";
  expr: Expr;
  missing: MissingPolicy | null;
  raw: string; // original source, used by the "keep" policy
}
export type TNode = TValue | TString | TObject | TArray | TFor | TIf | TGet;

function parsePolicy(v: JsonValue | undefined, tpath: string): MissingPolicy | null {
  if (v === undefined) return null;
  if (typeof v === "string" && (MISSING_POLICIES as string[]).includes(v)) {
    return v as MissingPolicy;
  }
  throw new TemplateCompileError(`invalid missing policy ${JSON.stringify(v)}`, tpath);
}

/** Splits a template string into literal and ${expr} parts. Supports \${ and \\ escapes. */
function splitParts(src: string, tpath: string): StrPart[] {
  const parts: StrPart[] = [];
  let buf = "";
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (ch === "\\" && i + 1 < src.length && (src[i + 1] === "$" || src[i + 1] === "\\")) {
      buf += src[i + 1];
      i += 2;
      continue;
    }
    if (ch === "$" && src[i + 1] === "{") {
      let j = i + 2;
      let quote = "";
      let exprSrc = "";
      let closed = false;
      while (j < src.length) {
        const c = src[j];
        if (quote) {
          if (c === "\\" && j + 1 < src.length) {
            exprSrc += c + src[j + 1];
            j += 2;
            continue;
          }
          if (c === quote) quote = "";
          exprSrc += c;
          j++;
          continue;
        }
        if (c === "'" || c === '"') {
          quote = c;
          exprSrc += c;
          j++;
          continue;
        }
        if (c === "}") {
          closed = true;
          break;
        }
        exprSrc += c;
        j++;
      }
      if (!closed) throw new TemplateCompileError(`unclosed \${...} in ${JSON.stringify(src)}`, tpath);
      if (buf) {
        parts.push({ s: buf });
        buf = "";
      }
      let expr: Expr;
      try {
        expr = parseExpr(exprSrc);
      } catch (e) {
        throw new TemplateCompileError((e as Error).message, tpath);
      }
      parts.push({ e: expr, raw: exprSrc });
      i = j + 1;
      continue;
    }
    buf += ch;
    i++;
  }
  if (buf) parts.push({ s: buf });
  return parts;
}

/** Compiles a directive field that holds an expression (string or {$call}/{$get}/{$lit} form). */
function compileExprJson(v: JsonValue, tpath: string): Expr {
  if (typeof v === "string") {
    try {
      return parseExpr(v);
    } catch (e) {
      throw new TemplateCompileError((e as Error).message, tpath);
    }
  }
  if (v !== null && typeof v === "object" && !Array.isArray(v)) {
    const o = v as { [k: string]: JsonValue };
    if ("$call" in o) {
      if (typeof o.$call !== "string") throw new TemplateCompileError("$call expects a function name", tpath);
      const rawArgs = Array.isArray(o.args) ? (o.args as JsonValue[]) : [];
      return {
        t: "call",
        fn: o.$call,
        args: rawArgs.map((a, k) => compileExprJson(a, `${tpath}/args/${k}`)),
      };
    }
    if ("$get" in o) return compileExprJson(o.$get, `${tpath}/$get`);
    if ("$lit" in o) return { t: "lit", v: o.$lit };
  }
  return { t: "lit", v };
}

function exprUsesVar(e: Expr, varName: string): boolean {
  if (e.t === "path") return e.segs[0] === "@" + varName;
  if (e.t === "call") return e.args.some((a) => exprUsesVar(a, varName));
  return false;
}

function templateUsesVar(t: TNode, varName: string): boolean {
  switch (t.kind) {
    case "value":
      return false;
    case "string":
      return t.parts.some((p) => "e" in p && exprUsesVar(p.e, varName));
    case "object":
      return t.entries.some((en) => templateUsesVar(en.val, varName));
    case "array":
      return t.items.some((x) => templateUsesVar(x, varName));
    case "for":
      return exprUsesVar(t.expr, varName) || templateUsesVar(t.body, varName);
    case "if":
      return (
        exprUsesVar(t.cond, varName) ||
        templateUsesVar(t.then, varName) ||
        (t.otherwise !== null && templateUsesVar(t.otherwise, varName))
      );
    case "get":
      return exprUsesVar(t.expr, varName);
  }
}

export function compileTemplate(tpl: JsonValue): TNode {
  return compileNode(tpl, "");
}

function compileNode(tpl: JsonValue, tpath: string): TNode {
  if (tpl === null || typeof tpl !== "object") {
    if (typeof tpl === "string") {
      const parts = splitParts(tpl, tpath);
      const single = parts.length === 1 && "e" in parts[0];
      return { kind: "string", tpath, parts, single };
    }
    return { kind: "value", tpath, v: tpl };
  }
  if (Array.isArray(tpl)) {
    return {
      kind: "array",
      tpath,
      items: tpl.map((x, k) => compileNode(x, `${tpath}/${k}`)),
    };
  }
  const o = tpl as { [k: string]: JsonValue };
  if ("$for" in o) {
    const expr = compileExprJson(o.$for, `${tpath}/$for`);
    if (expr.t !== "path") {
      throw new TemplateCompileError("$for expects a path expression", `${tpath}/$for`);
    }
    if (!("body" in o)) throw new TemplateCompileError("$for requires a body", tpath);
    const as = typeof o.as === "string" ? o.as : "it";
    const indexAs = typeof o.index === "string" ? o.index : null;
    const body = compileNode(o.body as JsonValue, `${tpath}/body`);
    return {
      kind: "for",
      tpath,
      expr,
      as,
      indexAs,
      body,
      missing: parsePolicy(o.missing, tpath),
      usesIndex: indexAs !== null && templateUsesVar(body, indexAs),
    };
  }
  if ("$if" in o) {
    if (!("then" in o)) throw new TemplateCompileError("$if requires then", tpath);
    return {
      kind: "if",
      tpath,
      cond: compileExprJson(o.$if, `${tpath}/$if`),
      then: compileNode(o.then as JsonValue, `${tpath}/then`),
      otherwise: "else" in o ? compileNode(o.else as JsonValue, `${tpath}/else`) : null,
      missing: parsePolicy(o.missing, tpath),
    };
  }
  if ("$get" in o) {
    return {
      kind: "get",
      tpath,
      expr: compileExprJson(o.$get, `${tpath}/$get`),
      missing: parsePolicy(o.missing, tpath),
      raw: typeof o.$get === "string" ? o.$get : JSON.stringify(o.$get),
    };
  }
  if ("$call" in o) {
    const expr = compileExprJson(o, tpath);
    return {
      kind: "get",
      tpath,
      expr,
      missing: parsePolicy(o.missing, tpath),
      raw: JSON.stringify(o),
    };
  }
  if ("$lit" in o) {
    return { kind: "value", tpath, v: o.$lit };
  }
  const entries: { key: string; val: TNode }[] = [];
  for (const k of Object.keys(o)) {
    if (k.startsWith("$")) throw new TemplateCompileError(`unknown directive ${k}`, `${tpath}/${k}`);
    entries.push({ key: k, val: compileNode(o[k], `${tpath}/${k}`) });
  }
  return { kind: "object", tpath, entries };
}
