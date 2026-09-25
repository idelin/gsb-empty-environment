import type { Grid } from './grid.ts';
import type { Coord, Rect } from './types.ts';

export interface Selection {
  anchor: Coord;
  focus: Coord;
}

export function normalizeRect(a: Coord, b: Coord): Rect {
  return {
    top: Math.min(a.row, b.row),
    left: Math.min(a.col, b.col),
    bottom: Math.max(a.row, b.row),
    right: Math.max(a.col, b.col),
  };
}

export function intersects(a: Rect, b: Rect): boolean {
  return a.top <= b.bottom && b.top <= a.bottom && a.left <= b.right && b.left <= a.right;
}

export function containsRect(outer: Rect, inner: Rect): boolean {
  return (
    outer.top <= inner.top &&
    outer.left <= inner.left &&
    outer.bottom >= inner.bottom &&
    outer.right >= inner.right
  );
}

export function unionRect(a: Rect, b: Rect): Rect {
  return {
    top: Math.min(a.top, b.top),
    left: Math.min(a.left, b.left),
    bottom: Math.max(a.bottom, b.bottom),
    right: Math.max(a.right, b.right),
  };
}

export function clampRect(rect: Rect, bounds: Rect): Rect {
  return {
    top: Math.max(rect.top, bounds.top),
    left: Math.max(rect.left, bounds.left),
    bottom: Math.min(rect.bottom, bounds.bottom),
    right: Math.min(rect.right, bounds.right),
  };
}

/**
 * Expand a rectangle to a fixed point over merged regions: any merged region
 * that intersects the selection but is not fully inside it pulls the
 * rectangle out to cover the whole region. Each iteration strictly grows the
 * rectangle and the grid bounds it, so chained merges still terminate.
 */
export function expandToFixedPoint(grid: Grid, rect: Rect): Rect {
  let current = clampRect(rect, grid.bounds());
  let changed = true;
  while (changed) {
    changed = false;
    for (const region of grid.mergedRegions()) {
      if (intersects(current, region) && !containsRect(current, region)) {
        current = unionRect(current, region);
        changed = true;
      }
    }
  }
  return current;
}

/** Range selection: normalize, then grow until every touched merge is whole. */
export function selectRange(grid: Grid, selection: Selection): Rect {
  return expandToFixedPoint(grid, normalizeRect(selection.anchor, selection.focus));
}
