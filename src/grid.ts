import { Capsule, capsuleAABB } from './geometry';

/**
 * Spatio-temporal hash grid.
 *
 * Space is partitioned into uniform cubic cells of `cellSize`; time is
 * partitioned into discrete slots. A cell key is (slot, ix, iy, iz), so
 * entities that share both a spatial neighborhood and a time slice land in
 * the same bucket and can be found with O(1) hash lookups.
 */
export class SpatioTemporalGrid {
  readonly cellSize: number;
  private cells = new Map<string, Set<number>>();
  private capsules = new Map<string, Capsule>();

  constructor(cellSize = 256) {
    this.cellSize = cellSize;
  }

  get cellCount(): number {
    return this.cells.size;
  }

  clear(): void {
    this.cells.clear();
    this.capsules.clear();
  }

  /** Insert a capsule (entity index, time slot) into every cell it overlaps. */
  insert(entityIndex: number, slot: number, capsule: Capsule): void {
    this.capsules.set(entityIndex + '@' + slot, capsule);
    for (const key of this.cellKeys(slot, capsule)) {
      let bucket = this.cells.get(key);
      if (!bucket) {
        bucket = new Set<number>();
        this.cells.set(key, bucket);
      }
      bucket.add(entityIndex);
    }
  }

  getCapsule(entityIndex: number, slot: number): Capsule | undefined {
    return this.capsules.get(entityIndex + '@' + slot);
  }

  /** Indices of all other entities sharing at least one (slot, cell) bucket. */
  query(entityIndex: number, slot: number, capsule: Capsule): Set<number> {
    const result = new Set<number>();
    for (const key of this.cellKeys(slot, capsule)) {
      const bucket = this.cells.get(key);
      if (!bucket) continue;
      for (const other of bucket) {
        if (other !== entityIndex) result.add(other);
      }
    }
    return result;
  }

  private *cellKeys(slot: number, capsule: Capsule): Generator<string> {
    const bb = capsuleAABB(capsule);
    const cs = this.cellSize;
    const x0 = Math.floor(bb.min.x / cs);
    const y0 = Math.floor(bb.min.y / cs);
    const z0 = Math.floor(bb.min.z / cs);
    const x1 = Math.floor(bb.max.x / cs);
    const y1 = Math.floor(bb.max.y / cs);
    const z1 = Math.floor(bb.max.z / cs);
    for (let x = x0; x <= x1; x++) {
      for (let y = y0; y <= y1; y++) {
        for (let z = z0; z <= z1; z++) {
          yield slot + '|' + x + ',' + y + ',' + z;
        }
      }
    }
  }
}
