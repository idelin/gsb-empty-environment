import { EdgeMessage, PartitionId, TxnId, WaitEdge, edgeKey } from './types';

/**
 * A single partition (shard / node) of the distributed system.
 *
 * It maintains its local wait-for graph, enforces the invariant that the
 * local graph stays acyclic, and streams versioned edge messages to the
 * global coordinator. Versions come from a per-partition Lamport-style
 * clock, so every message on a given edge is strictly increasing.
 *
 * Only the waiting end (`from`) of an edge must be a local transaction;
 * the lock holder (`to`) may live on another partition, which is exactly
 * how cross-partition cycles arise while every local graph stays acyclic.
 */
export class LocalPartition {
  private readonly edges = new Map<string, WaitEdge>();
  private readonly liveTxns = new Set<TxnId>();
  private clock = 0;

  constructor(
    public readonly id: PartitionId,
    private readonly sink: (msg: EdgeMessage) => void,
  ) {}

  begin(txn: TxnId): void {
    this.liveTxns.add(txn);
  }

  isAlive(txn: TxnId): boolean {
    return this.liveTxns.has(txn);
  }

  /** Record that local `from` started waiting on `to` (possibly remote). */
  addWait(from: TxnId, to: TxnId): WaitEdge {
    if (!this.liveTxns.has(from)) {
      throw new Error(`partition ${this.id}: unknown local transaction ${from}`);
    }
    const key = edgeKey(from, to);
    const existing = this.edges.get(key);
    if (existing) return existing; // already waiting; idempotent
    const edge: WaitEdge = { from, to, version: ++this.clock, partition: this.id };
    this.edges.set(key, edge);
    if (this.hasLocalCycle()) {
      this.edges.delete(key);
      throw new Error(`partition ${this.id}: edge ${from}->${to} would create a local cycle`);
    }
    this.sink({ kind: 'add', from, to, version: edge.version, partition: this.id });
    return edge;
  }

  /** Record that `from` stopped waiting on `to` (lock granted, txn ended, timeout). */
  removeWait(from: TxnId, to: TxnId): void {
    const key = edgeKey(from, to);
    if (!this.edges.delete(key)) return;
    this.sink({ kind: 'remove', from, to, version: ++this.clock, partition: this.id });
  }

  /** Finish a local transaction: drop all its local wait edges and mark it dead. */
  finish(txn: TxnId): void {
    for (const edge of [...this.edges.values()]) {
      if (edge.from === txn || edge.to === txn) {
        this.removeWait(edge.from, edge.to);
      }
    }
    this.liveTxns.delete(txn);
  }

  hasLocalCycle(): boolean {
    const adj = new Map<TxnId, TxnId[]>();
    for (const e of this.edges.values()) {
      const list = adj.get(e.from) ?? [];
      list.push(e.to);
      adj.set(e.from, list);
    }
    const state = new Map<TxnId, 1 | 2>(); // 1=in stack, 2=done
    const visit = (node: TxnId): boolean => {
      state.set(node, 1);
      for (const next of adj.get(node) ?? []) {
        const s = state.get(next);
        if (s === 1) return true;
        if (s === undefined && visit(next)) return true;
      }
      state.set(node, 2);
      return false;
    };
    for (const node of adj.keys()) {
      if (!state.has(node) && visit(node)) return true;
    }
    return false;
  }
}
