import { apply, type Mat, type Point } from './matrix.ts';

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface Circle {
  cx: number;
  cy: number;
  r: number;
}

export type ClipShape =
  | { kind: 'rect'; rect: Rect }
  | { kind: 'circle'; circle: Circle };

export interface AABB {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

/** Tolerance so boundary points survive float round-trips. Boundaries are inclusive. */
export const EPS = 1e-9;

export function rectContains(rect: Rect, p: Point): boolean {
  return (
    p.x >= rect.x - EPS &&
    p.x <= rect.x + rect.width + EPS &&
    p.y >= rect.y - EPS &&
    p.y <= rect.y + rect.height + EPS
  );
}

export function circleContains(circle: Circle, p: Point): boolean {
  const dx = p.x - circle.cx;
  const dy = p.y - circle.cy;
  const r = circle.r + EPS;
  return dx * dx + dy * dy <= r * r;
}

export function clipContains(shape: ClipShape, p: Point): boolean {
  return shape.kind === 'rect' ? rectContains(shape.rect, p) : circleContains(shape.circle, p);
}

export function aabbOfRect(rect: Rect): AABB {
  return { minX: rect.x, minY: rect.y, maxX: rect.x + rect.width, maxY: rect.y + rect.height };
}

/** Exact AABB of an affine-transformed AABB. */
export function transformAABB(m: Mat, bb: AABB): AABB {
  const p1 = apply(m, { x: bb.minX, y: bb.minY });
  const p2 = apply(m, { x: bb.maxX, y: bb.minY });
  const p3 = apply(m, { x: bb.minX, y: bb.maxY });
  const p4 = apply(m, { x: bb.maxX, y: bb.maxY });
  return {
    minX: Math.min(p1.x, p2.x, p3.x, p4.x),
    minY: Math.min(p1.y, p2.y, p3.y, p4.y),
    maxX: Math.max(p1.x, p2.x, p3.x, p4.x),
    maxY: Math.max(p1.y, p2.y, p3.y, p4.y),
  };
}

/** Exact AABB of a circle under an affine transform (the image is an ellipse). */
export function aabbOfTransformedCircle(m: Mat, circle: Circle): AABB {
  const center = apply(m, { x: circle.cx, y: circle.cy });
  // Row lengths of the linear part: x' = a*x + c*y, y' = b*x + d*y.
  const ex = circle.r * Math.hypot(m.a, m.c);
  const ey = circle.r * Math.hypot(m.b, m.d);
  return { minX: center.x - ex, minY: center.y - ey, maxX: center.x + ex, maxY: center.y + ey };
}

export function aabbOfClip(m: Mat, shape: ClipShape): AABB {
  return shape.kind === 'rect'
    ? transformAABB(m, aabbOfRect(shape.rect))
    : aabbOfTransformedCircle(m, shape.circle);
}

export function intersectAABB(a: AABB, b: AABB): AABB | null {
  const minX = Math.max(a.minX, b.minX);
  const minY = Math.max(a.minY, b.minY);
  const maxX = Math.min(a.maxX, b.maxX);
  const maxY = Math.min(a.maxY, b.maxY);
  if (minX > maxX + EPS || minY > maxY + EPS) return null;
  return {
    minX,
    minY,
    maxX: Math.max(minX, maxX),
    maxY: Math.max(minY, maxY),
  };
}

export function aabbContains(bb: AABB, p: Point): boolean {
  return p.x >= bb.minX - EPS && p.x <= bb.maxX + EPS && p.y >= bb.minY - EPS && p.y <= bb.maxY + EPS;
}
