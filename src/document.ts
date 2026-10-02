import { getSeq, type VersionVector } from './versionVector.ts';

export type ReplicaId = string;

/** Sentinel parent id for inserts at the very beginning of the document. */
export const ROOT_ID = '';

/**
 * Compact wire-friendly operation shapes. Every mutation of a replica is
 * stamped with a per-replica contiguous sequence number `s`, which is what
 * makes contiguous delta-intervals and watermark-based acks possible.
 */
export interface InsertOp {
  t: 'i';
  id: string; // unique char id: `${r}:${s}`
  p: string; // parent (insert-after) char id, ROOT_ID for doc head
  c: string; // the character
  r: ReplicaId;
  s: number;
}

export interface DeleteOp {
  t: 'd';
  id: string; // target char id (tombstone, idempotent)
  r: ReplicaId;
  s: number;
}

export type Op = InsertOp | DeleteOp;

interface CharNode {
  parent: string;
  char: string;
  tombstone: boolean;
  replica: ReplicaId;
  seq: number;
}

interface ChildRef {
  id: string;
  seq: number;
  replica: ReplicaId;
}

/**
 * Delta-state sequence CRDT (RGA-style) over a replicated op log.
 *
 * State = for each replica, the contiguous log of ops it produced
 * (index = seq - 1). Join = per-replica log union, which is safe because
 * only the origin replica ever extends its own log and delta-intervals are
 * contiguous. The version vector of the state is exactly the log lengths.
 *
 * deltaSince(vv) yields the incremental "delta slice" of everything that
 * changed after `vv` — this is the only thing put on the wire during
 * incremental sync, giving O(changed ops) packet size instead of O(document).
 */
export class DeltaDocument {
  readonly replicaId: ReplicaId;

  private localSeq = 0;
  private readonly logs = new Map<ReplicaId, Op[]>();
  private readonly pending = new Map<ReplicaId, Map<number, Op>>();

  // Incrementally maintained materialization (RGA tree).
  private readonly nodes = new Map<string, CharNode>();
  private readonly children = new Map<string, ChildRef[]>();
  private readonly awaitingParent = new Map<string, InsertOp[]>();
  private readonly pendingTombstones = new Set<string>();
  private tailId: string | null = null; // lazy cache for O(1) append

  constructor(replicaId: ReplicaId) {
    this.replicaId = replicaId;
  }

  // ---------------------------------------------------------------- local ops

  /** Insert `char` after `parentId` (null = document head). */
  insertAfter(parentId: string | null, char: string): InsertOp {
    if (char.length === 0) throw new Error('cannot insert empty char');
    const seq = ++this.localSeq;
    const op: InsertOp = {
      t: 'i',
      id: `${this.replicaId}:${seq}`,
      p: parentId ?? ROOT_ID,
      c: char,
      r: this.replicaId,
      s: seq,
    };
    const tailBefore = this.tailId;
    this.commit(op);
    if (tailBefore !== null && op.p === tailBefore) this.tailId = op.id;
    return op;
  }

  /** Insert `char` so that it becomes visible position `index`. */
  insertAt(index: number, char: string): InsertOp {
    const ids = this.visibleIds();
    if (index <= 0) return this.insertAfter(null, char);
    if (index >= ids.length) return this.insertAfter(ids[ids.length - 1], char);
    return this.insertAfter(ids[index - 1], char);
  }

  /** O(1) amortized append at the document tail. */
  append(char: string): InsertOp {
    return this.insertAfter(this.currentTailId(), char);
  }

  /** Tombstone the visible char at `index`. Returns null if out of range. */
  deleteAt(index: number): DeleteOp | null {
    const ids = this.visibleIds();
    if (index < 0 || index >= ids.length) return null;
    const seq = ++this.localSeq;
    const op: DeleteOp = { t: 'd', id: ids[index], r: this.replicaId, s: seq };
    this.commit(op);
    return op;
  }

  // ------------------------------------------------------------------- merge

  /**
   * Apply one op. Idempotent; buffers ops that arrive ahead of their
   * per-replica causal predecessor. Returns true if it was new.
   */
  applyOp(op: Op): boolean {
    const log = this.logFor(op.r);
    if (op.s <= log.length) return false; // duplicate delivery
    if (op.s > log.length + 1) {
      let buf = this.pending.get(op.r);
      if (!buf) this.pending.set(op.r, (buf = new Map()));
      buf.set(op.s, op);
      return false;
    }
    this.commit(op);
    const buf = this.pending.get(op.r);
    if (buf) {
      let next = log.length + 1;
      let queued = buf.get(next);
      while (queued) {
        buf.delete(next);
        this.commit(queued);
        next = log.length + 1;
        queued = buf.get(next);
      }
      if (buf.size === 0) this.pending.delete(op.r);
    }
    return true;
  }

  /** Merge a delta slice or a full state (both are just op lists). */
  merge(ops: Iterable<Op>): number {
    let applied = 0;
    for (const op of ops) if (this.applyOp(op)) applied++;
    return applied;
  }

  // ------------------------------------------------------------------ deltas

