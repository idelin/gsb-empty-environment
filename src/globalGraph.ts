import { EdgeMessage, TxnId, WaitEdge, edgeKey } from './types';

/**
 * Global wait-for graph maintained by the coordinator.
 *
 * Edges arrive as versioned add/remove messages from partitions. Delivery may
 * be reordered, duplicated, or delayed. To guarantee that a stale `add` can
 * never resurrect an edge that was already removed, every edge key keeps a
 * high-water mark of the greatest version ever observed (for either kind of
 * message); any message at or below the mark is discarded.
 */
export class GlobalWaitGraph {
  private readonly edges = new Map<string, WaitEdge>();
  private readonly highWater = new Map<string, number>();
  private appliedCount = 0;

  /** Apply one message. Returns true if it changed coordinator state. */
  apply(msg: EdgeMessage): boolean {
    const key = edgeKey(msg.from, msg.to);
    const seen = this.highWater.get(key) ?? -1;
    if (msg.version <= seen) {
      return false; // stale or duplicate: never resurrect an old edge
    }
    this.highWater.set(key, msg.version);
    this.appliedCount++;
    if (msg.kind === 'add') {
      this.edges.set(key, {
        from: msg.from,
        to: msg.to,
        version: msg.version,
        partition: msg.partition,
      });
    } else {
      this.edges.delete(key);
    }
    return true;
  }

  /** Apply a batch, returning how many messages were accepted. */
  applyAll(messages: Iterable<EdgeMessage>): number {
    let accepted = 0;
    for (const msg of messages) {
      if (this.apply(msg)) accepted++;
    }
    return accepted;
  }

  getEdge(from: TxnId, to: TxnId): WaitEdge | undefined {
    return this.edges.get(edgeKey(from, to));
  }

  hasEdge(from: TxnId, to: TxnId, version?: number): boolean {
    const edge = this.edges.get(edgeKey(from, to));
    if (!edge) return false;
    return version === undefined || edge.version === version;
  }

  snapshotEdges(): WaitEdge[] {
    return [...this.edges.values()].map((e) => ({ ...e }));
  }

  /** Monotonic count of accepted messages; acts as the coordinator epoch. */
  get epoch(): number {
    return this.appliedCount;
  }

  get size(): number {
    return this.edges.size;
  }
}
