import { mulberry32 } from './rng.ts';
import type { GossipMessage, NodeId } from './types.ts';

/**
 * Unreliable gossip transport. Every piggybacked packet is dropped with
 * probability `dropRate` (seeded => deterministic long-tail loss).
 */
export class LossyTransport {
  private handlers = new Map<NodeId, (msg: GossipMessage) => void>();
  private rng: () => number;
  readonly dropRate: number;
  sent = 0;
  dropped = 0;
  delivered = 0;

  constructor(dropRate: number, seed: number) {
    this.dropRate = dropRate;
    this.rng = mulberry32(seed);
  }

  register(id: NodeId, handler: (msg: GossipMessage) => void): void {
    this.handlers.set(id, handler);
  }

  send(from: NodeId, to: NodeId, msg: GossipMessage): boolean {
    this.sent++;
    if (this.rng() < this.dropRate) {
      this.dropped++;
      return false;
    }
    this.delivered++;
    this.handlers.get(to)?.(msg);
    return true;
  }

  get dropRatio(): number {
    return this.sent === 0 ? 0 : this.dropped / this.sent;
  }
}
