/** JSON-compatible tree model: object keys are unordered, arrays are ordered. */

export type JsonObject = { [key: string]: JsonValue };
export type JsonValue = null | boolean | number | string | JsonValue[] | JsonObject;

/** A location in the tree: string segments index objects, number segments index arrays. */
export type Path = readonly (string | number)[];

export function isObject(value: JsonValue): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function deepClone<T extends JsonValue>(value: T): T {
  if (Array.isArray(value)) {
    return value.map((item) => deepClone(item)) as T;
  }
  if (isObject(value)) {
    const out: JsonObject = {};
    for (const key of Object.keys(value)) {
      out[key] = deepClone(value[key]);
    }
    return out as T;
  }
  return value;
}

/**
 * Canonical serialization used ONLY for cycle detection and equality:
 * object keys are sorted recursively so key order never affects identity.
 * The working tree itself keeps its original key order.
 */
export function canonicalize(value: JsonValue): string {
  if (Array.isArray(value)) {
    return `[${value.map(canonicalize).join(',')}]`;
  }
  if (isObject(value)) {
    const body = Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonicalize(value[key])}`)
      .join(',');
    return `{${body}}`;
  }
  return JSON.stringify(value) ?? 'null';
}

export function getAt(root: JsonValue, path: Path): JsonValue {
  let node: JsonValue = root;
  for (const seg of path) {
    if (Array.isArray(node) && typeof seg === 'number') {
      node = node[seg];
    } else if (isObject(node) && typeof seg === 'string') {
      node = node[seg];
    } else {
      throw new Error(`path ${formatPath(path)} does not exist (failed at segment ${String(seg)})`);
    }
  }
  return node;
}

/** Immutable replace: rebuilds only the spine, preserving array order and object key order. */
export function setAt(root: JsonValue, path: Path, next: JsonValue): JsonValue {
  if (path.length === 0) {
    return next;
  }
  const [head, ...rest] = path;
  if (Array.isArray(root) && typeof head === 'number') {
    const copy = root.slice();
    copy[head] = setAt(root[head], rest, next);
    return copy;
  }
  if (isObject(root) && typeof head === 'string') {
    const copy: JsonObject = {};
    for (const key of Object.keys(root)) {
      copy[key] = key === head ? setAt(root[key], rest, next) : root[key];
    }
    return copy;
  }
  throw new Error(`path ${formatPath(path)} does not exist (failed at segment ${String(head)})`);
}

/** RFC 6901 JSON Pointer, e.g. "" for root, "/a/0/b". */
export function formatPath(path: Path): string {
  if (path.length === 0) {
    return '';
  }
  return path
    .map((seg) => '/' + String(seg).replace(/~/g, '~0').replace(/\//g, '~1'))
    .join('');
}

export function samePath(a: Path, b: Path): boolean {
  return a.length === b.length && a.every((seg, i) => seg === b[i]);
}

/** Deterministic segment order: numbers (numeric) before strings (lexicographic). */
function compareSegment(a: string | number, b: string | number): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  if (typeof a === 'number') return -1;
  if (typeof b === 'number') return 1;
  return a < b ? -1 : a > b ? 1 : 0;
}

export function comparePaths(a: Path, b: Path): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const c = compareSegment(a[i], b[i]);
    if (c !== 0) return c;
  }
  return a.length - b.length;
}

/**
 * Deterministic pre-order walk. Arrays are visited in index order (ordered);
 * object keys are visited in sorted order because object keys are unordered
 * and the engine must not depend on incidental insertion order.
 */
export function* walk(
  value: JsonValue,
  path: (string | number)[] = [],
): Generator<{ path: (string | number)[]; value: JsonValue }> {
  yield { path, value };
  if (Array.isArray(value)) {
    for (let i = 0; i < value.length; i++) {
      yield* walk(value[i], [...path, i]);
    }
  } else if (isObject(value)) {
    for (const key of Object.keys(value).sort()) {
      yield* walk(value[key], [...path, key]);
    }
  }
}
