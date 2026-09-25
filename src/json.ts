/** JSON-compatible tree model + path utilities.
 *  Objects are unordered (key order never affects equality), arrays are ordered. */

export type JsonObject = { [key: string]: JsonValue };
export type JsonValue = null | boolean | number | string | JsonValue[] | JsonObject;
export type PathSeg = string | number;
export type Path = readonly PathSeg[];

export function isObject(v: unknown): v is JsonObject {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function isArray(v: unknown): v is JsonValue[] {
  return Array.isArray(v);
}

/** Delete sentinel: `{ "$delete": true }`. Distinct from `null`:
 *  `null` is a real value that gets merged in; the sentinel removes the field. */
export const DELETE_KEY = "$delete";

export function isDeleteSentinel(v: JsonValue | undefined): boolean {
  return isObject(v) && (v as JsonObject)[DELETE_KEY] === true;
}

/** Pure cross-field reference: a value that is exactly `{ "$ref": "#/some/path" }`.
 *  Pure = the reference is the whole value, never string interpolation. */
export const REF_KEY = "$ref";

export function isRefMarker(v: JsonValue | undefined): boolean {
  return isObject(v) && typeof (v as JsonObject)[REF_KEY] === "string" && Object.keys(v as JsonObject).length === 1;
}

/** Deep clone of a JSON tree. */
export function cloneJson<T extends JsonValue>(v: T): T {
  if (isArray(v)) return v.map((x) => cloneJson(x)) as T;
  if (isObject(v)) {
    const out: JsonObject = {};
    for (const k of Object.keys(v)) out[k] = cloneJson(v[k]);
    return out as T;
  }
  return v;
}

/** Structural equality: object key order ignored, array order significant. */
export function jsonEquals(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (isArray(a) && isArray(b)) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!jsonEquals(a[i], b[i])) return false;
    return true;
  }
  if (isObject(a) && isObject(b)) {
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    if (ka.length !== kb.length) return false;
    for (const k of ka) {
      if (!Object.prototype.hasOwnProperty.call(b, k)) return false;
      if (!jsonEquals(a[k], b[k])) return false;
    }
    return true;
  }
  return false;
}

