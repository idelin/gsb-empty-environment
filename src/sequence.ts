/**
 * Offline-first text sequence CRDT based on stable element IDs (RGA-style).
 *
 * - Every element carries a stable, globally unique id `{ replica, counter }`.
 * - Inserts name their causal dependency (`left`). Inserts whose dependency
 *   has not arrived yet are buffered, never attached to an arbitrary position.
 * - Deletes are idempotent and leave tombstones. A delete that arrives before
 *   its insert is remembered, so the character stays invisible once the
 *   insert finally lands.
 * - `merge` is a set-union over operations, hence commutative, associative
 *   and idempotent.
 * - Tombstones are garbage-collected only after every known replica has
 *   acknowledged a state-vector watermark covering both the element and the
 *   delete that removed it.
 * - Public offsets are UTF-16 code-unit offsets. Elements hold whole Unicode
 *   code points, so offsets never fall inside a surrogate pair; callers that
 *   pass a mid-pair offset are clamped to the nearest safe boundary.
 */

export interface ElementId {
  replica: string;
  counter: number;
}

export interface InsertOp {
  type: "insert";
  id: ElementId;
  /** Causal dependency: the element this one is inserted after, or null for head. */
  left: ElementId | null;
  /** Exactly one Unicode code point (1 or 2 UTF-16 code units). */
  value: string;
}

export interface DeleteOp {
  type: "delete";
  id: ElementId;
  target: ElementId;
}

export type Op = InsertOp | DeleteOp;

/** replica -> highest contiguous counter that replica is known to have seen. */
export type StateVector = Record<string, number>;
/** replica -> that replica's acknowledged state vector. */
export type AckSet = Record<string, StateVector>;

interface Element {
  id: ElementId;
  key: string;
  left: string | null;
  value: string;
  deleted: boolean;
  deletedBy: ElementId | null;
}

function idKey(id: ElementId): string {
  return JSON.stringify([id.replica, id.counter]);
}

function keyToId(key: string): ElementId {
  const [replica, counter] = JSON.parse(key) as [string, number];
  return { replica, counter };
}

/** Newer siblings (higher counter, then higher replica name) sort first. */
function compareSiblings(a: ElementId, b: ElementId): number {
  if (a.counter !== b.counter) return b.counter - a.counter;
  if (a.replica === b.replica) return 0;
  return a.replica < b.replica ? 1 : -1;
}

export class TextSequence {
  readonly replicaId: string;

  private counter = 0;
  private readonly elements = new Map<string, Element>();
  private readonly children = new Map<string | null, string[]>();
  /** Inserts buffered on a not-yet-known left dependency: leftKey -> ops. */
  private readonly waiting = new Map<string, InsertOp[]>();
  /** Deletes that arrived before their target insert: targetKey -> delete op id. */
  private readonly pendingDeletes = new Map<string, ElementId>();
  /** GC'd element keys -> the left link they had, so late arrivals can be re-linked. */
  private readonly collected = new Map<string, string | null>();
  private readonly seenOps = new Set<string>();
  private readonly seenCounters = new Map<string, Set<number>>();
  private readonly opLog: Op[] = [];
  private readonly knownReplicas = new Set<string>();

  constructor(replicaId: string) {
    this.replicaId = replicaId;
    this.knownReplicas.add(replicaId);
  }

  // ---------------------------------------------------------------- queries

  /** Visible text. */
  toString(): string {
    let out = "";
    for (const el of this.visibleElements()) out += el.value;
    return out;
  }

  /** Visible length in UTF-16 code units. */
  get length(): number {
    let n = 0;
    for (const el of this.visibleElements()) n += el.value.length;
    return n;
  }

  /** Number of deleted-but-not-yet-collected elements. */
  get tombstoneCount(): number {
    let n = 0;
    for (const el of this.elements.values()) if (el.deleted) n++;
    return n;
  }

  /** Number of inserts buffered on missing causal dependencies. */
  get bufferedCount(): number {
    let n = 0;
    for (const ops of this.waiting.values()) n += ops.length;
    return n;
  }

