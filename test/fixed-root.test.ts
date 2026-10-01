import { test } from "node:test";
import assert from "node:assert/strict";
import { pow } from "../src/integer-roots.ts";
import { roundQuotient, ROUNDING_MODES } from "../src/rounding.ts";
import type { RoundingMode } from "../src/rounding.ts";
import { DEFAULT_BUDGET, rootFixed, sqrtFixed } from "../src/fixed-root.ts";
import type { FixedRootResult } from "../src/fixed-root.ts";

// Deterministic PRNG (mulberry32) so the property tests are reproducible.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rand = mulberry32(0x5eed);
function randomBigInt(bits: number): bigint {
  let v = 0n;
  let remaining = bits;
  while (remaining > 0) {
    const chunk = Math.min(remaining, 30);
    v = (v << BigInt(chunk)) | BigInt(Math.floor(rand() * 2 ** chunk));
    remaining -= chunk;
  }
  return v;
}

/**
 * Independent exact verification of a FixedRootResult: recompute the
 * integer bracket and the rounding decision from scratch using only the
 * defining inequalities on v^degree = A/B.
 */
function verifyResult(
  x: bigint,
  inScale: number,
  outScale: number,
  degree: number,
  mode: RoundingMode,
  res: FixedRootResult,
): void {
  const e = degree * outScale - inScale;
  const A = e >= 0 ? x * pow(10n, e) : x;
  const B = e >= 0 ? 1n : pow(10n, -e);
  const { floor, ceil, rounded, remainder, exact } = res;
  assert.ok(pow(floor, degree) * B <= A, "floor^k * B <= A");
  assert.ok(A < pow(floor + 1n, degree) * B, "A < (floor+1)^k * B");
  assert.equal(remainder, A - pow(floor, degree) * B, "exact remainder");
  assert.ok(remainder >= 0n);
  assert.equal(exact, remainder === 0n);
  assert.equal(ceil, exact ? floor : floor + 1n, "ceil bracket");
  assert.ok(rounded === floor || rounded === ceil, "rounded within bracket");
  const lhs = pow(2n, degree) * A;
  const rhs = pow(2n * floor + 1n, degree) * B;
  switch (mode) {
    case "floor":
      assert.equal(rounded, floor);
      break;
    case "ceil":
      assert.equal(rounded, ceil);
      break;
    case "halfUp":
      assert.equal(rounded, lhs >= rhs ? ceil : floor);
      break;
    case "halfEven":
      if (lhs !== rhs) assert.equal(rounded, lhs > rhs ? ceil : floor);
      else assert.equal(rounded, floor % 2n === 0n ? floor : ceil, "tie to even");
      break;
  }
}

test("sqrtFixed: perfect squares are exact at any output scale", () => {
  const ms = [1n, 2n, 7n, 123456789n, 10n ** 40n + 7n, 2n ** 200n - 1n];
  for (const m of ms) {
    for (const out of [0, 1, 5, 30]) {
      const res = sqrtFixed(m * m, 0, out, "halfEven");
      assert.equal(res.rounded, m * pow(10n, out));
      assert.equal(res.floor, res.rounded);
      assert.equal(res.ceil, res.rounded);
      assert.ok(res.exact);
      assert.equal(res.remainder, 0n);
    }
  }
});

test("sqrtFixed: sqrt(2) matches known decimal expansion", () => {
  // sqrt(2) = 1.414213562373095048801688724209698078569...
  const res = sqrtFixed(2n, 0, 20, "halfEven");
  assert.equal(res.rounded, 141421356237309504880n);
  assert.equal(sqrtFixed(2n, 0, 20, "halfUp").rounded, 141421356237309504880n);
  assert.equal(sqrtFixed(2n, 0, 20, "floor").rounded, 141421356237309504880n);
  assert.equal(sqrtFixed(2n, 0, 20, "ceil").rounded, 141421356237309504881n);
  const res50 = sqrtFixed(2n, 0, 50, "halfEven");
  assert.equal(res50.rounded, 141421356237309504880168872420969807856967187537695n);
});

