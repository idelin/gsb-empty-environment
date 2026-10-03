import type { Message, NodeId } from "./types.ts";

interface Receiver {
  receive(message: Message): void;
}

/**
 * Synchronous message bus with directed link failures, so both symmetric
 * partitions (two-way cut) and asymmetric partitions (one-way loss) can be
 * modelled. Delivery is instantaneous and in-order, which keeps the
 * simulation deterministic.
 */
export class Network {
  private receivers = new Map<NodeId, Receiver>();
  private blocked = new Set<string>();

  register(id: NodeId, receiver: Receiver): void {
    this.receivers.set(id, receiver);
  }

  private key(from: NodeId, to: NodeId): string {
    return `${from}->${to}`;
  }

  /** Sever the directed link from -> to. */
  blockLink(from: NodeId, to: NodeId): void {
    this.blocked.add(this.key(from, to));
  }

  healLink(from: NodeId, to: NodeId): void {
    this.blocked.delete(this.key(from, to));
  }

  connected(from: NodeId, to: NodeId): boolean {
    return from === to || !this.blocked.has(this.key(from, to));
  }

  /**
   * Symmetric partition: nodes may talk within their own group only; every
   * cross-group link is cut in both directions.
   */
  partition(groups: NodeId[][]): void {
    const groupOf = new Map<NodeId, number>();
    groups.forEach((group, index) => {
      for (const id of group) groupOf.set(id, index);
    });
    const ids = [...this.receivers.keys()];
    for (const a of ids) {
      for (const b of ids) {
        if (a === b) continue;
        if (groupOf.get(a) !== groupOf.get(b)) this.blockLink(a, b);
      }
    }
  }

  heal(): void {
    this.blocked.clear();
  }

  send(message: Message, to: NodeId): void {
    if (!this.connected(message.from, to)) return;
    this.receivers.get(to)?.receive(message);
  }

  broadcast(message: Message, ids: NodeId[]): void {
    for (const id of ids) {
      if (id !== message.from) this.send(message, id);
    }
  }
}
