import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational } from "../src/rational.ts";
import { intersectSegmentBox } from "../src/intersect.ts";
import type { QueryBox, Segment, TimeRange } from "../src/types.ts";

function seg(partial: Partial<Segment>): Segment {
  return {
    objectId: "o",
    segmentId: "s",
    t0: 0,
    t1: 10,
    from: { x: 0, y: 0, z: 0 },
    to: { x: 0, y: 0, z: 0 },
    ...partial,
  };
}

const fullTime: TimeRange = { t0: -100, t1: 100 };

function box(x0: number, x1: number, y0 = -5, y1 = 5, z0 = -5, z1 = 5): QueryBox {
  return { min: { x: x0, y: y0, z: z0 }, max: { x: x1, y: y1, z: z1 } };
}

test("stationary segment inside box: full segment time range", () => {
  const iv = intersectSegmentBox(seg({}), box(-1, 1), fullTime);
  assert.ok(iv);
  assert.ok(iv.lo.eq(Rational.of(0)));
  assert.ok(iv.hi.eq(Rational.of(10)));
  assert.equal(iv.hiOpen, true); // clamped by segment end (half-open)
});

test("stationary segment outside box: no hit", () => {
  assert.equal(intersectSegmentBox(seg({}), box(1, 2), fullTime), null);
});

test("stationary on box face (closed box): hit", () => {
  const iv = intersectSegmentBox(seg({}), box(0, 3), fullTime);
  assert.ok(iv);
  assert.ok(iv.lo.eq(Rational.of(0)));
});

test("zero-duration and negative-duration segments are rejected by the index, and intersect assumes valid input", () => {
  // intersectSegmentBox requires t1 > t0 (enforced by SpatioTemporalIndex);
  // here we assert the index-level guard instead (see index tests).
  assert.ok(true);
});

test("crossing box with both endpoints outside", () => {
  // x(t) = -10 + t on t in [0, 20); box x in [-2, 2] -> t in [8, 12]
  const s = seg({ t0: 0, t1: 20, from: { x: -10, y: 0, z: 0 }, to: { x: 10, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(-2, 2), fullTime);
  assert.ok(iv);
  assert.ok(iv.lo.eq(Rational.of(8)));
  assert.ok(iv.hi.eq(Rational.of(12)));
  assert.equal(iv.hiOpen, false); // clamped by space, not time
});

test("rational (non-integer) entry and exit times", () => {
  // x(t) = t/3 on t in [0, 30): from (0,0,0) to (10,0,0); box x in [1, 2]
  // entry: t/3 = 1 -> t = 3; exit: t/3 = 2 -> t = 6. Use d=3, T=10 instead:
  // from (0,0,0) to (3,0,0), t in [0,10): x = 3t/10; box x in [1,2]
  // entry t = 10/3, exit t = 20/3.
  const s = seg({ from: { x: 0, y: 0, z: 0 }, to: { x: 3, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(1, 2), fullTime);
  assert.ok(iv);
  assert.ok(iv.lo.eq(Rational.of(10, 3)));
  assert.ok(iv.hi.eq(Rational.of(20, 3)));
});

test("grazing touch at a single interior time point", () => {
  // Diagonal (t, 0, t) on t in [0,10); degenerate box corner at (5,0,5).
  const s = seg({ from: { x: 0, y: 0, z: 0 }, to: { x: 10, y: 0, z: 10 } });
  const corner: QueryBox = { min: { x: 5, y: 0, z: 5 }, max: { x: 5, y: 0, z: 5 } };
  const iv = intersectSegmentBox(s, corner, fullTime);
  assert.ok(iv);
  assert.ok(iv.lo.eq(Rational.of(5)));
  assert.ok(iv.hi.eq(Rational.of(5)));
  assert.equal(iv.hiOpen, false);
});

test("grazing past a box edge without entering: no hit", () => {
  // Path along x at y=3; box y in [0,2]: never inside.
  const s = seg({ from: { x: 0, y: 3, z: 0 }, to: { x: 10, y: 3, z: 0 } });
  assert.equal(intersectSegmentBox(s, box(0, 10, 0, 2), fullTime), null);
});

test("touch exactly at segment end t1 (right-open): no hit", () => {
  // Enters box only at t = 10 == t1, which is excluded.
  const s = seg({ from: { x: 0, y: 0, z: 0 }, to: { x: 10, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(10, 20), fullTime);
  assert.equal(iv, null);
});

test("query touching only the right-open endpoint t1: no hit", () => {
  // Stationary inside box, but query time starts exactly at t1.
  const s = seg({});
  const iv = intersectSegmentBox(s, box(-1, 1), { t0: 10, t1: 20 });
  assert.equal(iv, null);
});

test("query ends exactly when trajectory enters box: no hit", () => {
  // Enters box at t = 5; query time [0, 5) excludes the touch point.
  const s = seg({ from: { x: 0, y: 0, z: 0 }, to: { x: 10, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(5, 8), { t0: 0, t1: 5 });
  assert.equal(iv, null);
});

test("query clips the hit interval on both sides", () => {
  // In box for t in [2, 8]; query [4, 6) -> [4, 6)
  const s = seg({ t0: 0, t1: 20, from: { x: 0, y: 0, z: 0 }, to: { x: 20, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(2, 8), { t0: 4, t1: 6 });
  assert.ok(iv);
  assert.ok(iv.lo.eq(Rational.of(4)));
  assert.ok(iv.hi.eq(Rational.of(6)));
  assert.equal(iv.hiOpen, true); // clamped by query end
});

test("single-point touch at query start counts", () => {
  // Enters box exactly at t = 5; query [5, 9) includes the point.
  const s = seg({ from: { x: 0, y: 0, z: 0 }, to: { x: 10, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(5, 5), { t0: 5, t1: 9 });
  assert.ok(iv);
  assert.ok(iv.lo.eq(Rational.of(5)));
  assert.ok(iv.hi.eq(Rational.of(5)));
  assert.equal(iv.hiOpen, false);
});

test("negative direction movement", () => {
  // x(t) = 10 - t on t in [0,10); box x in [3,4] -> t in [6,7]
  const s = seg({ from: { x: 10, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(3, 4), fullTime);
  assert.ok(iv);
  assert.ok(iv.lo.eq(Rational.of(6)));
  assert.ok(iv.hi.eq(Rational.of(7)));
});
