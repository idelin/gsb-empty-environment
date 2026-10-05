import { Rat } from "./rational.ts";

/** Closed axis-aligned rectangle: occupies [x, x+w] x [y, y+h]. */
export interface Rect {
  readonly x: Rat;
  readonly y: Rat;
  readonly w: Rat;
  readonly h: Rat;
}

/** A body: closed rectangle at t=0 moving with constant rational velocity. */
export interface Body {
  readonly id: string;
  readonly rect: Rect;
  readonly vx: Rat;
  readonly vy: Rat;
}

/** Closed axis-aligned bounding box. */
export interface Aabb {
  readonly minX: Rat;
  readonly minY: Rat;
  readonly maxX: Rat;
  readonly maxY: Rat;
}

export function rectAabb(r: Rect): Aabb {
  return { minX: r.x, minY: r.y, maxX: r.x.add(r.w), maxY: r.y.add(r.h) };
}

/**
 * Swept AABB of a body over t in [0, T]: the union of all positions,
 * itself an AABB because motion is linear and axis-aligned per axis.
 */
export function sweptAabb(body: Body, T: Rat): Aabb {
  const dx = body.vx.mul(T);
  const dy = body.vy.mul(T);
  const x0 = body.rect.x;
  const y0 = body.rect.y;
  const x1 = x0.add(body.rect.w);
  const y1 = y0.add(body.rect.h);
  return {
    minX: Rat.min(x0, x0.add(dx)),
    minY: Rat.min(y0, y0.add(dy)),
    maxX: Rat.max(x1, x1.add(dx)),
    maxY: Rat.max(y1, y1.add(dy)),
  };
}

/** Closed-box overlap test: touching edges/corners count as overlap. */
export function aabbOverlap(a: Aabb, b: Aabb): boolean {
  return (
    a.minX.lte(b.maxX) &&
    b.minX.lte(a.maxX) &&
    a.minY.lte(b.maxY) &&
    b.minY.lte(a.maxY)
  );
}
