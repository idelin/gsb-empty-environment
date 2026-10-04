/**
 * Branching edit history over persistent ropes.
 *
 * Every edit commits a new immutable snapshot as a child of the current
 * one, so undo followed by a new edit creates a branch instead of
 * truncating the redo stack. Snapshots share unchanged rope nodes with
 * their ancestors; releaseSnapshot() drops a snapshot's reference and the
 * arena reclaims any nodes no longer reachable from surviving snapshots.
 */

import { Rope, RopeArena } from "./rope.js";
import { assertWellFormedUtf8 } from "./utf8.js";

export interface SnapshotInfo {
  id: number;
  parentId: number | null;
  childIds: number[];
}

class Snapshot {
  id: number;
  parentId: number | null;
  childIds: number[];
  rope: Rope;

  constructor(id: number, parentId: number | null, rope: Rope) {
    this.id = id;
    this.parentId = parentId;
    this.childIds = [];
    this.rope = rope;
  }
}

export class TextEditor {
  readonly arena: RopeArena = new RopeArena();
  private snapshots: Map<number, Snapshot> = new Map();
  private currentId: number = 0;
  private nextId: number = 1;

  constructor(initialText: string = "") {
    assertWellFormedUtf8(initialText);
    this.snapshots.set(0, new Snapshot(0, null, Rope.from(this.arena, initialText)));
  }

  private current(): Snapshot {
    const snap = this.snapshots.get(this.currentId);
    if (snap === undefined) throw new Error("corrupt history: current snapshot missing");
    return snap;
  }

  get currentSnapshotId(): number {
    return this.currentId;
  }

  get text(): string {
    return this.current().rope.toString();
  }

  /** Length in Unicode scalar values. */
  get length(): number {
    return this.current().rope.length;
  }

  /** Length in UTF-8 bytes. */
  get byteLength(): number {
    return this.current().rope.byteLength;
  }

  get nodeCount(): number {
    return this.current().rope.nodeCount;
  }

  snapshotCount(): number {
    return this.snapshots.size;
  }

  has(id: number): boolean {
    return this.snapshots.has(id);
  }

  info(id: number): SnapshotInfo | null {
    const snap = this.snapshots.get(id);
    if (snap === undefined) return null;
    return { id: snap.id, parentId: snap.parentId, childIds: snap.childIds.slice() };
  }

  childrenOf(id: number): number[] {
    const snap = this.snapshots.get(id);
    if (snap === undefined) return [];
    return snap.childIds.slice();
  }

  /** Children of the current snapshot, i.e. the branches leaving it. */
  branches(): number[] {
    return this.childrenOf(this.currentId);
  }

  /**
   * Inserts text at a scalar offset and commits a new snapshot.
   * Invalid UTF-8 (lone surrogates) rejects the whole edit: nothing is
   * allocated and the history is left untouched.
   */
  insert(offset: number, text: string): number {
    const rope = this.current().rope.insert(offset, text);
    return this.commit(rope);
  }

  /** Deletes the scalar range [start, end) and commits a new snapshot. */
  delete(start: number, end: number): number {
    const rope = this.current().rope.delete(start, end);
    return this.commit(rope);
  }

  private commit(rope: Rope): number {
    const id = this.nextId;
    this.nextId++;
    const parent = this.current();
    parent.childIds.push(id);
    this.snapshots.set(id, new Snapshot(id, parent.id, rope));
    this.currentId = id;
    return id;
  }

  undo(): boolean {
    const parentId = this.current().parentId;
    if (parentId === null || !this.snapshots.has(parentId)) return false;
    this.currentId = parentId;
    return true;
  }

  /** Moves to the most recently created child of the current snapshot. */
  redo(): boolean {
    const children = this.current().childIds;
    if (children.length === 0) return false;
    return this.checkout(children[children.length - 1]);
  }

  checkout(id: number): boolean {
    if (!this.snapshots.has(id)) return false;
    this.currentId = id;
    return true;
  }

  /**
   * Releases a snapshot that is not current. Its children are re-attached
   * to its parent so the history tree stays connected, its rope reference
   * is dropped, and nodes no longer shared with any surviving snapshot are
   * reclaimed by the arena.
   */
  releaseSnapshot(id: number): boolean {
    if (id === this.currentId) return false;
    const snap = this.snapshots.get(id);
    if (snap === undefined) return false;
    if (snap.parentId !== null) {
      const parent = this.snapshots.get(snap.parentId);
      if (parent !== undefined) {
        const idx = parent.childIds.indexOf(id);
        if (idx >= 0) parent.childIds.splice(idx, 1);
        for (const childId of snap.childIds) {
          parent.childIds.push(childId);
        }
      }
    }
    for (const childId of snap.childIds) {
      const child = this.snapshots.get(childId);
      if (child !== undefined) child.parentId = snap.parentId;
    }
    snap.rope.dispose();
    this.snapshots.delete(id);
    return true;
  }

  /** Debug invariant check across every surviving snapshot. */
  validate(): void {
    for (const snap of this.snapshots.values()) {
      snap.rope.validate();
    }
  }
}
