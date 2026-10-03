import { test } from "node:test";
import assert from "node:assert/strict";
import {
  barycentricInHull,
  convexHull,
  hullsClosest,
  point,
  pointInHull,
  segmentSegmentClosest,
} from "../src/geometry.ts";
import { Rational } from "../src/rational.ts";

test("convex hull of a square with interior and collinear points", () => {
  const hull = convexHull([
    point(0, 0), point(2, 0), point(2, 2), point(0, 2),
    point(1, 1), point(1, 0), point(0, 0),
  ]);
  assert.equal(hull.length, 4);
  // counter-clockwise, starting from the lexicographically smallest point
  assert.deepEqual(
    hull.map((p) => [p.x.toString(), p.y.toString()]),
    [["0", "0"], ["2", "0"], ["2", "2"], ["0", "2"]],
  );
});

test("degenerate hulls: single point and segment", () => {
  assert.equal(convexHull([point(1, 1), point(1, 1)]).length, 1);
  const seg = convexHull([point(0, 0), point(2, 2), point(1, 1)]);
  assert.equal(seg.length, 2);
});

test("segment-segment closest points are exact rationals", () => {
  const c = segmentSegmentClosest(point(0, 0), point(2, 0), point(1, 1), point(1, 3));
  assert.equal(c.onFirst.x.toString(), "1");
  assert.equal(c.onFirst.y.toString(), "0");
  assert.equal(c.onSecond.x.toString(), "1");
  assert.equal(c.onSecond.y.toString(), "1");
  assert.equal(c.dist2.toString(), "1");
});

test("crossing segments have zero distance", () => {
  const c = segmentSegmentClosest(point(0, 0), point(2, 2), point(0, 2), point(2, 0));
  assert.ok(c.dist2.isZero());
  assert.equal(c.onFirst.x.toString(), "1");
  assert.equal(c.onFirst.y.toString(), "1");
});

test("hull distance between separated triangles", () => {
  const A = convexHull([point(0, 0), point(2, 0), point(0, 2)]);
  const B = convexHull([point(4, 0), point(6, 0), point(4, 2)]);
  const c = hullsClosest(A, B);
  assert.equal(c.dist2.toString(), "4");
  assert.equal(c.onFirst.x.toString(), "2");
  assert.equal(c.onSecond.x.toString(), "4");
});

test("barycentric decomposition inside a triangle", () => {
  const hull = convexHull([point(0, 0), point(4, 0), point(0, 4)]);
  const cert = barycentricInHull(point(1, 1), hull);
  const sum = cert.weights.reduce((s, w) => s.add(w), Rational.ZERO);
  assert.ok(sum.eq(Rational.ONE));
  const px = cert.weights.reduce((s, w, i) => s.add(w.mul(cert.points[i].x)), Rational.ZERO);
  const py = cert.weights.reduce((s, w, i) => s.add(w.mul(cert.points[i].y)), Rational.ZERO);
  assert.ok(px.eq(Rational.ONE) && py.eq(Rational.ONE));
});

test("point-in-hull handles boundary and degenerate hulls", () => {
  const tri = convexHull([point(0, 0), point(4, 0), point(0, 4)]);
  assert.ok(pointInHull(point(2, 2), tri));   // on hypotenuse
  assert.ok(pointInHull(point(1, 1), tri));
  assert.ok(!pointInHull(point(3, 3), tri));
  assert.ok(pointInHull(point(1, 1), [point(0, 0), point(2, 2)]));
  assert.ok(!pointInHull(point(1, 2), [point(0, 0), point(2, 2)]));
  assert.ok(pointInHull(point(5, 5), [point(5, 5)]));
});
