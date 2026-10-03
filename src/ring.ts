import { hashKey } from "./hash.ts";

interface VNode {
  hash: number;
  nodeId: string;
}

/**
 * Consistent-hash ring. Each physical node owns `weight` virtual nodes.
 * Lookup walks clockwise to the first virtual node at or past the key hash.
 */
export class HashRing {
  private vnodes: VNode[] = [];

  rebuild(weights: ReadonlyMap<string, number>): void {
    const vnodes: VNode[] = [];
    for (const [nodeId, weight] of weights) {
      for (let i = 0; i < weight; i++) {
        vnodes.push({ hash: hashKey(`${nodeId}#${i}`), nodeId });
      }
    }
    vnodes.sort((a, b) => a.hash - b.hash);
    this.vnodes = vnodes;
  }

  get size(): number {
    return this.vnodes.length;
  }

  /** Returns the owning node id, or null when the ring is empty. */
  lookup(key: string): string | null {
    const ring = this.vnodes;
    if (ring.length === 0) return null;
    const target = hashKey(key);
    let lo = 0;
    let hi = ring.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (ring[mid].hash < target) lo = mid + 1;
      else hi = mid;
    }
    return ring[lo % ring.length].nodeId;
  }
}
