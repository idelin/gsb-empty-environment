/**
 * Spatio-temporal data cube: hour buckets x tile cells.
 *
 * Storage is a fixed ring of Float64Array layers (one per hour bucket), so
 * memory is strictly bounded by `capacityHours * cellCount * 8` bytes plus
 * a small index map. When a new hour arrives beyond capacity, the oldest
 * hour layer is evicted and its memory reused.
 */

export class SpatioTemporalCube {
  readonly cellCount: number;
  readonly capacityHours: number;
  private readonly layers: Float64Array[];
  private readonly slotOfHour = new Map<number, number>();
  private freeSlots: number[];
  private oldestHour = Number.POSITIVE_INFINITY;
  /** Stats for observability. */
  evictions = 0;
  pointsAdded = 0;

  constructor(cellCount: number, capacityHours: number) {
    if (!(cellCount > 0) || !(capacityHours > 0)) {
      throw new Error('cellCount and capacityHours must be positive');
    }
    this.cellCount = cellCount;
    this.capacityHours = capacityHours;
    this.layers = new Array<Float64Array>(capacityHours);
    this.freeSlots = [];
    for (let i = capacityHours - 1; i >= 0; i--) this.freeSlots.push(i);
  }

  /** Number of hour layers currently materialized. */
  get hoursStored(): number {
    return this.slotOfHour.size;
  }

  /** Bounded heap memory of the cube layers, in bytes. */
  get memoryBytes(): number {
    return this.capacityHours * this.cellCount * 8;
  }

  /** Oldest hour bucket currently stored (-1 when empty). */
  get minHour(): number {
    return this.slotOfHour.size === 0 ? -1 : this.oldestHour;
  }

  add(cell: number, hourBucket: number, weight: number): void {
    const slot = this.slotFor(hourBucket);
    this.layers[slot][cell] += weight;
    this.pointsAdded++;
  }

  /**
   * Accumulates per-cell sums over [startHour, endHourExclusive) into `out`
   * (length must be >= cellCount). `out` is zeroed first. Returns the number
   * of hour layers that intersected the window.
   */
  windowSums(startHour: number, endHourExclusive: number, out: Float64Array): number {
    out.fill(0);
    let matched = 0;
    for (const [hour, slot] of this.slotOfHour) {
      if (hour >= startHour && hour < endHourExclusive) {
        const layer = this.layers[slot];
        for (let i = 0; i < this.cellCount; i++) out[i] += layer[i];
        matched++;
      }
    }
    return matched;
  }

  private slotFor(hour: number): number {
    const existing = this.slotOfHour.get(hour);
    if (existing !== undefined) return existing;

    let slot: number;
    if (this.freeSlots.length > 0) {
      slot = this.freeSlots.pop()!;
      this.layers[slot] = new Float64Array(this.cellCount);
    } else {
      // Evict the oldest hour layer and reuse its buffer.
      slot = this.slotOfHour.get(this.oldestHour)!;
      this.slotOfHour.delete(this.oldestHour);
      this.layers[slot].fill(0);
      this.evictions++;
      this.recomputeOldest();
    }
    this.slotOfHour.set(hour, slot);
    if (hour < this.oldestHour) this.oldestHour = hour;
    return slot;
  }

  private recomputeOldest(): void {
    let min = Number.POSITIVE_INFINITY;
    for (const hour of this.slotOfHour.keys()) {
      if (hour < min) min = hour;
    }
    this.oldestHour = min;
  }
}
