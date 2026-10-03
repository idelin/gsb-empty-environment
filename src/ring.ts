import type { HashFn } from "./hashes.ts";
import { encodeKey } from "./hashes.ts";

/**
 * Consistent-hash ring over the uint32 space. Each physical node owns
 * `vnodesPerNode` virtual points placed by hashing "<node>#<i>" with the
 * hash function under test, so ring quality reflects that function.
 */
export class ConsistentHashRing {
  readonly nodeIds: string[];
  readonly positions: Uint32Array;
  readonly owners: Uint32Array;
  /** Points that collided onto an already-occupied ring position. */
  readonly collisions: number;

  constructor(nodeIds: string[], vnodesPerNode: number, hash: HashFn) {
    if (nodeIds.length === 0) throw new RangeError("ring needs >= 1 node");
    this.nodeIds = nodeIds.slice();
    const total = nodeIds.length * vnodesPerNode;
    const pos = new Uint32Array(total);
    const own = new Uint32Array(total);
    let n = 0;
    for (let node = 0; node < nodeIds.length; node++) {
      for (let v = 0; v < vnodesPerNode; v++) {
        pos[n] = hash(encodeKey(`${nodeIds[node]}#${v}`));
        own[n] = node;
        n++;
      }
    }
    const order = Array.from({ length: total }, (_, i) => i).sort(
      (a, b) => pos[a] - pos[b],
    );
    this.positions = new Uint32Array(total);
    this.owners = new Uint32Array(total);
    for (let i = 0; i < total; i++) {
      this.positions[i] = pos[order[i]];
      this.owners[i] = own[order[i]];
    }
    let collisions = 0;
    for (let i = 1; i < total; i++) {
      if (this.positions[i] === this.positions[i - 1]) collisions++;
    }
    this.collisions = collisions;
  }

  /** Index of the node owning hash value `h` (first point clockwise). */
  lookup(h: number): number {
    const pos = this.positions;
    let lo = 0;
    let hi = pos.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (pos[mid] < h) lo = mid + 1;
      else hi = mid;
    }
    if (lo === pos.length) lo = 0; // wrap around the ring
    return this.owners[lo];
  }
}
