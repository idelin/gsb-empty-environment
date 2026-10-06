/**
 * Axis-aligned rectangles with half-open bounds: [x0, x1) x [y0, y1).
 * Coordinates are integers; a rect is empty when x1 <= x0 or y1 <= y0.
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

export function rectArea(r: Rect): number {
  return isEmpty(r) ? 0 : (r.x1 - r.x0) * (r.y1 - r.y0);
}

export function rectEquals(a: Rect, b: Rect): boolean {
  return a.x0 === b.x0 && a.y0 === b.y0 && a.x1 === b.x1 && a.y1 === b.y1;
}

export function containsPoint(r: Rect, x: number, y: number): boolean {
  return r.x0 <= x && x < r.x1 && r.y0 <= y && y < r.y1;
}

export function intersect(a: Rect, b: Rect): Rect | null {
  const r = rect(
    Math.max(a.x0, b.x0),
    Math.max(a.y0, b.y0),
    Math.min(a.x1, b.x1),
    Math.min(a.y1, b.y1),
  );
  return isEmpty(r) ? null : r;
}

/**
 * Subtract b from a, returning 0..4 pairwise-disjoint rectangles whose
 * union is exactly a \ b.
 */
export function subtractRect(a: Rect, b: Rect): Rect[] {
  const c = intersect(a, b);
  if (c === null) return isEmpty(a) ? [] : [a];
  const out: Rect[] = [];
  // Top and bottom strips span the full width of a.
  if (c.y0 > a.y0) out.push(rect(a.x0, a.y0, a.x1, c.y0));
  if (c.y1 < a.y1) out.push(rect(a.x0, c.y1, a.x1, a.y1));
  // Left and right strips fill the middle band.
  if (c.x0 > a.x0) out.push(rect(a.x0, c.y0, c.x0, c.y1));
  if (c.x1 < a.x1) out.push(rect(c.x1, c.y0, a.x1, c.y1));
  return out;
}

/** Canonical total order: lexicographic on (x0, y0, x1, y1). */
export function compareRect(a: Rect, b: Rect): number {
  if (a.x0 !== b.x0) return a.x0 - b.x0;
  if (a.y0 !== b.y0) return a.y0 - b.y0;
  if (a.x1 !== b.x1) return a.x1 - b.x1;
  return a.y1 - b.y1;
}

export function sortRects(rs: Rect[]): Rect[] {
  return rs.slice().sort(compareRect);
}

/**
 * Part 1. Given opaque rectangles in layer order (index 0 is the bottom
 * layer), return for each rectangle the pairwise-disjoint rectangular
 * fragments not covered by any rectangle above it. Fragments of each
 * rectangle are sorted in canonical order. Rectangles fully covered by
 * upper layers yield an empty fragment list.
 */
export function visibleFragments(rects: readonly Rect[]): Rect[][] {
  return rects.map((r, i) => {
    if (isEmpty(r)) return [];
    let frags: Rect[] = [r];
    for (let j = i + 1; j < rects.length && frags.length > 0; j++) {
      if (isEmpty(rects[j])) continue;
      const next: Rect[] = [];
      for (const f of frags) next.push(...subtractRect(f, rects[j]));
      frags = next;
    }
    return sortRects(frags);
  });
}
