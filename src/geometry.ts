import { Canvas, Point, Rect } from "./types";

/**
 * Strict interior overlap test. Touching edges or corners does NOT count
 * as overlapping: two rects conflict only if their interiors intersect.
 */
export function rectsOverlap(a: Rect, b: Rect): boolean {
  return (
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height
  );
}

/** A rect is placeable when fully inside the canvas; boundary contact is ok. */
export function rectInCanvas(rect: Rect, canvas: Canvas): boolean {
  return (
    rect.x >= 0 &&
    rect.y >= 0 &&
    rect.x + rect.width <= canvas.width &&
    rect.y + rect.height <= canvas.height
  );
}

/** Euclidean distance from a point to the nearest point of a rect (0 if inside). */
export function displacement(anchor: Point, rect: Rect): number {
  const dx = Math.max(rect.x - anchor.x, 0, anchor.x - (rect.x + rect.width));
  const dy = Math.max(rect.y - anchor.y, 0, anchor.y - (rect.y + rect.height));
  return Math.hypot(dx, dy);
}
