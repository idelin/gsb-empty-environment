import { test } from "node:test";
import assert from "node:assert/strict";
import {
  checkCertificate,
  formatEquation,
  sideValue,
  verifySeparability,
} from "../src/separate.ts";
import { point } from "../src/geometry.ts";
import type { Point } from "../src/geometry.ts";
import { Rational } from "../src/rational.ts";

function assertSeparates(result: ReturnType<typeof verifySeparability>, A: Point[], B: Point[]): void {
  assert.ok(result.separable, "expected separable");
  for (const p of A) assert.ok(sideValue(p, result.hyperplane).sign() > 0, `A point (${p.x},${p.y}) not strictly positive`);
  for (const p of B) assert.ok(sideValue(p, result.hyperplane).sign() < 0, `B point (${p.x},${p.y}) not strictly negative`);
}

test("separated axis-aligned triangles", () => {
  const A = [point(0, 0), point(2, 0), point(0, 2)];
  const B = [point(4, 0), point(6, 0), point(4, 2)];
  const result = verifySeparability(A, B);
  assertSeparates(result, A, B);
  assert.ok(result.separable);
  assert.equal(result.hyperplane.equation, "x - 3 = 0");
});

test("spec-style equation shape: 2x - 3y + 5 = 0", () => {
  assert.equal(formatEquation(Rational.of(2), Rational.of(-3), Rational.of(5)), "2x - 3y + 5 = 0");
  assert.equal(formatEquation(Rational.of("4/2"), Rational.of("-6/2"), Rational.of("10/2")), "2x - 3y + 5 = 0");
  assert.equal(formatEquation(Rational.of(-2), Rational.of(3), Rational.of(-5)), "2x - 3y + 5 = 0");
  assert.equal(formatEquation(Rational.of(1), Rational.of(-1), Rational.of(0)), "x - y = 0");
  assert.equal(formatEquation(Rational.of(0), Rational.of("1/2"), Rational.of("-3/2")), "y - 3 = 0");
});

test("diagonal separation with exact rational normal", () => {
  const A = [point(0, 0), point(1, 0), point(0, 1)];
  const B = [point(3, 3), point(4, 3), point(3, 4)];
  const result = verifySeparability(A, B);
  assertSeparates(result, A, B);
  assert.ok(result.separable);
  // closest pair (1,1)? no: hulls are triangles; closest points (1,0)-(3,3)? The
  // exact closest pair is (1,1)... verify via margins instead of guessing.
  assert.ok(result.minSideA.sign() > 0);
  assert.ok(result.maxSideB.sign() < 0);
});

test("overlapping triangles: common point with barycentric certificates", () => {
  const A = [point(0, 0), point(4, 0), point(0, 4)];
  const B = [point(2, 1), point(6, 1), point(2, 5)];
  const result = verifySeparability(A, B);
  assert.ok(!result.separable, "expected non-separable");
  assert.ok(checkCertificate(result.point, result.certificateA));
  assert.ok(checkCertificate(result.point, result.certificateB));
  // certificate points must come from the original sets
  for (const p of result.certificateA.points) assert.ok(A.some((q) => q.x.eq(p.x) && q.y.eq(p.y)));
  for (const p of result.certificateB.points) assert.ok(B.some((q) => q.x.eq(p.x) && q.y.eq(p.y)));
});

test("overlap region and its barycenter for overlapping triangles", () => {
  const A = [point(0, 0), point(4, 0), point(0, 4)];
  const B = [point(2, 1), point(6, 1), point(2, 5)];
  const result = verifySeparability(A, B);
  assert.ok(!result.separable);
  // overlap is the triangle (2,1), (3,1), (2,2)
  assert.deepEqual(
    result.overlap.map((p) => [p.x.toString(), p.y.toString()]),
    [["2", "1"], ["3", "1"], ["2", "2"]],
  );
  // barycenter (7/3, 4/3) lies strictly inside the overlap
  assert.equal(result.point.x.toString(), "7/3");
  assert.equal(result.point.y.toString(), "4/3");
  assert.ok(checkCertificate(result.point, result.certificateA));
  assert.ok(checkCertificate(result.point, result.certificateB));
});

test("touching hulls (shared vertex) are not strictly separable", () => {
  const A = [point(0, 0), point(1, 0), point(0, 1)];
  const B = [point(1, 0), point(2, 0), point(1, 1)];
  const result = verifySeparability(A, B);
  assert.ok(!result.separable);
  assert.equal(result.point.x.toString(), "1");
  assert.equal(result.point.y.toString(), "0");
  assert.ok(checkCertificate(result.point, result.certificateA));
  assert.ok(checkCertificate(result.point, result.certificateB));
});

test("containment: one hull inside the other", () => {
  const A = [point(0, 0), point(10, 0), point(0, 10)];
  const B = [point(2, 2), point(3, 2), point(2, 3)];
  const result = verifySeparability(A, B);
  assert.ok(!result.separable);
  assert.ok(checkCertificate(result.point, result.certificateA));
  assert.ok(checkCertificate(result.point, result.certificateB));
});

test("degenerate sets: point vs segment, separated and touching", () => {
  const sep = verifySeparability([point(0, 0)], [point(2, 0), point(2, 4)]);
  assert.ok(sep.separable);
  assert.equal(sep.hyperplane.equation, "x - 1 = 0");

  const touch = verifySeparability([point(2, 2)], [point(2, 0), point(2, 4)]);
  assert.ok(!touch.separable);
  assert.equal(touch.point.x.toString(), "2");
  assert.equal(touch.point.y.toString(), "2");
});

test("rational coordinates: fractional separation", () => {
  const A = [point("1/2", 0), point(0, "1/2"), point(0, 0)];
  const B = [point(1, 0), point(0, 1), point(1, 1)];
  const result = verifySeparability(A, B);
  assertSeparates(result, A, B);
  assert.ok(result.separable);
  // closest points (1/2,0)-(1,0) and (0,1/2)-(0,1): distance 1/2
  assert.equal(result.minSideA.toString(), "1/16");
});

test("randomized self-check: certificates and hyperplanes always verify", () => {
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  for (let trial = 0; trial < 200; trial++) {
    const makeSet = (): Point[] => {
      const n = 1 + Math.floor(rand() * 6);
      const pts: Point[] = [];
      for (let i = 0; i < n; i++) {
        pts.push(point(Math.floor(rand() * 20) - 10, Math.floor(rand() * 20) - 10));
      }
      return pts;
    };
    const A = makeSet();
    const B = makeSet();
    const result = verifySeparability(A, B);
    if (result.separable) {
      for (const p of A) assert.ok(sideValue(p, result.hyperplane).sign() > 0);
      for (const p of B) assert.ok(sideValue(p, result.hyperplane).sign() < 0);
    } else {
      assert.ok(checkCertificate(result.point, result.certificateA));
      assert.ok(checkCertificate(result.point, result.certificateB));
    }
  }
});
