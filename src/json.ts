import { createHash } from "node:crypto";

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

/** Stable stringify: object keys sorted recursively, array order preserved. */
export function canonicalize(value: JsonValue): string {
  if (value === null) return "null";
  switch (typeof value) {
    case "boolean":
    case "number":
      return JSON.stringify(value);
    case "string":
      return JSON.stringify(value);
    case "object": {
      if (Array.isArray(value)) {
        return "[" + value.map(canonicalize).join(",") + "]";
      }
      const keys = Object.keys(value).sort();
      const parts = keys.map(
        (k) => JSON.stringify(k) + ":" + canonicalize(value[k]),
      );
      return "{" + parts.join(",") + "}";
    }
  }
}

export function digestOf(...canonicalParts: string[]): string {
  const hash = createHash("sha256");
  for (const part of canonicalParts) {
    hash.update(part);
    hash.update("\0");
  }
  return hash.digest("hex");
}

export function jsonEquals(a: JsonValue, b: JsonValue): boolean {
  return canonicalize(a) === canonicalize(b);
}

/** True when value is a JSON-compatible tree (no undefined/functions/NaN...). */
export function isJsonValue(value: unknown): boolean {
  if (value === null) return true;
  switch (typeof value) {
    case "boolean":
    case "string":
      return true;
    case "number":
      return Number.isFinite(value);
    case "object": {
      if (Array.isArray(value)) return value.every(isJsonValue);
      const proto = Object.getPrototypeOf(value);
      if (proto !== Object.prototype && proto !== null) return false;
      return Object.values(value as object).every(isJsonValue);
    }
    default:
      return false;
  }
}

/** "$.a.b[2]" style path joining. */
export function pathJoin(base: string, key: string | number): string {
  if (typeof key === "number") return `${base}[${key}]`;
  return /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(key)
    ? `${base}.${key}`
    : `${base}[${JSON.stringify(key)}]`;
}

export type PathSegment = string | number;

/** Parse "$.a[0].b" / "$.a[\"x y\"]" into segments. Throws on bad syntax. */
export function parsePath(path: string): PathSegment[] {
  if (!path.startsWith("$")) throw new Error(`path must start with $: ${path}`);
  const segments: PathSegment[] = [];
  let i = 1;
  while (i < path.length) {
    const ch = path[i];
    if (ch === ".") {
      i++;
      const m = /^[A-Za-z_$][A-Za-z0-9_$]*/.exec(path.slice(i));
      if (!m) throw new Error(`bad path segment at ${i} in ${path}`);
      segments.push(m[0]);
      i += m[0].length;
    } else if (ch === "[") {
      const rest = path.slice(i);
      const num = /^\[(\d+)\]/.exec(rest);
      if (num) {
        segments.push(Number(num[1]));
        i += num[0].length;
        continue;
      }
      const str = /^\["((?:[^"\\]|\\.)*)"\]/.exec(rest);
      if (str) {
        segments.push(JSON.parse(`"${str[1]}"`) as string);
        i += str[0].length;
        continue;
      }
      throw new Error(`bad index segment at ${i} in ${path}`);
    } else {
      throw new Error(`unexpected '${ch}' at ${i} in ${path}`);
    }
  }
  return segments;
}
