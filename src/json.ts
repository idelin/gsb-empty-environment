/**
 * JSON-compatible tree model plus path/pointer utilities.
 *
 * A `Path` is an ordered list of object keys (array indices are their decimal
 * string form). Object key order is meaningless; array order is significant.
 */
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | JsonObject;
export interface JsonObject {
  [key: string]: JsonValue;
}
export type Path = readonly string[];

/** Sentinel for "no value present at this path" (distinct from JSON null). */
export const MISSING: unique symbol = Symbol("missing");
export type Missing = typeof MISSING;

export function isPlainObject(v: unknown): v is JsonObject {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function deepClone<T>(v: T): T {
  return v === undefined ? v : structuredClone(v);
}

/** Structural equality: object key order ignored, array order respected. */
export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b)) return false;
    if (a.length !== b.length) return false;
    return a.every((v, i) => deepEqual(v, b[i]));
  }
  if (typeof a === "object") {
    const ka = Object.keys(a as JsonObject);
    const kb = Object.keys(b as JsonObject);
    if (ka.length !== kb.length) return false;
    return ka.every(
      (k) =>
        Object.hasOwn(b as JsonObject, k) &&
        deepEqual((a as JsonObject)[k], (b as JsonObject)[k]),
    );
  }
  return false;
}

export function deepFreeze<T>(v: T): T {
  if (typeof v === "object" && v !== null) {
    for (const k of Object.keys(v as JsonObject)) {
      deepFreeze((v as JsonObject)[k]);
    }
    Object.freeze(v);
  }
  return v;
}

/** JSON Pointer (RFC 6901). The root path is the empty string. */
export function pathToPointer(path: Path): string {
  return path
    .map((s) => s.replace(/~/g, "~0").replace(/\//g, "~1"))
    .map((s) => "/" + s)
    .join("");
}

export function pointerToPath(pointer: string): string[] {
  if (pointer === "") return [];
  if (!pointer.startsWith("/")) {
    throw new Error(`invalid JSON pointer: ${JSON.stringify(pointer)}`);
  }
  return pointer
    .slice(1)
    .split("/")
    .map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
}

export function getIn(root: JsonValue, path: Path): JsonValue | Missing {
  let node: JsonValue = root;
  for (const seg of path) {
    if (Array.isArray(node)) {
      const i = Number(seg);
      if (!Number.isInteger(i) || i < 0 || i >= node.length) return MISSING;
      node = node[i];
    } else if (isPlainObject(node)) {
      if (!Object.hasOwn(node, seg)) return MISSING;
      node = node[seg];
    } else {
      return MISSING;
    }
  }
  return node;
}

/**
 * Returns a new tree with `value` set at `path` (MISSING removes the key).
 * Untouched subtrees are shared with the input, so unchanged branches keep
 * reference identity (used to prove incremental recompute is minimal).
 */
export function setInImmutable(
  root: JsonValue,
  path: Path,
  value: JsonValue | Missing,
): JsonValue {
  if (path.length === 0) return value === MISSING ? {} : value;
  const head = path[0];
  const rest = path.slice(1);
  if (Array.isArray(root)) {
    const i = Number(head);
    if (!Number.isInteger(i) || i < 0 || i >= root.length) {
      throw new Error(`array index out of range at ${pathToPointer(path)}`);
    }
    const copy = root.slice();
    if (rest.length === 0 && value === MISSING) {
      copy.splice(i, 1);
      return copy;
    }
    const cur = copy[i];
    copy[i] = setInImmutable(
      isPlainObject(cur) || Array.isArray(cur) ? cur : {},
      rest,
      value,
    );
    return copy;
  }
  if (isPlainObject(root)) {
    const copy: JsonObject = { ...root };
    if (rest.length === 0) {
      if (value === MISSING) delete copy[head];
      else copy[head] = value;
      return copy;
    }
    const cur = Object.hasOwn(copy, head) ? copy[head] : {};
    copy[head] = setInImmutable(
      isPlainObject(cur) || Array.isArray(cur) ? cur : {},
      rest,
      value,
    );
    return copy;
  }
  throw new Error(`path traverses a non-container at ${pathToPointer(path)}`);
}

export function isPrefixPath(prefix: Path, path: Path): boolean {
  return prefix.length <= path.length && prefix.every((s, i) => s === path[i]);
}

/** True when either path is a prefix of the other (same subtree). */
export function pathsOverlap(a: Path, b: Path): boolean {
  return isPrefixPath(a, b) || isPrefixPath(b, a);
}

/** Pointer-string prefix check with segment-boundary safety. */
export function pointerUnder(pointer: string, ancestor: string): boolean {
  if (ancestor === "") return true;
  return pointer === ancestor || pointer.startsWith(ancestor + "/");
}
