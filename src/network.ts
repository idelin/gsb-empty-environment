import type { Message, MessageType, NodeId } from './types.ts';

export interface Receivable {
  readonly id: NodeId;
  receive(msg: Message): void;
  onTopologyChange(): void;
}

/**
 * Deterministic in-memory network simulator.
 * Messages are delivered asynchronously (setImmediate) so tests can
 * `await net.flush()` to reach a quiescent state. Supports symmetric
 * partitions between node sets and full healing.
 */
export class Network {
  private nodes = new Map<NodeId, Receivable>();
  private blocked = new Set<string>();
  private queue: Array<() => void> = [];
  private pumping = false;
  private pending = 0;
  private flushWaiters: Array<() => void> = [];

  readonly stats = {
    delivered: 0,
    dropped: 0,
    byType: new Map<MessageType, number>(),
  };

  register(node: Receivable): void {
    this.nodes.set(node.id, node);
  }

  size(): number {
    return this.nodes.size;
  }

  private pairKey(a: NodeId, b: NodeId): string {
    return a < b ? `${a}|${b}` : `${b}|${a}`;
  }

  /** Cut all links between the two sets (symmetric). */
  cut(groupA: NodeId[], groupB: NodeId[]): void {
    for (const a of groupA) {
      for (const b of groupB) this.blocked.add(this.pairKey(a, b));
    }
    this.notifyTopologyChange();
  }

  heal(): void {
    this.blocked.clear();
    this.notifyTopologyChange();
  }

  isBlocked(a: NodeId, b: NodeId): boolean {
    return this.blocked.has(this.pairKey(a, b));
  }

  /** Number of nodes reachable from `id`, including itself. */
  reachableCount(id: NodeId): number {
    let count = 0;
    for (const other of this.nodes.keys()) {
      if (other === id || !this.isBlocked(id, other)) count++;
    }
    return count;
  }

  private notifyTopologyChange(): void {
    for (const node of this.nodes.values()) node.onTopologyChange();
  }

  send(msg: Message): void {
    const targets = msg.to
      ? [msg.to]
      : [...this.nodes.keys()].filter((id) => id !== msg.from);
    for (const to of targets) {
      if (this.isBlocked(msg.from, to)) {
        this.stats.dropped++;
        continue;
      }
      this.stats.byType.set(msg.type, (this.stats.byType.get(msg.type) ?? 0) + 1);
      this.pending++;
      const node = this.nodes.get(to);
      this.queue.push(() => {
        try {
          this.stats.delivered++;
          node?.receive(msg);
        } finally {
          this.pending--;
        }
      });
    }
    this.pump();
  }

  private pump(): void {
    if (this.pumping) return;
    this.pumping = true;
    const step = (): void => {
      const job = this.queue.shift();
      if (job) {
        job();
        setImmediate(step);
      } else {
        this.pumping = false;
        if (this.pending === 0) this.resolveFlush();
      }
    };
    setImmediate(step);
  }

  private resolveFlush(): void {
    const waiters = this.flushWaiters.splice(0);
    for (const w of waiters) w();
  }

  /** Resolves once every in-flight message has been delivered. */
  flush(): Promise<void> {
    if (this.pending === 0 && this.queue.length === 0) return Promise.resolve();
    return new Promise((resolve) => this.flushWaiters.push(resolve));
  }

  resetStats(): void {
    this.stats.delivered = 0;
    this.stats.dropped = 0;
    this.stats.byType.clear();
  }
}
