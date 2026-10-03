import type { Message, NodeId } from './types.ts';

export interface Envelope {
  from: NodeId;
  to: NodeId;
  msg: Message;
}

type DeliverFn = (to: NodeId, msg: Message) => void;

/**
 * Deterministic in-memory network. Messages are queued and delivered
 * explicitly via deliverNext()/runUntilIdle(), so tests control every
 * interleaving. Supports symmetric partitions and message drops.
 */
export class Network {
  private queue: Envelope[] = [];
  private blockedPairs = new Set<string>();
  private deliverFn: DeliverFn;
  private afterDeliver: (() => void) | null = null;
  sent = 0;
  delivered = 0;
  droppedByPartition = 0;
  readonly sentByKind = new Map<string, number>();

  constructor(deliverFn: DeliverFn) {
    this.deliverFn = deliverFn;
  }

  setAfterDeliver(hook: (() => void) | null): void {
    this.afterDeliver = hook;
  }

  private pairKey(a: NodeId, b: NodeId): string {
    return a < b ? `${a}|${b}` : `${b}|${a}`;
  }

  isBlocked(a: NodeId, b: NodeId): boolean {
    return this.blockedPairs.has(this.pairKey(a, b));
  }

  send(from: NodeId, to: NodeId, msg: Message): void {
    this.sent++;
    this.sentByKind.set(msg.kind, (this.sentByKind.get(msg.kind) ?? 0) + 1);
    if (from !== to && this.isBlocked(from, to)) {
      this.droppedByPartition++;
      return;
    }
    this.queue.push({ from, to, msg });
  }

  /** Partition nodes into isolated groups; traffic across groups is dropped. */
  partition(groups: NodeId[][]): void {
    this.blockedPairs.clear();
    for (let i = 0; i < groups.length; i++) {
      for (let j = i + 1; j < groups.length; j++) {
        for (const a of groups[i]) {
          for (const b of groups[j]) {
            this.blockedPairs.add(this.pairKey(a, b));
          }
        }
      }
    }
  }

  heal(): void {
    this.blockedPairs.clear();
  }

  get pending(): number {
    return this.queue.length;
  }

  deliverNext(): boolean {
    const env = this.queue.shift();
    if (!env) return false;
    this.delivered++;
    this.deliverFn(env.to, env.msg);
    if (this.afterDeliver) this.afterDeliver();
    return true;
  }

  runUntilIdle(): void {
    while (this.deliverNext()) {
      // drain
    }
  }

  /** Drop all queued messages (simulates traffic lost before partition heal). */
  dropPending(): number {
    const n = this.queue.length;
    this.queue = [];
    return n;
  }

  dispose(): void {
    this.queue = [];
    this.blockedPairs.clear();
    this.afterDeliver = null;
  }
}
