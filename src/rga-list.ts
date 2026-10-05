/**
 * RGA-style replicated list CRDT.
 *
 * - Stable identity: every element gets an immutable, globally unique Id
 *   { counter, replica } (Lamport-style counter per replica).
 * - Deterministic order: siblings sharing the same parent anchor are sorted
 *   by (counter desc, replica desc) — a total order, identical on all replicas.
 * - Tombstones: delete only flags a node; the node stays in the tree so it can
 *   still serve as an insert anchor, and surviving elements never change their
 *   relative order.
 * - Out-of-order delivery: inserts whose parent anchor is not yet known are
 *   held in a causal buffer and flushed once the parent arrives; deletes are
 *   recorded in a tombstone set immediately and commute with inserts.
 */

export interface Id {
  counter: number;
  replica: string;
}

export type InsertOp = {
  type: "insert";
  id: Id;
  /** Anchor: insert after this element. null = beginning of the list. */
  parent: Id | null;
  value: string;
};

export type DeleteOp = {
  type: "delete";
  target: Id;
};

export type Op = InsertOp | DeleteOp;

export function idKey(id: Id): string {
  return id.counter + "@" + id.replica;
}

/** Total order on Ids: higher counter first, ties broken by higher replica id. */
export function compareIds(a: Id, b: Id): number {
  if (a.counter !== b.counter) return b.counter - a.counter;
  if (a.replica === b.replica) return 0;
  return a.replica < b.replica ? 1 : -1;
}

interface Node {
  id: Id;
  parent: Id | null;
  value: string;
  deleted: boolean;
  /** Kept sorted by compareIds (descending). */
  children: Node[];
}

function insertSorted(siblings: Node[], node: Node): void {
  let lo = 0;
  let hi = siblings.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (compareIds(node.id, siblings[mid].id) < 0) hi = mid;
    else lo = mid + 1;
  }
  siblings.splice(lo, 0, node);
}

export class RgaList {
  readonly replica: string;
  private counter = 0;
  private readonly nodes = new Map<string, Node>();
  private readonly roots: Node[] = [];
  private readonly tombstones = new Set<string>();
  private readonly buffer: InsertOp[] = [];

  constructor(replica: string) {
    if (!replica) throw new Error("replica id must be non-empty");
    this.replica = replica;
  }

  // ---- local mutation API -------------------------------------------------

  /** Insert `value` after the element `parent` (null = list head). */
  insertAfter(parent: Id | null, value: string): InsertOp {
    const op: InsertOp = {
      type: "insert",
      id: { counter: ++this.counter, replica: this.replica },
      parent,
      value,
    };
    this.apply(op);
    return op;
  }

  /** Insert `value` so it becomes the visible element at `index`. */
  insertAt(index: number, value: string): InsertOp {
    const visible = this.visibleNodes();
    if (index < 0 || index > visible.length) {
      throw new RangeError("index out of range: " + index);
    }
    const parent = index === 0 ? null : visible[index - 1].id;
    return this.insertAfter(parent, value);
  }

  delete(id: Id): DeleteOp {
    const op: DeleteOp = { type: "delete", target: id };
    this.apply(op);
    return op;
  }

  deleteAt(index: number): DeleteOp {
    const visible = this.visibleNodes();
    if (index < 0 || index >= visible.length) {
      throw new RangeError("index out of range: " + index);
    }
    return this.delete(visible[index].id);
  }

  // ---- remote op application ----------------------------------------------

  apply(op: Op): void {
    if (op.type === "insert") {
      this.applyInsert(op);
    } else {
      this.applyDelete(op);
    }
  }

  applyAll(ops: Iterable<Op>): void {
    for (const op of ops) this.apply(op);
  }

  private applyInsert(op: InsertOp): void {
    const key = idKey(op.id);
    if (this.nodes.has(key)) return; // idempotent re-delivery

    // Keep the local counter ahead of every id we have seen so locally
    // minted ids stay unique and causally fresh.
    if (op.id.counter > this.counter) this.counter = op.id.counter;

    if (op.parent !== null && !this.nodes.has(idKey(op.parent))) {
      this.buffer.push(op); // missing predecessor: wait for the anchor
      return;
    }

    const node: Node = {
      id: op.id,
      parent: op.parent,
      value: op.value,
      deleted: this.tombstones.has(key),
      children: [],
    };
    this.nodes.set(key, node);
    const siblings =
      op.parent === null ? this.roots : this.nodes.get(idKey(op.parent))!.children;
    insertSorted(siblings, node);

    this.flushBuffer();
  }

  private applyDelete(op: DeleteOp): void {
    const key = idKey(op.target);
    this.tombstones.add(key);
    const node = this.nodes.get(key);
    if (node) node.deleted = true;
    // If the insert arrives later, applyInsert consults `tombstones`.
  }

  private flushBuffer(): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (let i = 0; i < this.buffer.length; i++) {
        const op = this.buffer[i];
        if (op.parent === null || this.nodes.has(idKey(op.parent))) {
          this.buffer.splice(i, 1);
          i--;
          this.applyInsert(op);
          progressed = true;
        }
      }
    }
  }

  // ---- reads --------------------------------------------------------------

  get pendingCount(): number {
    return this.buffer.length;
  }

  has(id: Id): boolean {
    return this.nodes.has(idKey(id));
  }

  isDeleted(id: Id): boolean {
    return this.tombstones.has(idKey(id));
  }

  private visibleNodes(): Node[] {
    const out: Node[] = [];
    const walk = (nodes: Node[]) => {
      for (const node of nodes) {
        if (!node.deleted) out.push(node);
        walk(node.children);
      }
    };
    walk(this.roots);
    return out;
  }

  /** Visible values in list order (tombstones hidden). */
  toArray(): string[] {
    return this.visibleNodes().map((n) => n.value);
  }

  /** Visible entries with their stable ids, in list order. */
  entries(): Array<{ id: Id; value: string }> {
    return this.visibleNodes().map((n) => ({
      id: { ...n.id },
      value: n.value,
    }));
  }

  /**
   * Full structural dump (including tombstones and buffered ops). Two replicas
   * that have applied the same set of ops must produce identical dumps,
   * regardless of delivery order.
   */
  serialize(): string {
    const lines: string[] = [];
    const walk = (nodes: Node[], depth: number) => {
      for (const node of nodes) {
        lines.push(
          "  ".repeat(depth) +
            idKey(node.id) +
            (node.deleted ? " [x] " : " ") +
            JSON.stringify(node.value)
        );
        walk(node.children, depth + 1);
      }
    };
    walk(this.roots, 0);
    if (this.buffer.length > 0) {
      lines.push(
        "buffered: " +
          this.buffer.map((op) => idKey(op.id)).sort().join(",")
      );
    }
    return lines.join("\n");
  }
}
