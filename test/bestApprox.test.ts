import { test } from "node:test";
import assert from "node:assert/strict";
import {
  bestApprox,
  bestApproxExhaustive,
  bestInteger,
} from "../src/bestApprox.ts";
import {
  eq,
  make,
  neg,
  parseRational,
  toString,
  type Rational,
} from "../src/rational.ts";

const P = parseRational;

test("matches the exhaustive reference on a dense small-bound sweep", () => {
  const targets: Rational[] = [
    P("0"), P("1"), P("-1"), P("5"), P("-5"),
    P("0.5"), P("-0.5"), P("1.5"), P("-1.5"), P("2.5"), P("-2.5"),
    P("3.14159"), P("-3.14159"), P("2.71828"), P("-2.71828"),
    P("1/3"), P("-1/3"), P("22/7"), P("-22/7"), P("355/113"),
    P("0.499"), P("0.501"), P("-0.499"), P("-0.501"),
    P("1/6"), P("5/6"), P("-5/6"), P("7/24"), P("-7/24"),
  ];
  for (const x of targets) {
    for (let q = 1n; q <= 60n; q++) {
      const fast = bestApprox(x, q);
      const slow = bestApproxExhaustive(x, q);
      assert.ok(
        eq(fast.best, slow.best),
        `x=${toString(x)} Q=${q}: fast=${toString(fast.best)} slow=${toString(slow.best)}`,
      );
      assert.ok(eq(fast.error, slow.error), `error mismatch at x=${toString(x)} Q=${q}`);
    }
  }
});

test("randomized cross-check against exhaustive reference", () => {
  let seed = 123456789n;
  const rand = (mod: bigint): bigint => {
    seed = (seed * 6364136223846793005n + 1442695040888963407n) & ((1n << 64n) - 1n);
    return seed % mod;
  };
  for (let i = 0; i < 300; i++) {
    const num = rand(20000n) - 10000n;
    const den = 1n + rand(997n);
    const x = make(num, den);
    const q = 1n + rand(120n);
    const fast = bestApprox(x, q);
    const slow = bestApproxExhaustive(x, q);
    assert.ok(eq(fast.best, slow.best), `x=${toString(x)} Q=${q}`);
    assert.ok(eq(fast.error, slow.error));
  }
});

test("exactly representable targets give error 0", () => {
  for (const [s, q] of [["22/7", 7n], ["22/7", 100n], ["355/113", 113n], ["-22/7", 7n], ["5", 1n], ["0", 1n]] as const) {
    const r = bestApprox(P(s), q);
    assert.equal(r.error.num, 0n, s);
    assert.equal(toString(r.best), toString(P(s)));
  }
});

test("denominator bound 1 returns the best integer with explicit half-down rule", () => {
  assert.equal(bestInteger(P("0.4")), 0n);
  assert.equal(bestInteger(P("0.6")), 1n);
  assert.equal(bestInteger(P("0.5")), 0n); // exact half -> toward -infinity
  assert.equal(bestInteger(P("1.5")), 1n);
  assert.equal(bestInteger(P("2.5")), 2n);
  assert.equal(bestInteger(P("-0.5")), -1n);
  assert.equal(bestInteger(P("-1.5")), -2n);
  assert.equal(bestInteger(P("-0.4")), 0n);
  assert.equal(bestInteger(P("-0.6")), -1n);
  assert.equal(bestInteger(P("5")), 5n);
});

test("tie-break: equal error prefers smaller denominator, then smaller numerator", () => {
  // x = 1/6, Q = 3: 0/1 and 1/3 are both 1/6 away; 0/1 wins on denominator.
  assert.equal(toString(bestApprox(P("1/6"), 3n).best), "0");
  // x = 1/2, Q = 1: 0 and 1 tie on error and denominator; 0 wins on numerator.
  assert.equal(toString(bestApprox(P("1/2"), 1n).best), "0");
  // x = -1/2, Q = 1: -1 and 0 tie; -1 wins (toward -infinity).
  assert.equal(toString(bestApprox(P("-1/2"), 1n).best), "-1");
});

test("known convergents and semiconvergents of pi", () => {
  const pi = P("3.1415926535897932384626433832795028841971");
  const expect: [bigint, string][] = [
    [1n, "3"],
    [7n, "22/7"],
    [100n, "311/99"],
    [106n, "333/106"],
    [113n, "355/113"],
    [1000n, "355/113"],
    [33102n, "103993/33102"],
    [66317n, "208341/66317"],
    [100000n, "312689/99532"],
  ];
  for (const [q, want] of expect) {
    assert.equal(toString(bestApprox(pi, q).best), want, `Q=${q}`);
  }
});

test("huge denominator bounds jump via semiconvergents (no iteration over maxDen)", () => {
  const pi = P("3.1415926535897932384626433832795028841971");
  for (const q of [10n ** 10n, 10n ** 30n, 10n ** 60n]) {
    const r = bestApprox(pi, q);
    assert.ok(r.best.den <= q);
    // Dirichlet-quality error: far below 1/maxDen.
    assert.ok(r.error.num * q < r.error.den, `Q=${q}`);
  }
  // A target whose own denominator is gigantic is hit exactly when it fits.
  const big = make(123456789012345678901234567890123456789n, 10n ** 38n + 7n);
  const hit = bestApprox(big, 10n ** 38n + 7n);
  assert.equal(hit.error.num, 0n);
  assert.ok(eq(hit.best, big));
});

test("negation symmetry away from exact ties", () => {
  for (const s of ["3.14159", "2.71828", "0.37", "1.41"]) {
    for (let q = 1n; q <= 100n; q += 7n) {
      const pos = bestApprox(P(s), q);
      const negRes = bestApprox(P(`-${s}`), q);
      assert.ok(eq(negRes.best, neg(pos.best)), `x=${s} Q=${q}`);
      assert.ok(eq(negRes.error, pos.error));
    }
  }
});

test("rejects maxDen < 1", () => {
  assert.throws(() => bestApprox(P("1"), 0n));
  assert.throws(() => bestApproxExhaustive(P("1"), 0n));
});
