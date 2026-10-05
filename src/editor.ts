/**
 * TextEditor: persistent UTF-8 text buffer with undo and branching history.
 *
 * - All edit offsets/lengths are Unicode scalar values; edits never split
 *   a multi-byte encoding.
 * - Every committed edit creates a node in a history tree. undo()/redo()
 *   walk the tree; checkout() jumps to any snapshot, so diverging edits
 *   after an undo naturally form branches.
 * - Snapshots are persistent: old snapshots share every unchanged rope
 *   fragment with the current state. releaseSnapshot() drops a leaf
 *   snapshot and reclaims exactly the nodes no longer referenced.
 */

import {
  Node,
  NodePool,
  build,
  collect,
  join,
  reachableCount,
  splitOpt,
} from "./rope";
import {
  InvalidEncodingError,
  decodeUtf8,
  encodeUtf8,
  validateString,
} from "./utf8";

export { InvalidEncodingError };

export interface SnapshotInfo {
  id: number;
  label: string | undefined;
  parent: number | null;
  children: number[];
}

interface HistoryEntry {
  id: number;
  parent: number | null;
  children: number[];
  root: Node | null;
  label: string | undefined;
}

export class TextEditor {
  private readonly pool = new NodePool();
  private readonly entries = new Map<number, HistoryEntry>();
  private currentId = 0;
  private nextId = 1;

  constructor(initial?: string | Uint8Array) {
    let root: Node | null = null;
    if (initial !== undefined) {
      // Invalid encoding rejects the whole edit before anything is built.
      const text = typeof initial === "string" ? checkedString(initial) : decodeUtf8(initial);
      root = build(this.pool, text); // fresh reference transferred to the entry
    }
    this.entries.set(0, {
      id: 0,
      parent: null,
      children: [],
      root,
      label: "initial",
    });
  }

  /** Number of Unicode scalar values in the current document. */
  get scalarLength(): number {
    const r = this.root();
    return r ? r.scalars : 0;
  }

  /** Number of UTF-8 bytes in the current document. */
  get byteLength(): number {
    const r = this.root();
    return r ? r.bytes : 0;
  }

  /** Id of the history entry the editor is currently positioned at. */
  get currentSnapshotId(): number {
    return this.currentId;
  }

  /** Number of retained rope nodes (for diagnostics/tests). */
  get liveNodes(): number {
    return this.pool.live;
  }

  /** Number of live history entries. */
  get snapshotCount(): number {
    return this.entries.size;
  }

  /**
   * Inserts text at the given scalar offset.
   * Throws InvalidEncodingError (rejecting the whole edit, leaving the
   * document untouched) if the input is not well-formed UTF-8 / contains
   * lone surrogates. Throws RangeError for an out-of-range offset.
   */
  insert(offset: number, text: string | Uint8Array): void {
    const s = typeof text === "string" ? checkedString(text) : decodeUtf8(text);
    const len = this.scalarLength;
    if (!Number.isInteger(offset) || offset < 0 || offset > len) {
      throw new RangeError("insert offset " + offset + " out of range [0, " + len + "]");
    }
    if (s.length === 0) return; // no-op: no history entry
    const piece = build(this.pool, s);
    const [a, bc] = splitOpt(this.pool, this.root(), offset);
    const ab = join(this.pool, a, piece);
    const newRoot = join(this.pool, ab, bc);
    this.pool.release(piece);
    this.pool.release(a);
    this.pool.release(bc);
    this.pool.release(ab);
    this.commit(newRoot); // ownership of newRoot transferred
  }

  /** Deletes `length` scalars starting at scalar offset `offset`. */
  delete(offset: number, length: number): void {
    const total = this.scalarLength;
    if (
      !Number.isInteger(offset) ||
      !Number.isInteger(length) ||
      offset < 0 ||
      length < 0 ||
      offset + length > total
    ) {
      throw new RangeError(
        "delete range [" + offset + ", " + (offset + length) + ") out of [0, " + total + "]"
      );
    }
    if (length === 0) return; // no-op
    const [a, bc] = splitOpt(this.pool, this.root(), offset);
    const [b, c] = splitOpt(this.pool, bc, length);
    const newRoot = join(this.pool, a, c);
    this.pool.release(a);
    this.pool.release(bc);
    this.pool.release(b);
    this.pool.release(c);
    this.commit(newRoot);
  }

