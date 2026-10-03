/**
 * Path clearance queries on top of the versioned distance field.
 *
 * Clearance of a point = Euclidean distance to the nearest obstacle
 * (= sqrt of the stored squared distance). Clearance of a path is the
 * minimum clearance over points sampled along every segment -- endpoints
 * alone are NOT sufficient, an obstacle may sit in the middle of a long
 * segment.
 */

import { VersionedField } from "./field.ts";
import type { Version } from "./field.ts";

export interface Vec2 { x: number; y: number }

export interface PathClearance {
  /** Minimum Euclidean clearance along the whole path (Infinity if none). */
  clearance: number;
  /** World position where the minimum was attained. */
  at: Vec2;
}

/** Clearance (Euclidean distance to nearest obstacle) at a world point. */
export function clearanceAt(field: VersionedField, v: Version, wx: number, wy: number): number {
  const cell = field.getCell(v, Math.floor(wx), Math.floor(wy));
  return Math.sqrt(cell.dist);
}

/**
 * Minimum clearance along a polyline. Every segment is sampled at
 * intervals of at most `step` world units (default 0.5, i.e. at least two
 * samples per cell), including both endpoints.
 */
export function pathClearance(
  field: VersionedField,
  v: Version,
  points: ReadonlyArray<Vec2>,
  step = 0.5,
): PathClearance {
  let best = Number.POSITIVE_INFINITY;
  let bestAt: Vec2 = points.length > 0 ? { ...points[0] } : { x: 0, y: 0 };
  const visit = (x: number, y: number): void => {
    const c = clearanceAt(field, v, x, y);
    if (c < best) {
      best = c;
      bestAt = { x, y };
    }
  };
  if (points.length === 1) visit(points[0].x, points[0].y);
  for (let i = 0; i + 1 < points.length; i++) {
    const a = points[i], b = points[i + 1];
    const dx = b.x - a.x, dy = b.y - a.y;
    const len = Math.hypot(dx, dy);
    const n = Math.max(1, Math.ceil(len / step));
    for (let k = 0; k <= n; k++) {
      const t = k / n;
      visit(a.x + dx * t, a.y + dy * t);
    }
  }
  return { clearance: best, at: bestAt };
}

/**
 * True when every sampled point of the path keeps clearance >= radius.
 * Compares squared distances to avoid square roots and exits early.
 */
export function isPathClear(
  field: VersionedField,
  v: Version,
  points: ReadonlyArray<Vec2>,
  radius: number,
  step = 0.5,
): boolean {
  const r2 = radius * radius;
  const ok = (x: number, y: number): boolean =>
    field.getCell(v, Math.floor(x), Math.floor(y)).dist >= r2;
  if (points.length === 1) return ok(points[0].x, points[0].y);
  for (let i = 0; i + 1 < points.length; i++) {
    const a = points[i], b = points[i + 1];
    const dx = b.x - a.x, dy = b.y - a.y;
    const len = Math.hypot(dx, dy);
    const n = Math.max(1, Math.ceil(len / step));
    for (let k = 0; k <= n; k++) {
      const t = k / n;
      if (!ok(a.x + dx * t, a.y + dy * t)) return false;
    }
  }
  return true;
}
