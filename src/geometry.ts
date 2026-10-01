export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Point {
  x: number;
  y: number;
}

export type Direction = 'up' | 'down' | 'left' | 'right';

export const EPS = 1e-6;

export function center(r: Rect): Point {
  return { x: r.x + r.w / 2, y: r.y + r.h / 2 };
}

export function translate(r: Rect, dx: number, dy: number): Rect {
  return { x: r.x + dx, y: r.y + dy, w: r.w, h: r.h };
}

export function intersects(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

export function containsPoint(r: Rect, p: Point): boolean {
  return p.x >= r.x && p.x <= r.x + r.w && p.y >= r.y && p.y <= r.y + r.h;
}

/**
 * Published edge-based candidacy rule (Android FocusFinder style):
 * `dst` is a candidate in `dir` from `src` when it extends past `src` on the
 * far edge and starts at-or-beyond `src`'s near edge on the main axis.
 */
export function isDirectionalCandidate(dir: Direction, src: Rect, dst: Rect): boolean {
  switch (dir) {
    case 'right':
      return (src.x < dst.x || src.x + src.w <= dst.x) && src.x + src.w < dst.x + dst.w;
    case 'left':
      return (src.x + src.w > dst.x + dst.w || src.x >= dst.x + dst.w) && src.x > dst.x;
    case 'down':
      return (src.y < dst.y || src.y + src.h <= dst.y) && src.y + src.h < dst.y + dst.h;
    case 'up':
      return (src.y + src.h > dst.y + dst.h || src.y >= dst.y + dst.h) && src.y > dst.y;
  }
}

/** Signed displacement of `to` from `from` along the main axis of `dir`. Positive means "in the direction". */
export function mainAxisDelta(dir: Direction, from: Point, to: Point): number {
  switch (dir) {
    case 'right': return to.x - from.x;
    case 'left': return from.x - to.x;
    case 'down': return to.y - from.y;
    case 'up': return from.y - to.y;
  }
}

/** Absolute displacement of `to` from `from` on the axis perpendicular to `dir`. */
export function crossAxisDelta(dir: Direction, from: Point, to: Point): number {
  switch (dir) {
    case 'right':
    case 'left':
      return Math.abs(to.y - from.y);
    case 'up':
    case 'down':
      return Math.abs(to.x - from.x);
  }
}

/**
 * Delta to add to a container's scroll offset so that `r` becomes fully
 * visible inside viewport `vp`. Content moves opposite to the offset.
 */
export function scrollDeltaToReveal(vp: Rect, r: Rect): Point {
  let dx = 0;
  let dy = 0;
  if (r.w <= vp.w) {
    if (r.x < vp.x) dx = r.x - vp.x;
    else if (r.x + r.w > vp.x + vp.w) dx = r.x + r.w - (vp.x + vp.w);
  } else {
    dx = r.x - vp.x;
  }
  if (r.h <= vp.h) {
    if (r.y < vp.y) dy = r.y - vp.y;
    else if (r.y + r.h > vp.y + vp.h) dy = r.y + r.h - (vp.y + vp.h);
  } else {
    dy = r.y - vp.y;
  }
  return { x: dx, y: dy };
}
