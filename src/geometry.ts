export interface Vec {
  x: number;
  y: number;
}

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/**
 * Strict area overlap. Touching edges or corners does NOT count as overlap:
 * the interiors must intersect with positive area.
 */
export function rectsOverlap(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

/**
 * A rect is inside the canvas when fully contained; lying exactly on the
 * canvas boundary counts as inside.
 */
export function rectInsideCanvas(r: Rect, canvas: Rect): boolean {
  return (
    r.x >= canvas.x &&
    r.y >= canvas.y &&
    r.x + r.w <= canvas.x + canvas.w &&
    r.y + r.h <= canvas.y + canvas.h
  );
}

/** Euclidean distance from a point to the nearest point of a rect (0 when inside). */
export function distancePointToRect(p: Vec, r: Rect): number {
  const dx = Math.max(r.x - p.x, 0, p.x - (r.x + r.w));
  const dy = Math.max(r.y - p.y, 0, p.y - (r.y + r.h));
  return Math.hypot(dx, dy);
}
