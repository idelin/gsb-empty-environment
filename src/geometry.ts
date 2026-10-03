import { Rational } from "./rational.ts";

export interface Point {
  readonly x: Rational;
  readonly y: Rational;
}

export function point(x: Rational | number | string, y: Rational | number | string): Point {
  return { x: Rational.of(x), y: Rational.of(y) };
}

export function pointEq(a: Point, b: Point): boolean {
  return a.x.eq(b.x) && a.y.eq(b.y);
}

/** 2D cross product (b - o) x (c - o). */
export function cross(o: Point, b: Point, c: Point): Rational {
  return b.x.sub(o.x).mul(c.y.sub(o.y)).sub(b.y.sub(o.y).mul(c.x.sub(o.x)));
}

export function sub(a: Point, b: Point): Point {
  return { x: a.x.sub(b.x), y: a.y.sub(b.y) };
}

export function addScaled(p: Point, d: Point, t: Rational): Point {
  return { x: p.x.add(d.x.mul(t)), y: p.y.add(d.y.mul(t)) };
}

export function dot(a: Point, b: Point): Rational {
  return a.x.mul(b.x).add(a.y.mul(b.y));
}

export function dist2(a: Point, b: Point): Rational {
  const dx = a.x.sub(b.x);
  const dy = a.y.sub(b.y);
  return dx.mul(dx).add(dy.mul(dy));
}

/**
 * Monotone chain convex hull. Returns vertices in counter-clockwise order
 * with no collinear points on edges. Degenerate results: 1 point, or 2
 * endpoints of a segment.
 */
export function convexHull(points: readonly Point[]): Point[] {
  const sorted = [...points].sort((p, q) => p.x.cmp(q.x) || p.y.cmp(q.y));
  const unique: Point[] = [];
  for (const p of sorted) {
    if (unique.length === 0 || !pointEq(unique[unique.length - 1], p)) unique.push(p);
  }
  if (unique.length <= 2) return unique;

  const build = (seq: Point[]): Point[] => {
    const half: Point[] = [];
    for (const p of seq) {
      while (half.length >= 2 && cross(half[half.length - 2], half[half.length - 1], p).sign() <= 0) {
        half.pop();
      }
      half.push(p);
    }
    return half;
  };

  const lower = build(unique);
  const upper = build([...unique].reverse());
  return lower.slice(0, -1).concat(upper.slice(0, -1));
}

/** Edges of a (possibly degenerate) hull as segments [a, b]. */
export function hullSegments(hull: readonly Point[]): [Point, Point][] {
  if (hull.length === 1) return [[hull[0], hull[0]]];
  if (hull.length === 2) return [[hull[0], hull[1]]];
  const segs: [Point, Point][] = [];
  for (let i = 0; i < hull.length; i++) segs.push([hull[i], hull[(i + 1) % hull.length]]);
  return segs;
}

export interface ClosestPair {
  readonly onFirst: Point;
  readonly onSecond: Point;
  readonly dist2: Rational;
}

function clamp01(t: Rational): Rational {
  if (t.sign() < 0) return Rational.ZERO;
  if (t.cmp(Rational.ONE) > 0) return Rational.ONE;
  return t;
}

/** Closest points between segments p1q1 and p2q2 (Ericson, RTCD 5.1.9), exact. */
export function segmentSegmentClosest(p1: Point, q1: Point, p2: Point, q2: Point): ClosestPair {
  const d1 = sub(q1, p1);
  const d2 = sub(q2, p2);
  const r = sub(p1, p2);
  const a = dot(d1, d1);
  const e = dot(d2, d2);
  const f = dot(d2, r);

  let s: Rational;
  let t: Rational;
  if (a.isZero() && e.isZero()) {
    s = Rational.ZERO;
    t = Rational.ZERO;
  } else if (a.isZero()) {
    s = Rational.ZERO;
    t = clamp01(f.div(e));
  } else {
    const c = dot(d1, r);
    if (e.isZero()) {
      t = Rational.ZERO;
      s = clamp01(c.neg().div(a));
    } else {
      const b = dot(d1, d2);
      const denom = a.mul(e).sub(b.mul(b));
      s = denom.isZero() ? Rational.ZERO : clamp01(b.mul(f).sub(c.mul(e)).div(denom));
      t = b.mul(s).add(f).div(e);
      if (t.sign() < 0) {
        t = Rational.ZERO;
        s = clamp01(c.neg().div(a));
      } else if (t.cmp(Rational.ONE) > 0) {
        t = Rational.ONE;
        s = clamp01(b.sub(c).div(a));
      }
    }
  }
  const onFirst = addScaled(p1, d1, s);
  const onSecond = addScaled(p2, d2, t);
  return { onFirst, onSecond, dist2: dist2(onFirst, onSecond) };
}

