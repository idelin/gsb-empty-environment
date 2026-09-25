import {
  containsPos,
  rectOfRegion,
  type MergeRegion,
  type Pos,
  type Rect,
} from "./types.ts";

/**
 * Index over the merged regions of a grid. Answers the two questions
 * everything else depends on: which region (if any) covers a cell, and
 * where the value-holding anchor of that region is.
 */
export class MergeMap {
  readonly regions: MergeRegion[];
  private readonly byCell = new Map<string, MergeRegion>();

  constructor(regions: MergeRegion[]) {
    this.regions = [...regions];
    for (const region of this.regions) {
      const rect = rectOfRegion(region);
      for (let row = rect.top; row <= rect.bottom; row++) {
        for (let col = rect.left; col <= rect.right; col++) {
          const key = `${row}:${col}`;
          const existing = this.byCell.get(key);
          if (existing && existing !== region) {
            throw new Error(`overlapping merge regions at ${key}`);
          }
          this.byCell.set(key, region);
        }
      }
    }
  }

  /** Region containing `pos`, or undefined for a plain cell. */
  regionAt(pos: Pos): MergeRegion | undefined {
    return this.byCell.get(`${pos.row}:${pos.col}`);
  }

  /** True when `pos` sits inside a region but is not its anchor. */
  isCovered(pos: Pos): boolean {
    const region = this.regionAt(pos);
    return (
      region !== undefined &&
      (region.anchor.row !== pos.row || region.anchor.col !== pos.col)
    );
  }

  /** True when `pos` is the value-holding anchor of a region. */
  isAnchor(pos: Pos): boolean {
    const region = this.regionAt(pos);
    return (
      region !== undefined &&
      region.anchor.row === pos.row &&
      region.anchor.col === pos.col
    );
  }

  /**
   * Canonical landing cell for `pos`: the region anchor when `pos` is
   * inside a merge, otherwise `pos` itself. Never a covered cell.
   */
  resolveAnchor(pos: Pos): Pos {
    const region = this.regionAt(pos);
    return region ? { ...region.anchor } : { ...pos };
  }

  /** Bounds of the region at `pos`, or the 1x1 rect of `pos`. */
  boundsAt(pos: Pos): Rect {
    const region = this.regionAt(pos);
    return region
      ? rectOfRegion(region)
      : { top: pos.row, left: pos.col, bottom: pos.row, right: pos.col };
  }

  /** All regions intersecting `rect`. */
  regionsIntersecting(rect: Rect): MergeRegion[] {
    return this.regions.filter((region) => {
      const r = rectOfRegion(region);
      return (
        r.left <= rect.right &&
        rect.left <= r.right &&
        r.top <= rect.bottom &&
        rect.top <= r.bottom
      );
    });
  }

  /** Region lookup by rect membership used by clipboard export. */
  regionContaining(pos: Pos): MergeRegion | undefined {
    const region = this.regionAt(pos);
    return region && containsPos(rectOfRegion(region), pos) ? region : undefined;
  }
}