  /** Full text, or the substring covering scalar offsets [start, end). */
  getText(start?: number, end?: number): string {
    const total = this.scalarLength;
    const s = start === undefined ? 0 : start;
    const e = end === undefined ? total : end;
    if (!Number.isInteger(s) || !Number.isInteger(e) || s < 0 || e < s || e > total) {
      throw new RangeError("slice [" + s + ", " + e + ") out of [0, " + total + "]");
    }
    const [a, bc] = splitOpt(this.pool, this.root(), s);
    const [mid, c] = splitOpt(this.pool, bc, e - s);
    const parts: string[] = [];
    collect(mid, parts);
    this.pool.release(a);
    this.pool.release(bc);
    this.pool.release(mid);
    this.pool.release(c);
    return parts.join("");
  }

  /** Current document encoded as UTF-8 bytes. */
  getBytes(): Uint8Array {
    return encodeUtf8(this.getText());
  }

  /** Labels the current state and returns its snapshot id. */
  snapshot(label?: string): number {
    const entry = this.mustGet(this.currentId);
    if (label !== undefined) entry.label = label;
    return entry.id;
  }

  /** Moves to the parent state. Returns false if already at the root. */
  undo(): boolean {
    const entry = this.mustGet(this.currentId);
    if (entry.parent === null) return false;
    this.currentId = entry.parent;
    return true;
  }

  /** Moves to the most recently created child state. */
  redo(): boolean {
    const entry = this.mustGet(this.currentId);
    if (entry.children.length === 0) return false;
    this.currentId = entry.children[entry.children.length - 1];
    return true;
  }

  /** Jumps to any live snapshot, enabling branch exploration. */
  checkout(snapshotId: number): void {
    if (!this.entries.has(snapshotId)) {
      throw new Error("unknown snapshot id " + snapshotId);
    }
    this.currentId = snapshotId;
  }

  /**
   * Releases a leaf snapshot (no children, not the current position),
   * reclaiming the rope nodes no longer referenced by any live snapshot.
   */
  releaseSnapshot(snapshotId: number): void {
    const entry = this.entries.get(snapshotId);
    if (!entry) throw new Error("unknown snapshot id " + snapshotId);
    if (snapshotId === this.currentId) {
      throw new Error("cannot release the current snapshot");
    }
    if (entry.children.length > 0) {
      throw new Error("cannot release a snapshot with descendants");
    }
    if (entry.parent !== null) {
      const siblings = this.mustGet(entry.parent).children;
      siblings.splice(siblings.indexOf(snapshotId), 1);
    }
    this.pool.release(entry.root);
    this.entries.delete(snapshotId);
  }

  /** Metadata about a live history entry. */
  snapshotInfo(snapshotId: number): SnapshotInfo {
    const e = this.entries.get(snapshotId);
    if (!e) throw new Error("unknown snapshot id " + snapshotId);
    return { id: e.id, label: e.label, parent: e.parent, children: e.children.slice() };
  }

  /** Test hook: rope root of the current state (do not mutate). */
  debugRoot(): Node | null {
    return this.root();
  }

  /** Test hook: rope root of a specific snapshot (do not mutate). */
  debugRootOf(snapshotId: number): Node | null {
    const e = this.entries.get(snapshotId);
    if (!e) throw new Error("unknown snapshot id " + snapshotId);
    return e.root;
  }

  /**
   * Test hook: number of distinct nodes reachable from all live history
   * roots. Must always equal liveNodes (no leaks, no premature frees).
   */
  debugReachableNodes(): number {
    const roots: Array<Node | null> = [];
    this.entries.forEach((e) => roots.push(e.root));
    return reachableCount(roots);
  }

  private root(): Node | null {
    return this.mustGet(this.currentId).root;
  }

  private mustGet(id: number): HistoryEntry {
    const e = this.entries.get(id);
    if (!e) throw new Error("corrupted history: missing entry " + id);
    return e;
  }

  /** Takes ownership of the fresh `root` reference. */
  private commit(root: Node | null): void {
    const parent = this.mustGet(this.currentId);
    const id = this.nextId++;
    this.entries.set(id, {
      id,
      parent: parent.id,
      children: [],
      root,
      label: undefined,
    });
    parent.children.push(id);
    this.currentId = id;
  }
}

function checkedString(s: string): string {
  validateString(s);
  return s;
}