/** Closest pair of points between two (possibly degenerate) convex hulls. */
export function hullsClosest(hullA: readonly Point[], hullB: readonly Point[]): ClosestPair {
  let best: ClosestPair | null = null;
  for (const [a1, a2] of hullSegments(hullA)) {
    for (const [b1, b2] of hullSegments(hullB)) {
      const cand = segmentSegmentClosest(a1, a2, b1, b2);
      if (best === null || cand.dist2.cmp(best.dist2) < 0) best = cand;
    }
  }
  return best!;
}

export interface BarycentricCertificate {
  readonly points: Point[];
  readonly weights: Rational[];
}

/**
 * Expresses p (assumed inside the hull) as a convex combination of hull
 * vertices (Carathéodory: at most 3 vertices in 2D). Exact rationals.
 */
export function barycentricInHull(p: Point, hull: readonly Point[]): BarycentricCertificate {
  if (hull.length === 1) {
    if (!pointEq(p, hull[0])) throw new Error("point not in degenerate hull");
    return { points: [hull[0]], weights: [Rational.ONE] };
  }
  if (hull.length === 2) {
    const [u, v] = hull;
    const dx = v.x.sub(u.x);
    const dy = v.y.sub(u.y);
    const t = dx.isZero() ? p.y.sub(u.y).div(dy) : p.x.sub(u.x).div(dx);
    if (t.sign() < 0 || t.cmp(Rational.ONE) > 0) throw new Error("point outside hull segment");
    const onSeg = addScaled(u, { x: dx, y: dy }, t);
    if (!pointEq(onSeg, p)) throw new Error("point not on hull segment");
    return { points: [u, v], weights: [Rational.ONE.sub(t), t] };
  }
  const v0 = hull[0];
  for (let i = 1; i + 1 < hull.length; i++) {
    const v1 = hull[i];
    const v2 = hull[i + 1];
    const dx1 = v1.x.sub(v0.x);
    const dy1 = v1.y.sub(v0.y);
    const dx2 = v2.x.sub(v0.x);
    const dy2 = v2.y.sub(v0.y);
    const det = dx1.mul(dy2).sub(dx2.mul(dy1));
    if (det.isZero()) continue;
    const rx = p.x.sub(v0.x);
    const ry = p.y.sub(v0.y);
    const s = rx.mul(dy2).sub(dx2.mul(ry)).div(det);
    const t = dx1.mul(ry).sub(rx.mul(dy1)).div(det);
    if (s.sign() >= 0 && t.sign() >= 0 && s.add(t).cmp(Rational.ONE) <= 0) {
      return {
        points: [v0, v1, v2],
        weights: [Rational.ONE.sub(s).sub(t), s, t],
      };
    }
  }
  throw new Error("point not found in any hull triangle");
}

/** Inclusive point-in-convex-hull test (handles degenerate hulls). */
export function pointInHull(p: Point, hull: readonly Point[]): boolean {
  try {
    barycentricInHull(p, hull);
    return true;
  } catch {
    return false;
  }
}

/**
 * Proper intersection point of two segments, or null when they do not cross
 * at a single point (parallel/collinear overlaps are captured by endpoint
 * containment checks in hullIntersection).
 */
export function segmentIntersection(p1: Point, q1: Point, p2: Point, q2: Point): Point | null {
  const d1 = sub(q1, p1);
  const d2 = sub(q2, p2);
  const denom = d1.x.mul(d2.y).sub(d1.y.mul(d2.x));
  if (denom.isZero()) return null;
  const r = sub(p2, p1);
  const t = r.x.mul(d2.y).sub(r.y.mul(d2.x)).div(denom);
  const sParam = r.x.mul(d1.y).sub(r.y.mul(d1.x)).div(denom);
  const inRange = (v: Rational): boolean => v.sign() >= 0 && v.cmp(Rational.ONE) <= 0;
  if (!inRange(t) || !inRange(sParam)) return null;
  return addScaled(p1, d1, t);
}

/**
 * Vertices of the convex polygon conv(hullA) ∩ conv(hullB), as a convex hull
 * (possibly degenerate: empty, one point, or a segment).
 */
export function hullIntersection(hullA: readonly Point[], hullB: readonly Point[]): Point[] {
  const candidates: Point[] = [];
  for (const p of hullA) if (pointInHull(p, hullB)) candidates.push(p);
  for (const p of hullB) if (pointInHull(p, hullA)) candidates.push(p);
  for (const [a1, a2] of hullSegments(hullA)) {
    for (const [b1, b2] of hullSegments(hullB)) {
      const hit = segmentIntersection(a1, a2, b1, b2);
      if (hit !== null) candidates.push(hit);
    }
  }
  return convexHull(candidates);
}

/** Arithmetic mean of the given points (a barycentric interior point). */
export function barycenter(points: readonly Point[]): Point {
  if (points.length === 0) throw new Error("barycenter of empty set");
  const n = Rational.of(points.length);
  let x = Rational.ZERO;
  let y = Rational.ZERO;
  for (const p of points) {
    x = x.add(p.x);
    y = y.add(p.y);
  }
  return { x: x.div(n), y: y.div(n) };
}
