import type { StateRecord } from "./state.ts";

/** Minimal interface the network needs to deliver gossip to a node. */
export interface GossipReceiver {
  readonly nodeId: string;
  receiveGossip(events: StateRecord[]): void;
}

export interface NetworkStats {
  sent: number;
  dropped: number;
  delivered: number;
}

/**
 * In-memory lossy network. Every gossip piggyback packet is dropped with
 * probability `dropRate` (driven by an injectable RNG for reproducibility),
 * modelling probabilistic long-tail packet loss.
 */
export class LossyNetwork {
  readonly stats: NetworkStats = { sent: 0, dropped: 0, delivered: 0 };
  private nodes = new Map<string, GossipReceiver>();
  private dropRate: number;
  private rng: () => number;

  constructor(dropRate: number, rng: () => number) {
    this.dropRate = dropRate;
    this.rng = rng;
  }

  register(node: GossipReceiver): void {
    this.nodes.set(node.nodeId, node);
  }

  peersOf(nodeId: string): string[] {
    return [...this.nodes.keys()].filter((id) => id !== nodeId);
  }

  lookup<T extends GossipReceiver>(nodeId: string): T {
    const node = this.nodes.get(nodeId);
    if (!node) throw new Error(`unknown node: ${nodeId}`);
    return node as T;
  }

  /** Returns true when the packet was delivered, false when dropped. */
  sendGossip(from: string, to: string, events: StateRecord[]): boolean {
    this.stats.sent++;
    if (this.rng() < this.dropRate) {
      this.stats.dropped++;
      return false;
    }
    this.stats.delivered++;
    this.lookup(to).receiveGossip(events);
    return true;
  }

  dropRatio(): number {
    return this.stats.sent === 0 ? 0 : this.stats.dropped / this.stats.sent;
  }
}
