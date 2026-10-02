import { test } from "node:test";
import assert from "node:assert/strict";
import { Fraction } from "../src/fraction.ts";
import {
  add, deriv, evalAt, isolateRoots, mul, polyToString, primitive,
  rationalRoots, removeRationalRoots, sturmSequence,
} from "../src/polynomial.ts";

const F = (s: string | number) => Fraction.from(s);

test("add/mul/deriv", () => {
  const p = [1n, 2n, 3n];       // 1 + 2t + 3t^2
  const q = [1n, -1n];          // 1 - t
  assert.deepEqual(add(p, q), [2n, 1n, 3n]);
  assert.deepEqual(mul(p, q), [1n, 1n, 1n, -3n]);
  assert.deepEqual(deriv(p), [2n, 6n]);
});

test("evalAt is exact at rational points", () => {
  const p = [1n, 2n, 3n]; // 1 + 2t + 3t^2
  assert.equal(evalAt(p, F("1/2")).toString(), "11/4");
  assert.equal(evalAt(p, F(0)).toString(), "1");
});

test("primitive divides content, keeps sign", () => {
  assert.deepEqual(primitive([4n, -8n, 4n]), [1n, -2n, 1n]);
  assert.deepEqual(primitive([-2n, 4n]), [-1n, 2n]);
});

test("rationalRoots finds roots with multiplicity", () => {
  // (2t-1)^2 (t+1) = 4t^3 + 0t^2 - 3t + 1 ... expand: (4t^2-4t+1)(t+1) = 4t^3-4t^2+t+4t^2-4t+1 = 4t^3-3t+1
  const roots = rationalRoots([1n, -3n, 0n, 4n]);
  assert.equal(roots.length, 2);
  const half = roots.find((r) => r.root.toString() === "1/2");
  const minusOne = roots.find((r) => r.root.toString() === "-1");
  assert.equal(half?.multiplicity, 2);
  assert.equal(minusOne?.multiplicity, 1);
});

test("removeRationalRoots leaves irrational factor", () => {
  // (t - 1/2)(t^2 - 2) = t^3 - t^2/2 - 2t + 1  ->  2t^3 - t^2 - 4t + 2
  const rest = removeRationalRoots([2n, -4n, -1n, 2n]);
  assert.deepEqual(rest, [-2n, 0n, 1n]); // t^2 - 2
});

test("sturm isolation isolates sqrt(2)/2 in (0,1)", () => {
  // t^2 - 1/2 -> 2t^2 - 1 ; root ~0.7071
  const ivs = isolateRoots([-1n, 0n, 2n], F(0), F(1), F("1/1024"));
  assert.equal(ivs.length, 1);
  const { lo, hi } = ivs[0];
  const s2 = Math.SQRT1_2;
  assert.ok(lo.toNumber() < s2 && s2 < hi.toNumber());
  assert.ok(hi.sub(lo).cmp(F("1/1024")) <= 0);
  // endpoints are exact rationals
  assert.equal(typeof lo.toString(), "string");
});

test("sturm isolation finds all distinct roots", () => {
  // (t^2 - 1/4)(t^2 - 1/2) -> roots 1/2 (rational, removed) ... use pure irrational:
  // (t^2 - 1/2)(t^2 - 3/4) has roots +-0.7071, +-0.8660; two in (0,1)
  // = t^4 - 5/4 t^2 + 3/8 -> 8t^4 - 10t^2 + 3
  const ivs = isolateRoots([3n, 0n, -10n, 0n, 8n], F(0), F(1), F("1/4096"));
  assert.equal(ivs.length, 2);
  assert.ok(Math.abs(ivs[0].lo.toNumber() - Math.SQRT1_2) < 1e-3);
  assert.ok(Math.abs(ivs[1].lo.toNumber() - Math.sqrt(3) / 2) < 1e-3);
});

test("sturmSequence of constant-free polynomial terminates", () => {
  const seq = sturmSequence([1n, 1n]); // t + 1
  assert.ok(seq.length >= 2);
});

test("polyToString", () => {
  assert.equal(polyToString([1n, -2n, 1n]), "t^2 - 2*t + 1");
  assert.equal(polyToString([0n]), "0");
});
