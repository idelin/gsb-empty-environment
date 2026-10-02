import type { Message } from "./messages.ts";
import type { PaxosNode } from "./node.ts";

/**
 * Deterministic in-memory network. Messages are queued by send() and
 * delivered by flush(); partitions block edges between groups.
 */
export class Network {
  private queue: Message[] = [];
  private blocked = new Set<string>();
  readonly counts = new Map<string, number>();

  send(msg: Message): void {
    this.counts.set(msg.type, (this.counts.get(msg.type) ?? 0) + 1);
    if (this.blocked.has(`${msg.from}->${msg.to}`)) return;
    this.queue.push(msg);
  }

  get pending(): number {
    return this.queue.length;
  }

  resetCounts(): void {
    this.counts.clear();
  }

  count(type: Message["type"]): number {
    return this.counts.get(type) ?? 0;
  }

  partition(groups: string[][]): void {
    const groupOf = new Map<string, number>();
    groups.forEach((group, index) => {
      for (const id of group) groupOf.set(id, index);
    });
    for (const a of groupOf.keys()) {
      for (const b of groupOf.keys()) {
        if (groupOf.get(a) !== groupOf.get(b)) this.blocked.add(`${a}->${b}`);
      }
    }
  }

  heal(): void {
    this.blocked.clear();
  }

  discard(): void {
    this.queue = [];
  }

  flush(nodes: Map<string, PaxosNode>): void {
    while (this.queue.length > 0) {
      const msg = this.queue.shift()!;
      nodes.get(msg.to)?.receive(msg);
    }
  }
}
