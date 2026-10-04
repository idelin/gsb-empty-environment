/**
 * Structuring element: a set of offsets plus an explicit origin.
 * Offsets are absolute pixel displacements; the sampled position for an
 * output pixel p is `p + offset - origin`. Keeping the origin separate lets
 * each stage place the SE anchor independently (stage-level requirement).
 */
export class StructuringElement {
  readonly offsets: ReadonlyArray<readonly [number, number]>;
  readonly origin: readonly [number, number];
  /** max |dx|, max |dy| of (offset - origin); halo needed around a dirty region. */
  readonly reachX: number;
  readonly reachY: number;

  constructor(offsets: ReadonlyArray<readonly [number, number]>, origin: readonly [number, number] = [0, 0]) {
    if (offsets.length === 0) throw new Error('structuring element needs at least one offset');
    this.offsets = offsets.map(([dx, dy]) => [dx, dy] as const);
    this.origin = [origin[0], origin[1]] as const;
    let rx = 0;
    let ry = 0;
    for (const [dx, dy] of this.offsets) {
      rx = Math.max(rx, Math.abs(dx - this.origin[0]));
      ry = Math.max(ry, Math.abs(dy - this.origin[1]));
    }
    this.reachX = rx;
    this.reachY = ry;
  }

  /** Point-reflected SE (used to relate erosion to dilation). */
  reflected(): StructuringElement {
    return new StructuringElement(
      this.offsets.map(([dx, dy]) => [-dx, -dy] as const),
      [-this.origin[0], -this.origin[1]],
    );
  }

  containsOrigin(): boolean {
    return this.offsets.some(([dx, dy]) => dx === this.origin[0] && dy === this.origin[1]);
  }

  static square(radius: number): StructuringElement {
    const offs: Array<readonly [number, number]> = [];
    for (let dy = -radius; dy <= radius; dy++)
      for (let dx = -radius; dx <= radius; dx++) offs.push([dx, dy]);
    return new StructuringElement(offs);
  }

  static cross(radius: number): StructuringElement {
    const offs: Array<readonly [number, number]> = [[0, 0]];
    for (let r = 1; r <= radius; r++) offs.push([r, 0], [-r, 0], [0, r], [0, -r]);
    return new StructuringElement(offs);
  }
}