  /** All operations known to this replica, in arrival order. */
  getOps(): Op[] {
    return this.opLog.slice();
  }

  /** Highest contiguous op counter seen from each replica. */
  getStateVector(): StateVector {
    const vector: StateVector = {};
    for (const [replica, counters] of this.seenCounters) {
      let n = 0;
      while (counters.has(n + 1)) n++;
      vector[replica] = n;
    }
    return vector;
  }

  // ------------------------------------------------------- local mutations

  /**
   * Insert `text` at UTF-16 code-unit `offset`. A mid-surrogate-pair offset
   * is clamped down to the pair's start. Returns the ops to broadcast.
   */
  insertAt(offset: number, text: string): InsertOp[] {
    const at = this.clampOffsetFloor(offset);
    let leftKey = this.leftKeyAtOffset(at);
    const ops: InsertOp[] = [];
    for (const value of text) {
      const id: ElementId = { replica: this.replicaId, counter: ++this.counter };
      const op: InsertOp = {
        type: "insert",
        id,
        left: leftKey === null ? null : keyToId(leftKey),
        value,
      };
      this.apply(op);
      ops.push(op);
      leftKey = idKey(id);
    }
    return ops;
  }

  /**
   * Delete the visible characters intersecting the UTF-16 range
   * `[offset, offset + count)`. The range is widened to whole code points so
   * a surrogate pair is never split. Returns the ops to broadcast.
   */
  deleteAt(offset: number, count: number): DeleteOp[] {
    const start = this.clampOffsetFloor(offset);
    const end = this.clampOffsetCeil(offset + count);
    const ops: DeleteOp[] = [];
    let acc = 0;
    for (const el of this.visibleElements()) {
      const elStart = acc;
      const elEnd = acc + el.value.length;
      acc = elEnd;
      if (elStart < start || elEnd > end) continue;
      const op: DeleteOp = {
        type: "delete",
        id: { replica: this.replicaId, counter: ++this.counter },
        target: el.id,
      };
      this.apply(op);
      ops.push(op);
    }
    return ops;
  }

  // --------------------------------------------------------- op replication

  /** Apply a (possibly remote, possibly duplicated, possibly early) op. */
  apply(op: Op): void {
    const opKey = idKey(op.id);
    if (this.seenOps.has(opKey)) return; // duplicate delivery: no-op
    this.seenOps.add(opKey);
    this.opLog.push(op);

    let counters = this.seenCounters.get(op.id.replica);
    if (!counters) {
      counters = new Set();
      this.seenCounters.set(op.id.replica, counters);
    }
    counters.add(op.id.counter);

    if (op.type === "insert") this.applyInsert(op);
    else this.applyDelete(op);
  }

  /** State-based merge: absorb every op the other replica knows. */
  merge(other: TextSequence): void {
    this.knownReplicas.add(other.replicaId);
    for (const op of other.getOps()) this.apply(op);
  }

  // --------------------------------------------------------------------- GC

  /**
   * Collect tombstones confirmed by every known replica. `acks` maps each
   * replica to the state vector it has acknowledged. A tombstone is removed
   * only if all known replicas acked both the element and its delete.
   * Returns the number of collected tombstones.
   */
  collectGarbage(acks: AckSet): number {
    const known = new Set<string>([...this.knownReplicas, ...this.seenCounters.keys()]);
    let collectedNow = 0;
    for (const [key, el] of [...this.elements]) {
      if (!el.deleted || el.deletedBy === null) continue;
      if (!this.confirmedByAll(el.id, acks, known)) continue;
      if (!this.confirmedByAll(el.deletedBy, acks, known)) continue;
      this.collect(key, el);
      collectedNow++;
    }
    return collectedNow;
  }

  private confirmedByAll(id: ElementId, acks: AckSet, known: Set<string>): boolean {
    for (const replica of known) {
      const vector = acks[replica];
      if (!vector) return false;
      if ((vector[id.replica] ?? 0) < id.counter) return false;
    }
    return true;
  }

