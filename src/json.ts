// JSON-compatible tree utilities. Object key order is never significant;
// array order is always significant. All updates are immutable.

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type PathSegment = string | number;
export type Path = readonly PathSegment[];

/** JSON-Pointer style rendering, e.g. ["a", 0, "b"] -> "/a/0/b". */
export function pathToString(path: Path): string {
  if (path.length === 0) return "";
  return (
    "/" +
    path
      .map((seg) => String(seg).replace(/~/g, "~0").replace(/\//g, "~1"))
      .join("/")
  );
}

export function getAt(
  root: JsonValue,
  path: Path,
): { found: boolean; value?: JsonValue } {
  let node: JsonValue = root;
  for (const seg of path) {
    if (Array.isArray(node)) {
      if (typeof seg !== "number" || !Number.isInteger(seg) || seg < 0 || seg >= node.length) {
        return { found: false };
      }
      node = node[seg];
    } else if (node !== null && typeof node === "object") {
      if (typeof seg !== "string" || !(seg in node)) return { found: false };
      node = node[seg];
    } else {
      return { found: false };
    }
  }
  return { found: true, value: node };
}

/** Immutable set. Missing intermediate containers are created
 *  (object for string keys, array for numeric indices). */
export function setAt(root: JsonValue, path: Path, value: JsonValue): JsonValue {
  if (path.length === 0) return value;
  const [head, ...rest] = path;
  if (Array.isArray(root)) {
    if (typeof head !== "number") {
      throw new Error(`setAt: numeric segment required for array, got ${String(head)}`);
    }
    const copy = root.slice();
    copy[head] = setAt(head < root.length ? root[head] : null, rest, value);
    return copy;
  }
  if (root !== null && typeof root === "object") {
    if (typeof head !== "string") {
      throw new Error(`setAt: string segment required for object, got ${String(head)}`);
    }
    const child: JsonValue = head in root ? root[head] : null;
    return { ...root, [head]: setAt(child, rest, value) };
  }
  // root is a scalar (or null placeholder): create a container.
  if (typeof head === "number") {
    const arr: JsonValue[] = [];
    arr[head] = setAt(null, rest, value);
    return arr;
  }
  return { [head]: setAt(null, rest, value) };
}

export function removeAt(root: JsonValue, path: Path): JsonValue {
  if (path.length === 0) {
    throw new Error("removeAt: cannot remove the root");
  }
  const [head, ...rest] = path;
  if (Array.isArray(root)) {
    if (typeof head !== "number") return root;
    if (rest.length === 0) {
      const copy = root.slice();
      copy.splice(head, 1);
      return copy;
    }
    const copy = root.slice();
    copy[head] = removeAt(root[head], rest);
    return copy;
  }
  if (root !== null && typeof root === "object") {
    if (typeof head !== "string" || !(head in root)) return root;
    if (rest.length === 0) {
      const copy: { [key: string]: JsonValue } = { ...root };
      delete copy[head];
      return copy;
    }
    return { ...root, [head]: removeAt(root[head], rest) };
  }
  return root;
}

/** Structural equality; object key order is ignored. */
export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (a === null || b === null) return false;
  if (typeof a !== typeof b) return false;
  if (typeof a !== "object") return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b)) return false;
    if (a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i]));
  }
  const aObj = a as { [key: string]: JsonValue };
  const bObj = b as { [key: string]: JsonValue };
  const aKeys = Object.keys(aObj);
  const bKeys = Object.keys(bObj);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((k) => k in bObj && deepEqual(aObj[k], bObj[k]));
}

export interface WalkEntry {
  path: PathSegment[];
  value: JsonValue;
}

/** Depth-first walk over every node. Object keys are visited in sorted
 *  order so traversal is deterministic regardless of key insertion order. */
export function* walk(node: JsonValue, base: PathSegment[] = []): Generator<WalkEntry> {
  yield { path: base, value: node };
  if (Array.isArray(node)) {
    for (let i = 0; i < node.length; i++) {
      yield* walk(node[i], [...base, i]);
    }
  } else if (node !== null && typeof node === "object") {
    for (const key of Object.keys(node).sort()) {
      yield* walk(node[key], [...base, key]);
    }
  }
}
