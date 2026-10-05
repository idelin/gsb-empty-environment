import type { JsonValue } from "./ast.ts";
import { DocumentEditError } from "./errors.ts";
import type { PathSeg } from "./path.ts";

export type Edit =
  | { type: "set"; path: PathSeg[]; value: JsonValue }
  | { type: "delete"; path: PathSeg[] };

function isContainer(value: JsonValue): value is JsonValue[] | { [key: string]: JsonValue } {
  return value !== null && typeof value === "object";
}

function childOf(node: JsonValue, seg: PathSeg, path: PathSeg[]): JsonValue {
  if (Array.isArray(node)) {
    if (typeof seg !== "number" || !Number.isInteger(seg)) {
      throw new DocumentEditError(`expected integer array index, got ${JSON.stringify(seg)}`, path);
    }
    if (seg < 0 || seg >= node.length) {
      throw new DocumentEditError(`array index ${seg} out of bounds (length ${node.length})`, path);
    }
    return node[seg];
  }
  if (isContainer(node)) {
    if (typeof seg !== "string") {
      throw new DocumentEditError(`expected object key, got array index ${seg}`, path);
    }
    if (!Object.hasOwn(node, seg)) {
      throw new DocumentEditError(`missing key ${JSON.stringify(seg)}`, path);
    }
    return node[seg];
  }
  throw new DocumentEditError(`cannot descend into ${node === null ? "null" : typeof node}`, path);
}

function withChild(
  node: JsonValue,
  seg: PathSeg,
  child: JsonValue,
  path: PathSeg[],
): JsonValue {
  if (Array.isArray(node)) {
    if (typeof seg !== "number" || !Number.isInteger(seg) || seg < 0 || seg > node.length) {
      throw new DocumentEditError(
        `array index ${JSON.stringify(seg)} out of bounds for set (length ${node.length})`,
        path,
      );
    }
    const copy = node.slice();
    if (seg === node.length) copy.push(child);
    else copy[seg] = child;
    return copy;
  }
  if (isContainer(node)) {
    if (typeof seg !== "string") {
      throw new DocumentEditError(`expected object key, got array index ${seg}`, path);
    }
    return { ...node, [seg]: child };
  }
  throw new DocumentEditError(`cannot set child of ${node === null ? "null" : typeof node}`, path);
}

function withoutChild(node: JsonValue, seg: PathSeg, path: PathSeg[]): JsonValue {
  if (Array.isArray(node)) {
    if (typeof seg !== "number" || !Number.isInteger(seg) || seg < 0 || seg >= node.length) {
      throw new DocumentEditError(
        `array index ${JSON.stringify(seg)} out of bounds for delete (length ${Array.isArray(node) ? node.length : 0})`,
        path,
      );
    }
    const copy = node.slice();
    copy.splice(seg, 1);
    return copy;
  }
  if (isContainer(node)) {
    if (typeof seg !== "string" || !Object.hasOwn(node, seg)) {
      throw new DocumentEditError(`missing key ${JSON.stringify(seg)}`, path);
    }
    const copy = { ...node };
    delete copy[seg];
    return copy;
  }
  throw new DocumentEditError(`cannot delete child of ${node === null ? "null" : typeof node}`, path);
}

/**
 * Applies one edit to a JSON tree, returning a new tree. Unmodified
 * subtrees are shared with the previous version (persistent data
 * structure), so old versions stay intact and cheap to retain.
 */
export function applyEdit(root: JsonValue, edit: Edit): JsonValue {
  const { path } = edit;
  if (path.length === 0) {
    if (edit.type === "set") return edit.value;
    throw new DocumentEditError("cannot delete the document root", path);
  }
  // Walk down, remembering the chain; then rebuild bottom-up with copies.
  const chain: Array<{ node: JsonValue; seg: PathSeg }> = [];
  let current = root;
  for (let i = 0; i < path.length - 1; i++) {
    const seg = path[i];
    chain.push({ node: current, seg });
    current = childOf(current, seg, path.slice(0, i + 1));
  }
  const last = path[path.length - 1];
  let rebuilt: JsonValue;
  if (edit.type === "set") {
    rebuilt = withChild(current, last, edit.value, path);
  } else {
    rebuilt = withoutChild(current, last, path);
  }
  for (let i = chain.length - 1; i >= 0; i--) {
    rebuilt = withChild(chain[i].node, chain[i].seg, rebuilt, path.slice(0, i + 1));
  }
  return rebuilt;
}

/**
 * A versioned JSON document. Every `applyEdits` call produces a new
 * version; old versions remain readable (structural sharing), which is
 * what makes stable cursors possible: a cursor pins (version, path).
 */
export class VersionedDocument {
  private readonly versions = new Map<number, JsonValue>();
  private currentVersion = 0;

  constructor(initial: JsonValue) {
    this.versions.set(0, initial);
  }

  get version(): number {
    return this.currentVersion;
  }

  get(version?: number): JsonValue {
    const v = version ?? this.currentVersion;
    const snap = this.versions.get(v);
    if (snap === undefined) {
      throw new DocumentEditError(`version ${v} has been pruned or does not exist`, []);
    }
    return snap;
  }

  /** Applies edits sequentially; returns the new version number. */
  applyEdits(edits: Edit[]): number {
    let root = this.get();
    for (const edit of edits) {
      root = applyEdit(root, edit);
    }
    this.currentVersion += 1;
    this.versions.set(this.currentVersion, root);
    return this.currentVersion;
  }

  /** Drops snapshots older than `keepFrom` (they can no longer be resolved). */
  prune(keepFrom: number): void {
    for (const v of [...this.versions.keys()]) {
      if (v < keepFrom) this.versions.delete(v);
    }
  }
}
