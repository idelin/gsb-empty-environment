import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Fraction } from '../src/fraction.ts';
import {
  separate,
  verifySeparation,
  verifyCommonPoint,
} from '../src/separator.ts';

type Pt = [Fraction, Fraction];

/** Exact orientation of the triple (a, b, c): sign of the 2D cross product. */
function orient(a: Pt, b: Pt, c: Pt): number {
  const v = b[0]
    .sub(a[0])
    .mul(c[1].sub(a[1]))
    .sub(b[1].sub(a[1]).mul(c[0].sub(a[0])));
  return v.sign();
}

function onSegment(a: Pt, b: Pt, r: Pt): boolean {
  if (orient(a, b, r) !== 0) return false;
  const dot = r[0]
    .sub(a[0])
    .mul(r[0].sub(b[0]))
    .add(r[1].sub(a[1]).mul(r[1].sub(b[1])));
  return dot.sign() <= 0;
}

function segmentsIntersect(a: Pt, b: Pt, c: Pt, d: Pt): boolean {
  const o1 = orient(a, b, c);
  const o2 = orient(a, b, d);
  const o3 = orient(c, d, a);
  const o4 = orient(c, d, b);
  if (o1 * o2 < 0 && o3 * o4 < 0) return true;
  if (o1 === 0 && onSegment(a, b, c)) return true;
  if (o2 === 0 && onSegment(a, b, d)) return true;
  if (o3 === 0 && onSegment(c, d, a)) return true;
  if (o4 === 0 && onSegment(c, d, b)) return true;
  return false;
}

function inTriangle(a: Pt, b: Pt, c: Pt, r: Pt): boolean {
  if (orient(a, b, c) === 0) {
    // Degenerate triangle: r must lie on one of its edges.
    return onSegment(a, b, r) || onSegment(b, c, r) || onSegment(c, a, r);
  }
  const o1 = orient(a, b, r);
  const o2 = orient(b, c, r);
  const o3 = orient(c, a, r);
  return (o1 >= 0 && o2 >= 0 && o3 >= 0) || (o1 <= 0 && o2 <= 0 && o3 <= 0);
}

/** Independent oracle: r in conv(S), via Caratheodory (triangles/segments/points). */
function inHull(S: Pt[], r: Pt): boolean {
  for (const s of S) if (s[0].equals(r[0]) && s[1].equals(r[1])) return true;
  for (let i = 0; i < S.length; i++) {
    for (let j = i + 1; j < S.length; j++) {
      if (onSegment(S[i], S[j], r)) return true;
      for (let k = j + 1; k < S.length; k++) {
        if (inTriangle(S[i], S[j], S[k], r)) return true;
      }
    }
  }
  return false;
}

/** Independent oracle for 2D: conv(P) and conv(Q) intersect (boundary counts). */
function hullsIntersect(P: Pt[], Q: Pt[]): boolean {
  for (const p of P) if (inHull(Q, p)) return true;
  for (const q of Q) if (inHull(P, q)) return true;
  for (let i = 0; i < P.length; i++) {
    for (let j = 0; j < P.length; j++) {
      if (i === j) continue;
      for (let k = 0; k < Q.length; k++) {
        for (let l = k + 1; l < Q.length; l++) {
          if (segmentsIntersect(P[i], P[j], Q[k], Q[l])) return true;
        }
      }
    }
  }
  return false;
}

function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 0x100000000;
  };
}

test('randomized cross-check against independent 2D oracle (300 cases)', () => {
  const rand = lcg(20261003);
  const randInt = (lo: number, hi: number) => lo + Math.floor(rand() * (hi - lo + 1));
  for (let trial = 0; trial < 300; trial++) {
    const nP = randInt(1, 5);
    const nQ = randInt(1, 5);
    const range = randInt(2, 6);
    const mk = (): Pt => [Fraction.from(randInt(-range, range)), Fraction.from(randInt(-range, range))];
    const P: Pt[] = Array.from({ length: nP }, mk);
    const Q: Pt[] = Array.from({ length: nQ }, mk);
    const Pin = P.map((p) => [p[0], p[1]]);
    const Qin = Q.map((q) => [q[0], q[1]]);
    const res = separate(Pin, Qin);
    const expectedIntersect = hullsIntersect(P, Q);
    assert.equal(
      res.separable,
      !expectedIntersect,
      `trial ${trial}: P=${JSON.stringify(Pin.map(String))} Q=${JSON.stringify(Qin.map(String))}`,
    );
    if (res.separable) {
      assert.ok(verifySeparation(Pin, Qin, res.normal, res.offset), `trial ${trial}: bad separator`);
    } else {
      assert.ok(
        verifyCommonPoint(Pin, Qin, res.point, res.coeffsP, res.coeffsQ),
        `trial ${trial}: bad common point`,
      );
    }
  }
});