  private collect(key: string, el: Element): void {
    // Re-link live children onto the collected element's left neighbour.
    const childKeys = this.children.get(key) ?? [];
    const parentList = (this.children.get(el.left) ?? []).filter((k) => k !== key);
    parentList.push(...childKeys);
    this.children.set(el.left, parentList);
    for (const childKey of childKeys) {
      const child = this.elements.get(childKey);
      if (child) child.left = el.left;
    }
    this.children.delete(key);
    this.elements.delete(key);
    this.collected.set(key, el.left);

    // Inserts that were buffered on the collected element can now be retried;
    // resolveLeft walks through the collected chain.
    const unblocked = this.waiting.get(key);
    if (unblocked) {
      this.waiting.delete(key);
      for (const op of unblocked) this.applyInsert(op);
    }
  }

  // --------------------------------------------------------------- internal

  private applyInsert(op: InsertOp): void {
    const key = idKey(op.id);
    if (this.elements.has(key) || this.collected.has(key)) return;
    const leftKey = this.resolveLeft(op.left);
    if (leftKey !== null && !this.elements.has(leftKey)) {
      // Causal dependency missing: buffer, never attach to an arbitrary spot.
      const list = this.waiting.get(leftKey) ?? [];
      list.push(op);
      this.waiting.set(leftKey, list);
      return;
    }
    const deletedBy = this.pendingDeletes.get(key) ?? null;
    if (deletedBy) this.pendingDeletes.delete(key);
    this.elements.set(key, {
      id: op.id,
      key,
      left: leftKey,
      value: op.value,
      deleted: deletedBy !== null,
      deletedBy,
    });
    const siblings = this.children.get(leftKey) ?? [];
    siblings.push(key);
    this.children.set(leftKey, siblings);

    const unblocked = this.waiting.get(key);
    if (unblocked) {
      this.waiting.delete(key);
      for (const child of unblocked) this.applyInsert(child);
    }
  }

  private applyDelete(op: DeleteOp): void {
    const targetKey = idKey(op.target);
    if (this.collected.has(targetKey)) return;
    const el = this.elements.get(targetKey);
    if (el) {
      if (!el.deleted) {
        el.deleted = true;
        el.deletedBy = op.id;
      }
    } else if (!this.pendingDeletes.has(targetKey)) {
      // Delete arrived before the insert: remember it as a future tombstone.
      this.pendingDeletes.set(targetKey, op.id);
    }
  }

  private resolveLeft(left: ElementId | null): string | null {
    let key = left === null ? null : idKey(left);
    while (key !== null && this.collected.has(key)) {
      key = this.collected.get(key)!;
    }
    return key;
  }

  private orderedElements(): Element[] {
    const out: Element[] = [];
    const visit = (leftKey: string | null): void => {
      const kids = (this.children.get(leftKey) ?? [])
        .slice()
        .sort((a, b) => compareSiblings(this.elements.get(a)!.id, this.elements.get(b)!.id));
      for (const key of kids) {
        const el = this.elements.get(key)!;
        out.push(el);
        visit(key);
      }
    };
    visit(null);
    return out;
  }

  private visibleElements(): Element[] {
    return this.orderedElements().filter((el) => !el.deleted);
  }

  private boundaries(): number[] {
    const result = [0];
    let acc = 0;
    for (const el of this.visibleElements()) {
      acc += el.value.length;
      result.push(acc);
    }
    return result;
  }

  private clampOffsetFloor(offset: number): number {
    let best = 0;
    for (const b of this.boundaries()) {
      if (b <= offset) best = b;
      else break;
    }
    return best;
  }

  private clampOffsetCeil(offset: number): number {
    const all = this.boundaries();
    for (const b of all) {
      if (b >= offset) return b;
    }
    return all[all.length - 1];
  }

  private leftKeyAtOffset(offset: number): string | null {
    let acc = 0;
    let last: string | null = null;
    for (const el of this.visibleElements()) {
      if (acc >= offset) break;
      acc += el.value.length;
      last = el.key;
    }
    return last;
  }
}
