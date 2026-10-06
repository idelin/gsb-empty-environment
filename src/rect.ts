/**
 * Axis-aligned rectangles with half-open bounds: [x0, x1) x [y0, y1).
 * A point p is inside iff x0 <= p.x < x1 and y0 <= p.y < y1.
 * Coordinates are numbers; exactness of the verified algorithms relies on
 * integer coordinates (see the pixel-grid oracles in test/).
 */
export interface Rect {
  readonly x0: number;
  readonly y0: number;
  readonly x1: number;
  readonly y1: number;
}

export function rect(x0: number, y0: number, x1: number, y1: number): Rect {
  return { x0, y0, x1, y1 };
}

export function isEmpty(r: Rect): boolean {
  return r.x1 <= r.x0 || r.y1 <= r.y0;
}

export function area(r: Rect): number {
  return isEmpty(r) ? 0 : (r.x1 - r.x0) * (r.y1 - r.y0);
}

export function containsPoint(r: Rect, x: number, y: number): boolean {
  return r.x0 <= x && x < r.x1 && r.y0 <= y && y < r.y1;
}

export function intersect(a: Rect, b: Rect): Rect {
  return {
    x0: Math.max(a.x0, b.x0),
    y0: Math.max(a.y0, b.y0),
    x1: Math.min(a.x1, b.x1),
    y1: Math.min(a.y1, b.y1),
  };
}

export function overlaps(a: Rect, b: Rect): boolean {
  return !isEmpty(intersect(a, b));
}

export function equalsRect(a: Rect, b: Rect): boolean {
  return a.x0 === b.x0 && a.y0 === b.y0 && a.x1 === b.x1 && a.y1 === b.y1;
}

/**
 * Subtract occluder `b` from `a`, returning 0..4 disjoint rectangles whose
 * union is a \ b. The pieces are produced in canonical order.
 */
export function subtract(a: Rect, b: Rect): Rect[] {
  const inter = intersect(a, b);
  if (isEmpty(inter)) return isEmpty(a) ? [] : [a];
  const out: Rect[] = [];
  // top strip
  if (inter.y0 > a.y0) out.push(rect(a.x0, a.y0, a.x1, inter.y0));
  // bottom strip
  if (inter.y1 < a.y1) out.push(rect(a.x0, inter.y1, a.x1, a.y1));
  // left strip (between the horizontal cuts)
  if (inter.x0 > a.x0) out.push(rect(a.x0, inter.y0, inter.x0, inter.y1));
  // right strip
  if (inter.x1 < a.x1) out.push(rect(inter.x1, inter.y0, a.x1, inter.y1));
  return out.filter((r) => !isEmpty(r));
}

/**
 * Canonical fragment order: ascending y0, then x0, then y1, then x1
 * (reading order: top-to-bottom, left-to-right).
 */
export function compareRects(a: Rect, b: Rect): number {
  if (a.y0 !== b.y0) return a.y0 - b.y0;
  if (a.x0 !== b.x0) return a.x0 - b.x0;
  if (a.y1 !== b.y1) return a.y1 - b.y1;
  return a.x1 - b.x1;
}

export function sortCanonical(rects: Rect[]): Rect[] {
  return [...rects].sort(compareRects);
}

/** Debug helper, e.g. "[0,0 2x3)". */
export function rectToString(r: Rect): string {
  return `[${r.x0},${r.y0} ${r.x1 - r.x0}x${r.y1 - r.y0})`;
}
