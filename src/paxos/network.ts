import type { Message, MessageType } from './types.ts';
import type { PaxosNode } from './node.ts';

/**
 * Deterministic in-memory transport with partition support.
 *
 * `send` only enqueues onto the receiver's lock-free inbox; nothing is
 * handled until `pump()` runs, which keeps tests fully deterministic.
 * Links can be cut individually to simulate network partitions.
 */
export class Network {
  private readonly nodes: PaxosNode[] = [];
  private readonly open = new Map<string, boolean>();
  readonly stats = {
    sent: 0,
    dropped: 0,
    delivered: 0,
    byType: new Map<MessageType, number>(),
  };

  register(node: PaxosNode): void {
    this.nodes[node.id] = node;
    node.network = this;
  }

  private linkKey(from: number, to: number): string {
    return `${from}->${to}`;
  }

  setLink(from: number, to: number, open: boolean): void {
    this.open.set(this.linkKey(from, to), open);
  }

  /** Bidirectionally cut/restablish the link between a and b. */
  setPartition(a: number, b: number, connected: boolean): void {
    this.setLink(a, b, connected);
    this.setLink(b, a, connected);
  }

  /** Cut all links between group A and group B (a network partition). */
  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) for (const b of groupB) this.setPartition(a, b, false);
  }

  isolate(id: number): void {
    for (const node of this.nodes) {
      if (node.id !== id) this.setPartition(id, node.id, false);
    }
  }

  healAll(): void {
    this.open.clear();
  }

  private isOpen(from: number, to: number): boolean {
    return this.open.get(this.linkKey(from, to)) ?? true;
  }

  send(from: number, to: number, msg: Message): void {
    if (from === to) {
      this.nodes[to].receive(msg);
      return;
    }
    this.stats.sent++;
    this.stats.byType.set(msg.type, (this.stats.byType.get(msg.type) ?? 0) + 1);
    if (!this.isOpen(from, to)) {
      this.stats.dropped++;
      return;
    }
    this.nodes[to].receive(msg);
  }

  broadcast(from: number, msg: Message): void {
    for (const node of this.nodes) {
      if (node.id !== from) this.send(from, node.id, msg);
    }
  }

  /** Deliver everything currently queued. Returns messages handled. */
  pump(): number {
    let handled = 0;
    for (const node of this.nodes) handled += node.processInbox();
    this.stats.delivered += handled;
    return handled;
  }

  /** Pump until the network is quiescent. */
  pumpUntilIdle(maxRounds = 10_000): number {
    let total = 0;
    for (let round = 0; round < maxRounds; round++) {
      const handled = this.pump();
      total += handled;
      if (handled === 0) return total;
    }
    throw new Error('network did not quiesce (message storm?)');
  }

  /** Leak check: nothing sent is left unaccounted for. */
  get leakFree(): boolean {
    return (
      this.stats.sent === this.stats.delivered + this.stats.dropped &&
      this.nodes.every((n) => n.inboxLeakFree)
    );
  }

  countSent(type: MessageType): number {
    return this.stats.byType.get(type) ?? 0;
  }
}
