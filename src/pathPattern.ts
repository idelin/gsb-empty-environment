import { PseudonymizationError } from "./errors.ts";

export type ConcreteSegment =
  | { kind: "key"; name: string }
  | { kind: "index"; index: number };

type PatternSegment =
  | { kind: "key"; name: string }
  | { kind: "anyKey" }
  | { kind: "anyIndex" };

export interface CompiledPattern {
  source: string;
  segments: PatternSegment[];
}

/**
 * Parses "users[].contact.email" / "users.*.email".
 * `[]` matches any array index, `*` matches any single object key.
 */
export function compilePattern(pattern: string): CompiledPattern {
  if (typeof pattern !== "string" || pattern.length === 0) {
    throw new PseudonymizationError("INVALID_RULE", "$", "rule pattern must be a non-empty string");
  }
  const segments: PatternSegment[] = [];
  let buf = "";
  let i = 0;
  const flush = () => {
    segments.push(buf === "*" ? { kind: "anyKey" } : { kind: "key", name: buf });
    buf = "";
  };
  while (i < pattern.length) {
    const ch = pattern[i]!;
    if (ch === ".") {
      if (buf === "" && (i === 0 || pattern[i - 1] !== "]")) {
        throw new PseudonymizationError("INVALID_RULE", "$", `empty segment in pattern "${pattern}"`);
      }
      if (buf !== "") flush();
      i++;
    } else if (ch === "[") {
      if (pattern.slice(i, i + 2) !== "[]") {
        throw new PseudonymizationError("INVALID_RULE", "$", `only "[]" array wildcard is supported in "${pattern}"`);
      }
      if (buf !== "") flush();
      segments.push({ kind: "anyIndex" });
      i += 2;
    } else {
      buf += ch;
      i++;
    }
  }
  if (buf !== "") flush();
  if (pattern.endsWith(".") || segments.length === 0) {
    throw new PseudonymizationError("INVALID_RULE", "$", `empty segment in pattern "${pattern}"`);
  }
  return { source: pattern, segments };
}

export function matches(compiled: CompiledPattern, path: ConcreteSegment[]): boolean {
  const { segments } = compiled;
  if (segments.length !== path.length) return false;
  for (let i = 0; i < segments.length; i++) {
    const p = segments[i]!;
    const c = path[i]!;
    if (p.kind === "anyIndex") {
      if (c.kind !== "index") return false;
    } else if (p.kind === "anyKey") {
      if (c.kind !== "key") return false;
    } else {
      if (c.kind !== "key" || c.name !== p.name) return false;
    }
  }
  return true;
}

/** Renders a concrete path like $.users[0].email — never includes values. */
export function formatPath(path: ConcreteSegment[]): string {
  let out = "$";
  for (const seg of path) {
    out += seg.kind === "index" ? `[${seg.index}]` : `.${seg.name}`;
  }
  return out;
}
