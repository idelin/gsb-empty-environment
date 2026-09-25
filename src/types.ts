/** A cell coordinate in the grid. */
export interface Pos {
  row: number;
  col: number;
}

/** An inclusive rectangle of cells. */
export interface Rect {
  top: number;
  left: number;
  bottom: number;
  right: number;
}

/**
 * A merged region: the anchor cell holds the value and spans
 * `rows` x `cols` cells. Every other cell inside the region is
 * "covered" and can never be the navigation or selection target.
 */
export interface MergeRegion {
  anchor: Pos;
  rows: number;
  cols: number;
}

export function rectOfRegion(region: MergeRegion): Rect {
  return {
    top: region.anchor.row,
    left: region.anchor.col,
    bottom: region.anchor.row + region.rows - 1,
    right: region.anchor.col + region.cols - 1,
  };
}

export function containsPos(rect: Rect, pos: Pos): boolean {
  return (
    pos.row >= rect.top &&
    pos.row <= rect.bottom &&
    pos.col >= rect.left &&
    pos.col <= rect.right
  );
}

export function rectsIntersect(a: Rect, b: Rect): boolean {
  return (
    a.left <= b.right && b.left <= a.right && a.top <= b.bottom && b.top <= a.bottom
  );
}

export function unionRects(a: Rect, b: Rect): Rect {
  return {
    top: Math.min(a.top, b.top),
    left: Math.min(a.left, b.left),
    bottom: Math.max(a.bottom, b.bottom),
    right: Math.max(a.right, b.right),
  };
}

export function rectsEqual(a: Rect, b: Rect): boolean {
  return (
    a.top === b.top && a.left === b.left && a.bottom === b.bottom && a.right === b.right
  );
}

export function rectSize(rect: Rect): { rows: number; cols: number } {
  return { rows: rect.bottom - rect.top + 1, cols: rect.right - rect.left + 1 };
}
