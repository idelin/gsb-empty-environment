/**
 * Convergent list model (RGA-style CRDT).
 *
 * Semantics:
 * - Every insert carries a globally unique id and the id of its predecessor
 *   (ROOT_ID anchors at the head of the list).
 * - Elements sharing a predecessor are ordered by id, lexicographic ascending.
 * - A delete only tombstones its target: the node stays in the tree so that
 *   later inserts can still anchor on it.
 * - A delete may arrive before the insert it targets; the tombstone is
 *   recorded and replayed once the insert materializes.
 * - An insert whose predecessor is unknown is buffered (never dropped) and
 *   applied once the predecessor materializes, cascading to its own waiters.
 * - Duplicate and out-of-order delivery converge: the final state is a pure
 *   function of the set of applied messages. Conflicting inserts that reuse
 *   an id are invalid input; the first one seen wins.
 */

export const ROOT_ID = '';

export interface InsertMsg<T> {
  kind: 'insert';
  id: string;
  pred: string;
  value: T;
}

export interface DeleteMsg {
  kind: 'delete';
  id: string;
}

export type Message<T> = InsertMsg<T> | DeleteMsg;

export interface PendingInsert<T> {
  id: string;
  pred: string;
  value: T;
}

export interface SnapshotEntry<T> {
  id: string;
  value: T;
  deleted: boolean;
}

export interface Diagnostics<T> {
  /** Inserts buffered because their predecessor is unknown, sorted by id. */
  pending: Array<PendingInsert<T>>;
  /** Predecessor ids referenced by pending inserts but not materialized. */
  missingPredecessors: string[];
  /** Deletes whose target insert has not been applied yet. */
  deletesAwaitingInsert: string[];
}

interface Node<T> {
  id: string;
  pred: string;
  value: T;
  deleted: boolean;
  children: string[];
}

export function compareIds(a: string, b: string): number {
  if (a < b) return -1;
  if (a > b) return 1;
  return 0;
}

export class ListModel<T> {
  private nodes: Map<string, Node<T>> = new Map();
  private rootChildren: string[] = [];
  private pending: Map<string, Array<InsertMsg<T>>> = new Map();
  private deletedIds: Set<string> = new Set();

  apply(msg: Message<T>): void {
    if (msg.kind === 'insert') {
      this.applyInsert(msg);
    } else {
      this.applyDelete(msg.id);
    }
  }

  applyAll(msgs: Array<Message<T>>): void {
    for (const msg of msgs) {
      this.apply(msg);
    }
  }

  /** Visible (non-tombstoned) values in list order. */
  values(): T[] {
    const out: T[] = [];
    for (const entry of this.snapshot()) {
      if (!entry.deleted) out.push(entry.value);
    }
    return out;
  }

  /** Full traversal order including tombstones, pre-order from the root. */
  snapshot(): Array<SnapshotEntry<T>> {
    const out: Array<SnapshotEntry<T>> = [];
    for (const id of this.rootChildren) {
      this.collect(id, out);
    }
    return out;
  }

  diagnostics(): Diagnostics<T> {
    const pending: Array<PendingInsert<T>> = [];
    for (const bucket of this.pending.values()) {
      for (const op of bucket) {
        pending.push({ id: op.id, pred: op.pred, value: op.value });
      }
    }
    pending.sort((a, b) => compareIds(a.id, b.id));
    const missingPredecessors = Array.from(this.pending.keys());
    missingPredecessors.sort(compareIds);
    const deletesAwaitingInsert = Array.from(this.deletedIds);
    deletesAwaitingInsert.sort(compareIds);
    return {
      pending: pending,
      missingPredecessors: missingPredecessors,
      deletesAwaitingInsert: deletesAwaitingInsert,
    };
  }

  private collect(id: string, out: Array<SnapshotEntry<T>>): void {
    const node = this.nodes.get(id);
    if (node === undefined) return;
    out.push({ id: node.id, value: node.value, deleted: node.deleted });
    for (const childId of node.children) {
      this.collect(childId, out);
    }
  }

  private applyInsert(msg: InsertMsg<T>): void {
    if (this.nodes.has(msg.id)) return;
    if (msg.pred === ROOT_ID) {
      this.materialize(msg, this.rootChildren);
      return;
    }
    const parent = this.nodes.get(msg.pred);
    if (parent === undefined) {
      this.buffer(msg);
      return;
    }
    this.materialize(msg, parent.children);
  }

  private materialize(msg: InsertMsg<T>, siblings: string[]): void {
    const node: Node<T> = {
      id: msg.id,
      pred: msg.pred,
      value: msg.value,
      deleted: this.deletedIds.has(msg.id),
      children: [],
    };
    this.nodes.set(msg.id, node);
    this.deletedIds.delete(msg.id);
    const at = siblings.findIndex((sib) => compareIds(sib, msg.id) > 0);
    if (at === -1) {
      siblings.push(msg.id);
    } else {
      siblings.splice(at, 0, msg.id);
    }
    const queued = this.pending.get(msg.id);
    if (queued !== undefined) {
      this.pending.delete(msg.id);
      for (const child of queued) {
        this.applyInsert(child);
      }
    }
  }

  private buffer(msg: InsertMsg<T>): void {
    let bucket = this.pending.get(msg.pred);
    if (bucket === undefined) {
      bucket = [];
      this.pending.set(msg.pred, bucket);
    }
    for (const op of bucket) {
      if (op.id === msg.id) return;
    }
    bucket.push(msg);
  }

  private applyDelete(id: string): void {
    const node = this.nodes.get(id);
    if (node !== undefined) {
      node.deleted = true;
      return;
    }
    this.deletedIds.add(id);
  }
}
