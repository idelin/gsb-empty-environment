import type { Capsule } from './capsule.ts';

export interface CellCoord {
  x: number;
  y: number;
  z: number;
}

/**
 * Spatiotemporal hash grid. Space is partitioned into uniform cubic cells of
 * `cellSize` meters; time is partitioned into slots of `slotDuration` seconds.
 * A capsule inserted for slot `s` is hashed into every spatial cell its AABB
 * overlaps, giving O(1) average insert and neighbor lookup per entity.
 */
export class SpacetimeGrid {
  readonly cellSize: number;
  readonly slotDuration: number;
  private buckets = new Map<string, number[]>();

  constructor(cellSize: number, slotDuration: number) {
    if (cellSize <= 0 || slotDuration <= 0) {
      throw new Error('cellSize and slotDuration must be positive');
    }
    this.cellSize = cellSize;
    this.slotDuration = slotDuration;
  }

  slotOf(t: number): number {
    return Math.floor(t / this.slotDuration);
  }

  private key(slot: number, cx: number, cy: number, cz: number): string {
    return `${slot}:${cx},${cy},${cz}`;
  }

  private cellOf(v: number): number {
    return Math.floor(v / this.cellSize);
  }

  /** Cells overlapped by a capsule's axis-aligned bounding box. */
  *cellsOfCapsule(slot: number, capsule: Capsule): Generator<CellCoord> {
    const r = capsule.radius;
    const minX = this.cellOf(Math.min(capsule.start.x, capsule.end.x) - r);
    const maxX = this.cellOf(Math.max(capsule.start.x, capsule.end.x) + r);
    const minY = this.cellOf(Math.min(capsule.start.y, capsule.end.y) - r);
    const maxY = this.cellOf(Math.max(capsule.start.y, capsule.end.y) + r);
    const minZ = this.cellOf(Math.min(capsule.start.z, capsule.end.z) - r);
    const maxZ = this.cellOf(Math.max(capsule.start.z, capsule.end.z) + r);
    for (let cx = minX; cx <= maxX; cx++) {
      for (let cy = minY; cy <= maxY; cy++) {
        for (let cz = minZ; cz <= maxZ; cz++) {
          yield { x: cx, y: cy, z: cz };
        }
      }
    }
  }

  insert(slot: number, entityId: number, capsule: Capsule): void {
    for (const cell of this.cellsOfCapsule(slot, capsule)) {
      const k = this.key(slot, cell.x, cell.y, cell.z);
      let bucket = this.buckets.get(k);
      if (bucket === undefined) {
        bucket = [];
        this.buckets.set(k, bucket);
      }
      bucket.push(entityId);
    }
  }

  /**
   * Ids of entities sharing at least one spacetime cell with `capsule`
   * during `slot` (the querying entity itself is excluded).
   */
  queryNeighbors(slot: number, entityId: number, capsule: Capsule): number[] {
    const found = new Set<number>();
    for (const cell of this.cellsOfCapsule(slot, capsule)) {
      const bucket = this.buckets.get(this.key(slot, cell.x, cell.y, cell.z));
      if (bucket === undefined) continue;
      for (const id of bucket) {
        if (id !== entityId) found.add(id);
      }
    }
    return [...found];
  }

  clear(): void {
    this.buckets.clear();
  }

  get bucketCount(): number {
    return this.buckets.size;
  }
}
