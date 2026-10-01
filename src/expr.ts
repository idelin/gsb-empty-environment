import { TemplateError } from "./errors.ts";

/** Literal argument allowed inside a pipe call: string / number / boolean / null. */
export type Literal = string | number | boolean | null;

export interface Pipe {
  name: string;
  args: Literal[];
}

/** A template expression: a path plus zero or more piped function calls. */
export interface Expr {
  path: string[];
  pipes: Pipe[];
}

const IDENT = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const INDEX = /^(0|[1-9][0-9]*)$/;

/** Parse "a.b.0.c" into segments. `$root` is a valid first segment. */
export function parsePath(src: string, where: string): string[] {
  const segs = src.split(".");
  if (segs.length === 0 || segs.some((s) => s.length === 0)) {
    throw new TemplateError(`invalid path "${src}"`, where);
  }
  for (const s of segs) {
    if (!IDENT.test(s) && !INDEX.test(s)) {
      throw new TemplateError(`invalid path segment "${s}" in "${src}"`, where);
    }
  }
  return segs;
}

/** Split on top-level `|` (ignoring pipes inside quotes/parens). */
function splitPipes(src: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let quote: string | null = null;
  let cur = "";
  for (let i = 0; i < src.length; i++) {
    const c = src[i];
    if (quote !== null) {
      cur += c;
      if (c === "\\" && i + 1 < src.length) {
        cur += src[i + 1];
        i++;
        continue;
      }
      if (c === quote) quote = null;
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      cur += c;
      continue;
    }
    if (c === "(") depth++;
    if (c === ")") depth--;
    if (c === "|" && depth === 0) {
      parts.push(cur);
      cur = "";
      continue;
    }
    cur += c;
  }
  parts.push(cur);
  return parts;
}

function splitArgs(src: string): string[] {
  const parts: string[] = [];
  let quote: string | null = null;
  let cur = "";
  for (let i = 0; i < src.length; i++) {
    const c = src[i];
    if (quote !== null) {
      cur += c;
      if (c === "\\" && i + 1 < src.length) {
        cur += src[i + 1];
        i++;
        continue;
      }
      if (c === quote) quote = null;
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      cur += c;
      continue;
    }
    if (c === ",") {
      parts.push(cur);
      cur = "";
      continue;
    }
    cur += c;
  }
  parts.push(cur);
  return parts;
}

function parseLiteral(src: string, where: string): Literal {
  const s = src.trim();
  if (s.length >= 2 && (s[0] === "'" || s[0] === '"') && s[s.length - 1] === s[0]) {
    const body = s.slice(1, -1);
    let out = "";
    for (let i = 0; i < body.length; i++) {
      const c = body[i];
      if (c === "\\" && i + 1 < body.length) {
        const n = body[i + 1];
        if (n === "n") out += "\n";
        else if (n === "t") out += "\t";
        else out += n;
        i++;
        continue;
      }
      out += c;
    }
    return out;
  }
  if (s === "true") return true;
  if (s === "false") return false;
  if (s === "null") return null;
  if (/^-?[0-9]+(\.[0-9]+)?$/.test(s)) return Number(s);
  throw new TemplateError(`invalid literal argument "${s}"`, where);
}

function parsePipe(src: string, where: string, fnNames: ReadonlySet<string>): Pipe {
  const m = /^([A-Za-z_$][A-Za-z0-9_$]*)(?:\((.*)\))?$/s.exec(src);
  if (!m) throw new TemplateError(`malformed pipe "${src}"`, where);
  const name = m[1];
  if (!fnNames.has(name)) {
    throw new TemplateError(`unknown function "${name}" (not in whitelist)`, where);
  }
  const argsSrc = m[2];
  const args: Literal[] =
    argsSrc === undefined || argsSrc.trim() === ""
      ? []
      : splitArgs(argsSrc).map((a) => parseLiteral(a, where));
  return { name, args };
}

/** Parse "path.to.value | fn('a', 1) | other" into an Expr. */
export function parseExpr(src: string, where: string, fnNames: ReadonlySet<string>): Expr {
  const parts = splitPipes(src).map((p) => p.trim());
  if (parts.some((p) => p.length === 0)) {
    throw new TemplateError(`malformed expression "${src}"`, where);
  }
  const path = parsePath(parts[0], where);
  const pipes = parts.slice(1).map((p) => parsePipe(p, where, fnNames));
  return { path, pipes };
}