test("sqrtFixed: exact decimal midpoints, half-even vs half-up", () => {
  // sqrt(0.25) = 0.5 exactly -> tie at outScale 0
  const t1u = sqrtFixed(25n, 2, 0, "halfUp");
  const t1e = sqrtFixed(25n, 2, 0, "halfEven");
  assert.equal(t1u.rounded, 1n);
  assert.equal(t1e.rounded, 0n, "0.5 -> 0 (even)");
  assert.equal(t1e.floor, 0n);
  assert.equal(t1e.ceil, 1n);
  assert.ok(!t1e.exact);
  // sqrt(2.25) = 1.5 -> both round to 2
  assert.equal(sqrtFixed(225n, 2, 0, "halfUp").rounded, 2n);
  assert.equal(sqrtFixed(225n, 2, 0, "halfEven").rounded, 2n);
  // sqrt(6.25) = 2.5 -> half-even rounds to 2, half-up to 3
  assert.equal(sqrtFixed(625n, 2, 0, "halfUp").rounded, 3n);
  assert.equal(sqrtFixed(625n, 2, 0, "halfEven").rounded, 2n);
  // sqrt(12.25) = 3.5 -> both round to 4
  assert.equal(sqrtFixed(1225n, 2, 0, "halfUp").rounded, 4n);
  assert.equal(sqrtFixed(1225n, 2, 0, "halfEven").rounded, 4n);
});

test("rootFixed: exact decimal midpoints for cube roots", () => {
  // cbrt(0.125) = 0.5 exactly
  assert.equal(rootFixed(125n, 3, 0, 3, "halfUp").rounded, 1n);
  assert.equal(rootFixed(125n, 3, 0, 3, "halfEven").rounded, 0n);
  // cbrt(3.375) = 1.5
  assert.equal(rootFixed(3375n, 3, 0, 3, "halfUp").rounded, 2n);
  assert.equal(rootFixed(3375n, 3, 0, 3, "halfEven").rounded, 2n);
  // cbrt(15.625) = 2.5 -> half-even 2, half-up 3
  assert.equal(rootFixed(15625n, 3, 0, 3, "halfUp").rounded, 3n);
  assert.equal(rootFixed(15625n, 3, 0, 3, "halfEven").rounded, 2n);
});

test("sqrtFixed: extremely small inputs do not collapse to 0", () => {
  // sqrt(1e-100) = 1e-50 exactly; naive input truncation would give 0.
  const tiny = sqrtFixed(1n, 100, 50, "halfEven");
  assert.equal(tiny.rounded, 1n);
  assert.ok(tiny.exact);
  // sqrt(1e-101) ~= 3.162e-51: below half an ulp at scale 50 but positive.
  const t2 = sqrtFixed(1n, 101, 50, "halfEven");
  assert.equal(t2.rounded, 0n, "nearest: 0.316 ulp rounds to 0");
  assert.equal(t2.floor, 0n);
  assert.equal(t2.ceil, 1n);
  assert.equal(sqrtFixed(1n, 101, 50, "ceil").rounded, 1n, "ceil keeps it nonzero");
  // sqrt(0.5) = 0.7071... rounds to 1 at scale 0; truncating the input
  // 0.5 -> 0 first would wrongly answer 0.
  assert.equal(sqrtFixed(5n, 1, 0, "halfUp").rounded, 1n);
  assert.equal(sqrtFixed(5n, 1, 0, "halfEven").rounded, 1n);
  // sqrt(0.04) = 0.2 exact at scale 1 even though inScale > 2*outScale.
  const t3 = sqrtFixed(4n, 2, 1, "halfEven");
  assert.equal(t3.rounded, 2n);
  assert.ok(t3.exact);
});
test("sqrtFixed fast path agrees with general rootFixed(degree 2)", () => {
  for (let i = 0; i < 300; i++) {
    const bits = 1 + Math.floor(rand() * 300);
    const x = randomBigInt(bits);
    const inScale = Math.floor(rand() * 40);
    const outScale = Math.floor(rand() * 25);
    const mode = ROUNDING_MODES[i % ROUNDING_MODES.length];
    const fast = sqrtFixed(x, inScale, outScale, mode);
    const general = rootFixed(x, inScale, outScale, 2, mode);
    assert.equal(
      fast.rounded,
      general.rounded,
      `x=${x} in=${inScale} out=${outScale} ${mode}`,
    );
    assert.equal(fast.floor, general.floor);
    assert.equal(fast.ceil, general.ceil);
    assert.equal(fast.remainder, general.remainder);
  }
});

