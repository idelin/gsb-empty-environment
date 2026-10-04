// Continuous collision detection for axis-aligned rectangles moving with
// constant rational velocity over the closed time interval [0, T].
//
// Narrow phase: exact per-axis rational inequalities (no sampling).
// Broad phase: swept AABB + spatial hash to produce candidate pairs.

import { Fraction, F, floorDiv } from './fraction.js';
import type { FractionInput } from './fraction.js';

export interface Body {
  id: string;
  x: Fraction;   // min corner x at t = 0
  y: Fraction;   // min corner y at t = 0
  w: Fraction;   // width  (>= 0)
  h: Fraction;   // height (>= 0)
  vx: Fraction;  // constant velocity x
  vy: Fraction;  // constant velocity y
}

export interface Aabb {
  minX: Fraction;
  minY: Fraction;
  maxX: Fraction;
  maxY: Fraction;
}

export interface ContactInterval {
  a: string;
  b: string;
  start: Fraction;  // closed interval [start, end] inside [0, T]
  end: Fraction;
}

// Intersect the solution set of "k * t <= c" with the interval [lo, hi] .
// Returns null when infeasible. Handles k == 0 (zero relative velocity).
function applyBound (k: Fraction, c: Fraction, lo: Fraction, hi: Fraction) {
  const s = k.sign();
  if (s === 0) {
    // 0 * t <= c  holds for all t iff c >= 0, otherwise never.
    if (c.sign() >= 0) return [lo, hi];
    return null;
  }
  const bound = c.div(k);
  if (s > 0) {
    hi = min2(hi, bound);
  } else {
    lo = max2(lo, bound);
  }
  if (lo.cmp(hi) > 0) return null;
  return [lo, hi];
}

function min2 (a: Fraction, b: Fraction) {
  if (a.cmp(b) <= 0) return a;
  return b;
}

function max2 (a: Fraction, b: Fraction) {
  if (a.cmp(b) >= 0) return a;
  return b;
}

// Exact contact interval of two bodies over [0, T].
// Contact (touching) counts as collision; initial overlap starts at 0.
// Returns null when the pair never collides in [0, T].
export function pairContactInterval (a: Body, b: Body, T: FractionInput) {
  let lo = Fraction.zero();
  let hi = F(T);
  // x axis:
  //   (a.x + a.vx t) <= (b.x + b.w + b.vx t)   <=>   (a.vx - b.vx) t <= b.x + b.w - a.x
  //   (b.x + b.vx t) <= (a.x + a.w + a.vx t)   <=>   (b.vx - a.vx) t <= a.x + a.w - b.x
  let r = applyBound(a.vx.sub(b.vx), b.x.add(b.w).sub(a.x), lo, hi);
  if (r === null) return null;
  lo = r[0]; hi = r[1];
  r = applyBound(b.vx.sub(a.vx), a.x.add(a.w).sub(b.x), lo, hi);
  if (r === null) return null;
  lo = r[0]; hi = r[1];
  // y axis (same derivation with y/h|vy)
  r = applyBound(a.vy.sub(b.vy), b.y.add(b.h).sub(a.y), lo, hi);
  if (r === null) return null;
  lo = r[0]; hi = r[1];
  r = applyBound(b.vy.sub(a.vy), a.y.add(a.h).sub(b.y), lo, hi);
  if (r === null) return null;
  lo = r[0]; hi = r[1];
  return [lo, hi];
}

// Swept AABB over [0, T]: the rectangle's trajectory bounding box.
export function sweptAabb (body: Body, T: FractionInput) {
  const t = F(T);
  const x1 = body.x;
  const x2 = body.x.add(body.vx.mul(t));
  const y1 = body.y;
  const y2 = body.y.add(body.vy.mul(t));
  return {
    minX: min2(x1, x2),
    minY: min2(y1, y2),
    maxX: max2(x1, x2).add(body.w),
    maxY: max2(y1, y2).add(body.h)
  };
}

function aabbIntersects (a: Aabb, b: Aabb) {
  return a.minX.cmp(b.maxX) <= 0 && b.minX.cmp(a.maxX) <= 0
    && a.minY.cmp(b.maxY) <= 0 && b.minY.cmp(a.maxY) <= 0;
}

export interface BroadPhaseResult {
  pairs: [Body, Body][];   // unique candidate pairs (each pair listed once)
  cellCount: number;       // number of occupied grid cells
  cellSize: Fraction;
}

// Spatial hash over swept AABBs. A body crossing many cells is inserted into
// every overlapped cell; pairs are deduplicated so a pair sharing several
// cells is reported only once.
export function broadPhase (bodies: Body[], T: FractionInput, cellSize: FractionInput) {
  const cs = F(cellSize);
  if (cs.sign() <= 0) throw new Error('cellSize must be positive');
  const grid = new Map<string, number[]>();
  const aabbs = bodies.map(b => sweptAabb(b, T));
  for (let i = 0; i < bodies.length; i++) {
    const box = aabbs[i];
    const cx0 = floorDiv(box.minX, cs);
    const cx1 = floorDiv(box.maxX, cs);
    const cy0 = floorDiv(box.minY, cs);
    const cy1 = floorDiv(box.maxY, cs);
    for (let cx = cx0; cx <= cx1; cx++) {
      for (let cy = cy0; cy <= cy1; cy++) {
        const key = cx + ',' + cy;
        const list = grid.get(key);
        if (list === undefined) { grid.set(key, [i]); } else { list.push(i); }
      }
    }
  }
  const seen = new Set<string>();
  const pairs: [Body, Body][] = [];
  for (const list of grid.values()) {
    for (let i = 0; i < list.length; i++) {
      for (let j = i + 1; j < list.length; j++) {
        const key = list[i] + '|' + list[j];
        if (seen.has(key) === false) {
          seen.add(key);
          if (aabbIntersects(aabbs[list[i]], aabbs[list[j]])) {
            pairs.push([bodies[list[i]], bodies[list[j]]]);
          }
        }
      }
    }
  }
  return { pairs, cellCount: grid.size, cellSize: cs }
}

// Default cell size heuristic: largest swept extent, clamped to >= 1.
export function defaultCellSize (bodies: Body[], T: FractionInput) {
  let maxExtent = Fraction.zero();
  for (const body of bodies) {
    const swept = sweptAabb(body, T);
    const w = max2(swept.maxX, swept.minX).sub(swept.minX);
    const h = max2(swept.maxY, swept.minY).sub(swept.minY);
    const m = max2(w, h);
    if (maxExtent.cmp(m) < 0) maxExtent = m;
  }
  if (maxExtent.sign() <= 0) return Fraction.of(1);
  return maxExtent;
}

// Independent O(n^2) all-pairs exact check, used to verify that the
// candidate generation did not miss any colliding pair.
export function allPairsContacts (bodies: Body[], T: FractionInput) {
  const result: ContactInterval[] = [];
  for (let i = 0; i < bodies.length; i++) {
    for (let j = i + 1; j < bodies.length; j++) {
      const interval = pairContactInterval(bodies[i], bodies[j], T);
      if (interval !== null) {
        result.push({ a: bodies[i].id, b: bodies[j].id, start: interval[0], end: interval[1] });
      }
    }
  }
  return result;
}
