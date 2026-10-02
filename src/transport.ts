import { SyncNode } from './node.ts';
import { messageBytes, type Message } from './protocol.ts';
import type { ReplicaId } from './document.ts';

/**
 * Reliable, ordered, in-memory transport. Delivers synchronously and keeps
 * traffic statistics so tests and the demo can measure packet sizes.
 * (A production transport would add retransmission on top of the same
 * message types; the CRDT merge is idempotent, so duplicates are harmless.)
 */
export class InMemoryNetwork {
  private readonly nodes = new Map<ReplicaId, SyncNode>();
  readonly stats = { messages: 0, bytes: 0 };
  /** Every message that crossed the wire, for inspection in tests/demo. */
  readonly log: Array<{ from: ReplicaId; to: ReplicaId; msg: Message; bytes: number }> = [];

  createNode(id: ReplicaId): SyncNode {
    const node = new SyncNode(id, (to, msg) => this.deliver(id, to, msg));
    this.nodes.set(id, node);
    return node;
  }

  /** Full-mesh handshake between two nodes (exchanges sync-requests). */
  connect(a: ReplicaId, b: ReplicaId): void {
    this.mustGet(a).addPeer(b);
    this.mustGet(b).addPeer(a);
  }

  resetStats(): void {
    this.stats.messages = 0;
    this.stats.bytes = 0;
    this.log.length = 0;
  }

  private deliver(from: ReplicaId, to: ReplicaId, msg: Message): void {
    const bytes = messageBytes(msg);
    this.stats.messages++;
    this.stats.bytes += bytes;
    this.log.push({ from, to, msg, bytes });
    this.mustGet(to).receive(msg);
  }

  private mustGet(id: ReplicaId): SyncNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    return node;
  }
}
