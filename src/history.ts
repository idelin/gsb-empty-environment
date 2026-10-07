/**
 * Transaction-based undo/redo history for a drawing editor.
 *
 * Design notes:
 * - History is a tree of full document snapshots. Undo moves the cursor to the
 *   parent node; committing after an undo forks a new branch instead of
 *   discarding the old one, so every branch stays reachable via redo(branch).
 * - Consecutive commits carrying the same non-null transaction label merge
 *   into a single undo step, but only when both commits are marked mergeable
 *   and no other history operation (commit with another label, undo, redo)
 *   happened in between. Marking a save point does NOT break merging, so a
 *   merge may span a save point.
 * - Dirty tracking is content-based: markSaved() remembers the saved document
 *   content and isDirty() compares the current snapshot against it. Because
 *   the comparison is by content (not by node identity), a merged transaction
 *   that spans a save point is still reported clean whenever the document
 *   returns to the saved content, and eviction of the node that was current
 *   at save time does not corrupt dirty state.
 * - Capacity eviction keeps at most `capacity` committed entries. It first
 *   promotes the root along a single-child trunk (dropping the oldest undo
 *   step), otherwise trims the oldest leaf that is neither the root nor the
 *   current node (dropping abandoned redo branches). The current path is
 *   never broken.
 */

export interface CommitOptions {
  /** Transaction label, e.g. "move:obj-1". Required for merging. */
  label?: string | null;
  /** Whether this transaction may merge with an adjacent identical one. */
  mergeable?: boolean;
}

export interface HistoryOptions<T> {
  /** Maximum number of retained committed entries. Defaults to unbounded. */
  capacity?: number;
  /** Snapshot equality used for dirty checks. Defaults to structural equality. */
  equals?: (a: T, b: T) => boolean;
}

interface HistoryNode<T> {
  id: number;
  parent: HistoryNode<T> | null;
  children: HistoryNode<T>[];
  snapshot: T;
  label: string | null;
  mergeable: boolean;
  seq: number;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (
    typeof a !== "object" || a === null ||
    typeof b !== "object" || b === null
  ) {
    return false;
  }
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keysA = Object.keys(a as Record<string, unknown>);
  const keysB = Object.keys(b as Record<string, unknown>);
  if (keysA.length !== keysB.length) return false;
  for (const key of keysA) {
    if (!Object.prototype.hasOwnProperty.call(b, key)) return false;
    if (!deepEqual(
      (a as Record<string, unknown>)[key],
      (b as Record<string, unknown>)[key],
    )) {
      return false;
    }
  }
  return true;
}

export class History<T> {
  private root: HistoryNode<T>;
  private current: HistoryNode<T>;
  private savedSnapshot: T;
  private mergeTarget: HistoryNode<T> | null = null;
  private nextId = 1;
  private nextSeq = 1;
  private count = 0;
  private readonly capacity: number;
  private readonly equals: (a: T, b: T) => boolean;

  constructor(initial: T, options: HistoryOptions<T> = {}) {
    this.capacity = options.capacity ?? Number.POSITIVE_INFINITY;
    this.equals = options.equals ?? (deepEqual as (a: T, b: T) => boolean);
    this.root = {
      id: 0,
      parent: null,
      children: [],
      snapshot: initial,
      label: null,
      mergeable: false,
      seq: 0,
    };
    this.current = this.root;
    this.savedSnapshot = initial;
  }

  /** Snapshot at the current position. */
  get value(): T {
    return this.current.snapshot;
  }

  /** Number of retained committed entries (the base snapshot is not counted). */
  get size(): number {
    return this.count;
  }

  /** Number of undo steps available from the current position. */
  get depth(): number {
    let depth = 0;
    let node = this.current;
    while (node.parent !== null) {
      depth += 1;
      node = node.parent;
    }
    return depth;
  }

  canUndo(): boolean {
    return this.current !== this.root;
  }

  canRedo(): boolean {
    return this.current.children.length > 0;
  }

  /** Number of redo branches at the current position. */
  branches(): number {
    return this.current.children.length;
  }

  commit(snapshot: T, options: CommitOptions = {}): void {
    const label = options.label ?? null;
    const mergeable = options.mergeable ?? false;
    const target = this.current;
    const canMerge =
      mergeable &&
      label !== null &&
      this.mergeTarget === target &&
      target !== this.root &&
      target.mergeable &&
      target.label === label;
    if (canMerge) {
      // Fold into the existing entry: undo still returns to the state before
      // the whole merged sequence. mergeTarget intentionally stays in place,
      // so further identical transactions keep merging.
      target.snapshot = snapshot;
      return;
    }
    const node: HistoryNode<T> = {
      id: this.nextId++,
      parent: target,
      children: [],
      snapshot,
      label,
      mergeable,
      seq: this.nextSeq++,
    };
    target.children.push(node);
    this.current = node;
    this.mergeTarget = node;
    this.count += 1;
    this.evict();
  }

  undo(): T | undefined {
    if (this.current === this.root) return undefined;
    this.current = this.current.parent as HistoryNode<T>;
    this.mergeTarget = null;
    return this.current.snapshot;
  }

  /**
   * Move one step towards a leaf. With several branches (after undo + new
   * commit), `branch` selects the child index; the default is the most
   * recently created branch.
   */
  redo(branch?: number): T | undefined {
    const children = this.current.children;
    if (children.length === 0) return undefined;
    const index = branch ?? children.length - 1;
    const next = children[index];
    if (next === undefined) return undefined;
    this.current = next;
    this.mergeTarget = null;
    return next.snapshot;
  }

  /**
   * Record the current content as the saved content. Does not interrupt an
   * ongoing mergeable transaction: a later merge may span this save point,
   * and dirty state stays correct because it is content-based.
   */
  markSaved(): void {
    this.savedSnapshot = this.current.snapshot;
  }

  isDirty(): boolean {
    return !this.equals(this.current.snapshot, this.savedSnapshot);
  }

  private evict(): void {
    while (this.count > this.capacity) {
      if (this.root !== this.current && this.root.children.length === 1) {
        // Single trunk: promote the oldest entry to be the new base snapshot,
        // dropping one undo step from the tail of history.
        const promoted = this.root.children[0];
        promoted.parent = null;
        this.root = promoted;
        this.count -= 1;
        continue;
      }
      // Branched history (or cursor at the base): trim the oldest leaf that
      // is neither the base nor the current node.
      let victim: HistoryNode<T> | null = null;
      const stack: HistoryNode<T>[] = [this.root];
      while (stack.length > 0) {
        const node = stack.pop() as HistoryNode<T>;
        if (node !== this.root && node !== this.current && node.children.length === 0) {
          if (victim === null || node.seq < victim.seq) victim = node;
        }
        for (const child of node.children) stack.push(child);
      }
      if (victim === null) break;
      const siblings = (victim.parent as HistoryNode<T>).children;
      siblings.splice(siblings.indexOf(victim), 1);
      this.count -= 1;
    }
  }
}