  /** Version vector of the current state (per-replica contiguous lengths). */
  version(): VersionVector {
    const vv: VersionVector = {};
    for (const [replica, log] of this.logs) {
      if (log.length > 0) vv[replica] = log.length;
    }
    return vv;
  }

  /**
   * The incremental delta slice: every op with seq > vv[replica], grouped by
   * replica (sorted ids for determinism) and ordered by seq so each replica's
   * causal order is preserved on the wire.
   */
  deltaSince(vv: VersionVector): Op[] {
    const ops: Op[] = [];
    const replicas = [...this.logs.keys()].sort();
    for (const replica of replicas) {
      const log = this.logs.get(replica)!;
      for (let i = getSeq(vv, replica); i < log.length; i++) ops.push(log[i]);
    }
    return ops;
  }

  /** Full state transfer = delta from the zero vector. */
  serializeState(): Op[] {
    return this.deltaSince({});
  }

  /** Canonical fingerprint: equal iff two replicas hold equivalent state. */
  fingerprint(): string {
    return JSON.stringify(this.serializeState());
  }

  // --------------------------------------------------------- materialization

  /** Visible text (tombstones skipped). */
  text(): string {
    let out = '';
    for (const id of this.orderedIds()) {
      const node = this.nodes.get(id)!;
      if (!node.tombstone) out += node.char;
    }
    return out;
  }

  /** Ids of visible chars in document order. */
  visibleIds(): string[] {
    const ids: string[] = [];
    for (const id of this.orderedIds()) {
      if (!this.nodes.get(id)!.tombstone) ids.push(id);
    }
    return ids;
  }

  get length(): number {
    let n = 0;
    for (const node of this.nodes.values()) if (!node.tombstone) n++;
    return n;
  }

  // ----------------------------------------------------------------- private

  private logFor(replica: ReplicaId): Op[] {
    let log = this.logs.get(replica);
    if (!log) this.logs.set(replica, (log = []));
    return log;
  }

  private commit(op: Op): void {
    this.logFor(op.r).push(op);
    if (op.r === this.replicaId) this.localSeq = Math.max(this.localSeq, op.s);
    if (op.t === 'i') this.commitInsert(op);
    else this.commitDelete(op);
    this.tailId = null; // conservative invalidation; append() re-caches
  }

  private commitInsert(op: InsertOp): void {
    if (this.nodes.has(op.id)) return;
    const tombstone = this.pendingTombstones.delete(op.id);
    this.nodes.set(op.id, {
      parent: op.p,
      char: op.c,
      tombstone,
      replica: op.r,
      seq: op.s,
    });
    if (op.p !== ROOT_ID && !this.nodes.has(op.p)) {
      // Parent not materialized yet (cross-replica causal gap): park it.
      const list = this.awaitingParent.get(op.p) ?? [];
      list.push(op);
      this.awaitingParent.set(op.p, list);
    } else {
      this.attach(op.id, op.s, op.r, op.p);
    }
    const orphans = this.awaitingParent.get(op.id);
    if (orphans) {
      this.awaitingParent.delete(op.id);
      for (const orphan of orphans) this.attach(orphan.id, orphan.s, orphan.r, orphan.p);
    }
  }

  private commitDelete(op: DeleteOp): void {
    const node = this.nodes.get(op.id);
    if (node) node.tombstone = true;
    else this.pendingTombstones.add(op.id); // target insert not seen yet
  }

  /** Insert into parent's child list, sorted by (seq, replica) descending. */
  private attach(id: string, seq: number, replica: ReplicaId, parent: string): void {
    let list = this.children.get(parent);
    if (!list) this.children.set(parent, (list = []));
    let lo = 0;
    let hi = list.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const c = list[mid];
      const before = c.seq > seq || (c.seq === seq && c.replica > replica);
      if (before) lo = mid + 1;
      else hi = mid;
    }
    list.splice(lo, 0, { id, seq, replica });
  }

  /**
   * Pre-order walk of the RGA tree (iterative — recursion would overflow the
   * stack on deep chain-shaped documents, and generators benchmark far
   * slower here than a plain loop).
   */
  private orderedIds(): string[] {
    const out: string[] = [];
    const stacks: ChildRef[][] = [this.children.get(ROOT_ID) ?? []];
    const idx: number[] = [0];
    while (stacks.length > 0) {
      const top = stacks[stacks.length - 1];
      const i = idx[idx.length - 1];
      if (i >= top.length) {
        stacks.pop();
        idx.pop();
        continue;
      }
      idx[idx.length - 1] = i + 1;
      const id = top[i].id;
      out.push(id);
      const kids = this.children.get(id);
      if (kids && kids.length > 0) {
        stacks.push(kids);
        idx.push(0);
      }
    }
    return out;
  }

  private currentTailId(): string {
    if (this.tailId !== null) return this.tailId;
    let last = ROOT_ID;
    for (const id of this.orderedIds()) {
      if (!this.nodes.get(id)!.tombstone) last = id;
    }
    this.tailId = last;
    return last;
  }
}
