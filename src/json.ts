/**
 * JSON-compatible tree utilities.
 *
 * Semantics: object keys are unordered, arrays are ordered. Equality and
 * hashing therefore ignore object key order but respect array order.
 */

export type JsonObject = { [key: string]: JsonValue };
export type JsonArray = JsonValue[];
export type JsonValue = null | boolean | number | string | JsonArray | JsonObject;

/** A path into a JSON tree: string segments are object keys, numbers are array indices. */
export type JsonPath = ReadonlyArray<string | number>;

export function isJsonObject(value: JsonValue): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Format a path as a JSONPath-like string, e.g. `$.items[2].name`. */
export function formatPath(path: JsonPath): string {
  let out = '$';
  for (const seg of path) {
    if (typeof seg === 'number') {
      out += `[${seg}]`;
    } else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(seg)) {
      out += `.${seg}`;
    } else {
      out += `[${JSON.stringify(seg)}]`;
    }
  }
  return out;
}

/** Parse a JSONPath-like string produced by {@link formatPath}. */
export function parsePath(text: string): JsonPath {
  if (!text.startsWith('$')) throw new Error(`invalid path: ${text}`);
  const segs: (string | number)[] = [];
  let i = 1;
  while (i < text.length) {
    if (text[i] === '.') {
      const m = /^\.([A-Za-z_$][A-Za-z0-9_$]*)/.exec(text.slice(i));
      if (!m) throw new Error(`invalid path: ${text}`);
      segs.push(m[1]);
      i += m[0].length;
    } else if (text[i] === '[') {
      const end = text.indexOf(']', i);
      if (end < 0) throw new Error(`invalid path: ${text}`);
      const inner = text.slice(i + 1, end);
      if (/^\d+$/.test(inner)) segs.push(Number(inner));
      else segs.push(JSON.parse(inner) as string);
      i = end + 1;
    } else {
      throw new Error(`invalid path: ${text}`);
    }
  }
  return segs;
}

/** Resolve a path against a tree; returns undefined when any segment is missing. */
export function getAtPath(root: JsonValue, path: JsonPath): JsonValue | undefined {
  let node: JsonValue | undefined = root;
  for (const seg of path) {
    if (typeof seg === 'number') {
      if (!Array.isArray(node) || seg >= node.length) return undefined;
      node = node[seg];
    } else {
      if (!isJsonObject(node) || !(seg in node)) return undefined;
      node = node[seg];
    }
  }
  return node;
}

/**
 * Canonical serialization: object keys sorted recursively, arrays kept in
 * order. Two trees that differ only in object key order canonicalize to the
 * same string, so it is a stable identity for dedup/checkpoint hashing.
 */
export function canonicalize(value: JsonValue): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) {
    return `[${value.map(canonicalize).join(',')}]`;
  }
  const keys = Object.keys(value).sort();
  const body = keys.map((k) => `${JSON.stringify(k)}:${canonicalize(value[k])}`).join(',');
  return `{${body}}`;
}

/** Key-order-insensitive structural equality (arrays remain order-sensitive). */
export function jsonEquals(a: JsonValue, b: JsonValue): boolean {
  return canonicalize(a) === canonicalize(b);
}
