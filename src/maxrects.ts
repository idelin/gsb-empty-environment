import { rectsIntersect, rectContainsRect, type Rect } from './geometry.ts';

export interface PlacedRect extends Rect {
  rotated: boolean;
}

/**
 * Deterministic MAXRECTS bin for one page, operating purely in footprint
 * space. Obstacles (fixed items, kept placements) are registered up front.
 */
export class PagePacker {
  readonly width: number;
  readonly height: number;
  freeRects: Rect[];
  usedRects: Rect[] = [];

  constructor(width: number, height: number) {
    this.width = width;
    this.height = height;
    this.freeRects = [{ x: 0, y: 0, w: width, h: height }];
  }

  clone(): PagePacker {
    const copy = new PagePacker(this.width, this.height);
    copy.freeRects = this.freeRects.map((r) => ({ ...r }));
    copy.usedRects = this.usedRects.map((r) => ({ ...r }));
    return copy;
  }

  /** Register an occupied rectangle (fixed item or kept placement). */
  addObstacle(rect: Rect): void {
    this.splitFreeRects(rect);
    this.pruneContained();
    this.usedRects.push({ ...rect });
  }

  /** Can a w x h footprint (optionally rotated) fit anywhere on this page? */
  canFit(w: number, h: number, allowRotate: boolean): boolean {
    return this.findBest(w, h, allowRotate) !== null;
  }

  /**
   * Best-short-side-fit placement. Deterministic: ties broken by
   * (short fit, long fit, free-rect y, free-rect x, no-rotate first).
   */
  place(w: number, h: number, allowRotate: boolean): PlacedRect | null {
    const best = this.findBest(w, h, allowRotate);
    if (!best) return null;
    const placed: PlacedRect = {
      x: best.x,
      y: best.y,
      w: best.w,
      h: best.h,
      rotated: best.rotated,
    };
    this.splitFreeRects(placed);
    this.pruneContained();
    this.usedRects.push({ x: placed.x, y: placed.y, w: placed.w, h: placed.h });
    return placed;
  }

  /** Occupy an exact rectangle (used by the exact search and updates). */
  placeExact(rect: Rect, rotated: boolean): PlacedRect {
    const placed: PlacedRect = { ...rect, rotated };
    this.splitFreeRects(placed);
    this.pruneContained();
    this.usedRects.push({ x: placed.x, y: placed.y, w: placed.w, h: placed.h });
    return placed;
  }

  private findBest(
    w: number,
    h: number,
    allowRotate: boolean,
  ): PlacedRect | null {
    let best: (PlacedRect & { s1: number; s2: number }) | null = null;
    for (const free of this.freeRects) {
      const options: { w: number; h: number; rotated: boolean }[] = [];
      if (w <= free.w && h <= free.h) options.push({ w, h, rotated: false });
      if (allowRotate && h <= free.w && w <= free.h && (w !== h || !options.length)) {
        options.push({ w: h, h: w, rotated: true });
      }
      for (const opt of options) {
        const s1 = Math.min(free.w - opt.w, free.h - opt.h);
        const s2 = Math.max(free.w - opt.w, free.h - opt.h);
        const cand = { x: free.x, y: free.y, w: opt.w, h: opt.h, rotated: opt.rotated, s1, s2 };
        if (
          !best ||
          cand.s1 < best.s1 ||
          (cand.s1 === best.s1 &&
            (cand.s2 < best.s2 ||
              (cand.s2 === best.s2 &&
                (cand.y < best.y ||
                  (cand.y === best.y &&
                    (cand.x < best.x || (cand.x === best.x && !cand.rotated && best.rotated)))))))
        ) {
          best = cand;
        }
      }
    }
    return best;
  }

  private splitFreeRects(used: Rect): void {
    const next: Rect[] = [];
    for (const free of this.freeRects) {
      if (!rectsIntersect(free, used)) {
        next.push(free);
        continue;
      }
      // Left / right / top / bottom remnants of the free rect.
      if (used.x > free.x) {
        next.push({ x: free.x, y: free.y, w: used.x - free.x, h: free.h });
      }
      if (used.x + used.w < free.x + free.w) {
        next.push({
          x: used.x + used.w,
          y: free.y,
          w: free.x + free.w - (used.x + used.w),
          h: free.h,
        });
      }
      if (used.y > free.y) {
        next.push({ x: free.x, y: free.y, w: free.w, h: used.y - free.y });
      }
      if (used.y + used.h < free.y + free.h) {
        next.push({
          x: free.x,
          y: used.y + used.h,
          w: free.w,
          h: free.y + free.h - (used.y + used.h),
        });
      }
    }
    this.freeRects = next.filter((r) => r.w > 0 && r.h > 0);
  }

  private pruneContained(): void {
    const kept: Rect[] = [];
    for (let i = 0; i < this.freeRects.length; i++) {
      const a = this.freeRects[i]!;
      let contained = false;
      for (let j = 0; j < this.freeRects.length; j++) {
        if (i === j) continue;
        const b = this.freeRects[j]!;
        // Identical duplicates: keep only the first occurrence.
        if (rectContainsRect(b, a) && (!rectContainsRect(a, b) || j < i)) {
          contained = true;
          break;
        }
      }
      if (!contained) kept.push(a);
    }
    this.freeRects = kept;
  }
}