test("recovery: guard-digit re-rounding reproduces the direct result", () => {
  // Computing at outScale+G and re-rounding the exact integer quotient by
  // 10^G with the same rule must give the same answer as computing directly
  // at outScale. Includes exact ties and perfect powers.
  const cases: Array<[bigint, number, number]> = [
    [2n, 0, 0],
    [2n, 0, 10],
    [25n, 2, 0], // exact tie 0.5
    [625n, 2, 0], // exact tie 2.5
    [15625n, 3, 0], // cbrt tie 2.5 (degree 3 below)
    [123456789123456789n, 4, 3],
    [1n, 100, 50], // tiny exact
    [1n, 101, 50], // tiny inexact
    [10n ** 60n + 1n, 0, 7],
  ];
  const G = 6;
  for (const [x, inScale, outScale] of cases) {
    for (const degree of [2, 3, 5]) {
      for (const mode of ROUNDING_MODES) {
        const direct = rootFixed(x, inScale, outScale, degree, mode);
        const hi = rootFixed(x, inScale, outScale + G, degree, mode);
        const recovered = roundQuotient(hi.rounded, pow(10n, G), mode);
        assert.equal(
          recovered,
          direct.rounded,
          `x=${x} in=${inScale} out=${outScale} k=${degree} ${mode}`,
        );
      }
    }
  }
});

test("property: exact bracket + rounding decision on random inputs", () => {
  for (let i = 0; i < 250; i++) {
    const bits = 1 + Math.floor(rand() * 250);
    const x = randomBigInt(bits);
    const inScale = Math.floor(rand() * 60);
    const outScale = Math.floor(rand() * 30);
    const degree = 2 + Math.floor(rand() * 5);
    const mode = ROUNDING_MODES[Math.floor(rand() * ROUNDING_MODES.length)];
    const res = rootFixed(x, inScale, outScale, degree, mode);
    verifyResult(x, inScale, outScale, degree, mode, res);
  }
});

test("sqrtFixed: result square bound verification (directed modes)", () => {
  // For floor mode the returned m satisfies (m*10^-out)^2 <= value,
  // and (m+1)^2 * B > A in exact integer terms (checked in verifyResult).
  for (let i = 0; i < 200; i++) {
    const x = randomBigInt(1 + Math.floor(rand() * 200));
    const outScale = Math.floor(rand() * 20);
    const res = sqrtFixed(x, 0, outScale, "floor");
    verifyResult(x, 0, outScale, 2, "floor", res);
    // explicit square bounds in ulp units: floor^2 <= x*10^(2out) < (floor+1)^2
    const scaled = x * pow(10n, 2 * outScale);
    assert.ok(res.rounded * res.rounded <= scaled);
    assert.ok(scaled < (res.rounded + 1n) * (res.rounded + 1n));
  }
});

test("rootFixed: negative inputs and invalid degrees rejected", () => {
  assert.throws(() => sqrtFixed(-1n, 0, 0), RangeError);
  assert.throws(() => rootFixed(-8n, 0, 0, 3), RangeError);
  assert.throws(() => rootFixed(8n, 0, 0, 1), RangeError);
  assert.throws(() => rootFixed(8n, 0, 0, 0), RangeError);
  assert.throws(() => rootFixed(8n, 0, 0, 2.5), RangeError);
});

test("budget: digit counts are capped and configurable", () => {
  assert.throws(
    () => sqrtFixed(2n, 0, DEFAULT_BUDGET.maxScale + 1),
    RangeError,
    "outScale beyond budget",
  );
  assert.throws(
    () => sqrtFixed(2n, DEFAULT_BUDGET.maxScale + 1, 0),
    RangeError,
    "inScale beyond budget",
  );
  const tight = { maxScale: 1000, maxBits: 256 };
  assert.throws(() => sqrtFixed(2n ** 4096n, 0, 0, "halfEven", tight), RangeError);
  // within budget it still works
  const ok = sqrtFixed(2n, 0, 10, "halfEven", tight);
  assert.equal(ok.rounded, 14142135624n);
});

test("rootFixed: large exact powers and known roots", () => {
  // sqrt(2^4096) = 2^2048 exactly
  const big = sqrtFixed(1n << 4096n, 0, 0, "halfEven");
  assert.equal(big.rounded, 1n << 2048n);
  assert.ok(big.exact);
  // cbrt(2) = 1.2599210498948731647... at 16 places
  const cbrt2 = rootFixed(2n, 0, 16, 3, "halfEven");
  assert.equal(cbrt2.rounded, 12599210498948732n);
  // 10th root of 10^1000 = 10^100 exactly
  const r10 = rootFixed(pow(10n, 1000), 0, 5, 10, "halfEven");
  assert.equal(r10.rounded, pow(10n, 105));
  assert.ok(r10.exact);
});