/** Canonical path encoding (JSON-Pointer-ish). Root is "". */
export function encodePath(path: Path): string {
  if (path.length === 0) return "";
  return (
    "/" +
    path
      .map((s) => String(s).replace(/~/g, "~0").replace(/\//g, "~1"))
      .join("/")
  );
}

export function decodePath(s: string): PathSeg[] {
  if (s === "" || s === "/") return [];
  if (!s.startsWith("/")) throw new Error(`bad encoded path: ${s}`);
  return s
    .slice(1)
    .split("/")
    .map((seg) => {
      const raw = seg.replace(/~1/g, "/").replace(/~0/g, "~");
      return /^-?\d+$/.test(raw) ? Number(raw) : raw;
    });
}

/** Parse a "#/a/b/0" ref pointer into segments. */
export function parsePointer(pointer: string): PathSeg[] {
  if (!pointer.startsWith("#/") && pointer !== "#")
    throw new Error(`ref pointer must start with "#/" or be "#": ${pointer}`);
  const body = pointer === "#" ? "" : pointer.slice(1);
  return decodePath(body);
}

/** Read the value at `path`; `undefined` means the path is absent. */
export function getPath(root: JsonValue, path: Path): JsonValue | undefined {
  let cur: JsonValue | undefined = root;
  for (const seg of path) {
    if (cur === undefined || cur === null) return undefined;
    if (isArray(cur)) {
      const i = typeof seg === "number" ? seg : /^\d+$/.test(seg) ? Number(seg) : NaN;
      if (!Number.isInteger(i) || i < 0 || i >= cur.length) return undefined;
      cur = cur[i];
    } else if (isObject(cur)) {
      const k = String(seg);
      if (!Object.prototype.hasOwnProperty.call(cur, k)) return undefined;
      cur = cur[k];
    } else {
      return undefined;
    }
  }
  return cur;
}

export function hasPath(root: JsonValue, path: Path): boolean {
  return getPath(root, path) !== undefined;
}

/** Immutable set; creates intermediate containers as needed. */
export function setPathImmutable(root: JsonValue, path: Path, value: JsonValue): JsonValue {
  if (path.length === 0) return value;
  const [head, ...rest] = path;
  if (typeof head === "number" || (isArray(root) && /^\d+$/.test(String(head)))) {
    const i = Number(head);
    const arr: JsonValue[] = isArray(root) ? root.slice() : [];
    while (arr.length <= i) arr.push(null);
    arr[i] = setPathImmutable(arr[i] === undefined ? null : arr[i], rest, value);
    return arr;
  }
  const obj: JsonObject = isObject(root) ? { ...root } : {};
  const k = String(head);
  const child = Object.prototype.hasOwnProperty.call(obj, k) ? obj[k] : (typeof rest[0] === "number" ? [] : {});
  obj[k] = setPathImmutable(child as JsonValue, rest, value);
  return obj;
}

/** Immutable delete; keeps (possibly emptied) ancestors, like the merge fold does. */
export function deletePathImmutable(root: JsonValue, path: Path): JsonValue {
  if (path.length === 0) return root;
  const [head, ...rest] = path;
  if (isArray(root)) {
    const i = Number(head);
    if (!Number.isInteger(i) || i < 0 || i >= root.length) return root;
    if (rest.length === 0) {
      const arr = root.slice();
      arr.splice(i, 1);
      return arr;
    }
    const arr = root.slice();
    arr[i] = deletePathImmutable(arr[i], rest);
    return arr;
  }
  if (isObject(root)) {
    const k = String(head);
    if (!Object.prototype.hasOwnProperty.call(root, k)) return root;
    const obj = { ...root };
    if (rest.length === 0) delete obj[k];
    else obj[k] = deletePathImmutable(obj[k], rest);
    return obj;
  }
  return root;
}

/** Minimal set of paths where `a` and `b` differ (a removed key is reported at its own path). */
export function diffPaths(a: JsonValue | undefined, b: JsonValue | undefined, base: PathSeg[] = []): PathSeg[][] {
  if (a === undefined && b === undefined) return [];
  if (a !== undefined && b !== undefined && jsonEquals(a, b)) return [];
  // Sentinels are atomic: never diff into them, or the marker would leak into the tree.
  if ((a !== undefined && isDeleteSentinel(a)) || (b !== undefined && isDeleteSentinel(b))) return [base];
  // Refs are atomic markers too: a changed pointer dirties the whole ref field.
  if ((a !== undefined && isRefMarker(a)) || (b !== undefined && isRefMarker(b))) return [base];
  if (isObject(a) && isObject(b)) {
    const out: PathSeg[][] = [];
    const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
    for (const k of keys) {
      const av = Object.prototype.hasOwnProperty.call(a, k) ? a[k] : undefined;
      const bv = Object.prototype.hasOwnProperty.call(b, k) ? b[k] : undefined;
      out.push(...diffPaths(av, bv, [...base, k]));
    }
    return out;
  }
  if (isArray(a) && isArray(b)) {
    // Arrays are ordered: any difference dirties the whole array field.
    return [base];
  }
  return [base];
}

/** Drop paths that have an ancestor already in the set. */
export function minimalCover(paths: PathSeg[][]): PathSeg[][] {
  const sorted = [...paths].sort((x, y) => x.length - y.length);
  const out: PathSeg[][] = [];
  for (const p of sorted) {
    if (!out.some((q) => isPrefix(q, p))) out.push(p);
  }
  return out;
}

export function isPrefix(prefix: Path, path: Path): boolean {
  if (prefix.length > path.length) return false;
  for (let i = 0; i < prefix.length; i++) if (prefix[i] !== path[i]) return false;
  return true;
}

/** Two paths are "comparable" when one is a prefix of the other. */
export function pathsComparable(a: Path, b: Path): boolean {
  return isPrefix(a, b) || isPrefix(b, a);
}

export function deepFreeze<T>(v: T): T {
  if (v !== null && typeof v === "object") {
    if (v instanceof Map) {
      for (const val of v.values()) deepFreeze(val);
    } else {
      for (const k of Object.keys(v)) deepFreeze((v as Record<string, unknown>)[k]);
    }
    Object.freeze(v);
  }
  return v;
}

/** Human-readable path for error messages and traces (display only). */
export function formatPath(path: Path): string {
  return path.length === 0 ? "/" : encodePath(path);
}
