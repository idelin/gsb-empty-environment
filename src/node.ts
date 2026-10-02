import { DeltaDocument, type Op, type ReplicaId } from './document.ts';
import type { Message } from './protocol.ts';
import { mergeVV, type VersionVector } from './versionVector.ts';

export type SendFn = (to: ReplicaId, msg: Message) => void;

/**
 * A sync peer. Wraps a DeltaDocument and tracks, for every known peer, the
 * acknowledgment watermark = the version vector that peer has confirmed it
 * holds. Outbound traffic is restricted to delta slices above that watermark.
 */
export class SyncNode {
  readonly id: ReplicaId;
  readonly doc: DeltaDocument;

  private readonly sendFn: SendFn;
  private readonly peers = new Set<ReplicaId>();
  private readonly ackWatermarks = new Map<ReplicaId, VersionVector>();

  constructor(id: ReplicaId, sendFn: SendFn) {
    this.id = id;
    this.doc = new DeltaDocument(id);
    this.sendFn = sendFn;
  }

  /** Register a peer and ask it to bring us up to date. */
  addPeer(peerId: ReplicaId): void {
    if (peerId === this.id || this.peers.has(peerId)) return;
    this.peers.add(peerId);
    this.requestSync(peerId);
  }

  private requestSync(peerId: ReplicaId): void {
    this.sendFn(peerId, {
      type: 'sync-request',
      from: this.id,
      have: this.doc.version(),
    });
  }

  receive(msg: Message): void {
    switch (msg.type) {
      case 'sync-request': {
        const isNewPeer = !this.peers.has(msg.from);
        this.peers.add(msg.from);
        this.sendFn(msg.from, {
          type: 'delta',
          from: this.id,
          base: msg.have,
          ops: this.doc.deltaSince(msg.have),
          version: this.doc.version(),
        });
        // Reciprocal handshake: ask the new peer for what we are missing.
        if (isNewPeer) this.requestSync(msg.from);
        break;
      }
      case 'delta': {
        const isNewPeer = !this.peers.has(msg.from);
        this.peers.add(msg.from);
        this.doc.merge(msg.ops);
        this.sendFn(msg.from, {
          type: 'ack',
          from: this.id,
          version: this.doc.version(),
        });
        if (isNewPeer) this.requestSync(msg.from);
        break;
      }
      case 'ack': {
        const prev = this.ackWatermarks.get(msg.from) ?? {};
        this.ackWatermarks.set(msg.from, mergeVV(prev, msg.version));
        break;
      }
    }
  }

  /**
   * Push to every peer exactly the ops it has not yet acknowledged.
   * Returns the per-peer delta messages actually sent (empty ops skipped).
   */
  broadcast(): Array<{ to: ReplicaId; ops: Op[] }> {
    const sent: Array<{ to: ReplicaId; ops: Op[] }> = [];
    for (const peer of this.peers) {
      const base = this.ackWatermarks.get(peer) ?? {};
      const ops = this.doc.deltaSince(base);
      if (ops.length === 0) continue; // nothing new for this peer
      this.sendFn(peer, {
        type: 'delta',
        from: this.id,
        base,
        ops,
        version: this.doc.version(),
      });
      sent.push({ to: peer, ops });
    }
    return sent;
  }

  // Local mutations: apply, then propagate only the fresh delta slice.
  insertAt(index: number, char: string): void {
    this.doc.insertAt(index, char);
    this.broadcast();
  }

  append(char: string): void {
    this.doc.append(char);
    this.broadcast();
  }

  deleteAt(index: number): void {
    if (this.doc.deleteAt(index)) this.broadcast();
  }

  /** The highest version vector `peerId` has acknowledged so far. */
  ackWatermark(peerId: ReplicaId): VersionVector {
    return { ...(this.ackWatermarks.get(peerId) ?? {}) };
  }
}
