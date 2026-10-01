import { test } from "node:test";
import assert from "node:assert/strict";
import {
  bitLength,
  iroot,
  irootRem,
  isqrt,
  isqrtRem,
  pow,
} from "../src/integer-roots.ts";

test("isqrt: exhaustive small integers 0..100000 vs Math.sqrt + bracket", () => {
  for (let n = 0; n <= 100_000; n++) {
    const b = BigInt(n);
    const r = isqrt(b);
    // independent reference (safe: sqrt(n) is far from integer boundary at this size)
    assert.equal(r, BigInt(Math.floor(Math.sqrt(n))), `isqrt(${n})`);
    // exact integer bracket: r^2 <= n < (r+1)^2
    assert.ok(r * r <= b && b < (r + 1n) * (r + 1n), `bracket(${n})`);
  }
});

test("isqrt: square boundary counterexamples m^2-1, m^2, m^2+1", () => {
  const ms: bigint[] = [];
  for (let m = 1; m <= 500; m++) ms.push(BigInt(m));
  ms.push(10n ** 30n, 2n ** 1000n, 10n ** 500n + 12345n);
  for (const m of ms) {
    const sq = m * m;
    assert.equal(isqrt(sq), m, `isqrt(m^2) exact for m=${m.toString().slice(0, 12)}...`);
    assert.equal(isqrt(sq - 1n), m - 1n, "isqrt(m^2 - 1)");
    assert.equal(isqrt(sq + 1n), m, "isqrt(m^2 + 1)");
    const rem = isqrtRem(sq);
    assert.equal(rem.remainder, 0n, "perfect square has zero remainder");
  }
});

test("isqrt: oscillation-prone inputs k^2-1 terminate correctly", () => {
  // Under a naive "stop when y_{i+1} == y_i" rule these never settle:
  // the Newton orbit alternates between k-1 and k. Our stop rule is
  // "stop on non-decrease", which is proven correct.
  for (let k = 2n; k <= 400n; k++) {
    const n = k * k - 1n;
    const { root, remainder } = isqrtRem(n);
    assert.equal(root, k - 1n, `isqrt(${n})`);
    assert.equal(remainder, n - (k - 1n) * (k - 1n));
    assert.equal(remainder, 2n * k - 2n);
  }
});

test("isqrt: iteration count stays within proven-order bound", () => {
  // Termination is proven by monotone decrease; the cap bitLength+8 must
  // never be hit. Check a spread of sizes, including 4096-bit values.
  let x = 1n;
  for (let i = 0; i < 200; i++) {
    x = x * 2n ** 17n + 0x9e3779b97f4a7c15n;
    const { root, remainder, iterations } = isqrtRem(x);
    assert.ok(iterations <= bitLength(x), `iterations ${iterations} vs bits ${bitLength(x)}`);
    assert.ok(root * root <= x && x < (root + 1n) * (root + 1n));
    assert.equal(remainder, x - root * root);
    assert.ok(remainder >= 0n && remainder <= 2n * root);
  }
});

test("isqrt: negative input rejected", () => {
  assert.throws(() => isqrt(-1n), RangeError);
  assert.throws(() => isqrtRem(-(10n ** 50n)), RangeError);
});

test("iroot: exhaustive small values, degrees 2..8, bracket invariant", () => {
  for (let k = 2; k <= 8; k++) {
    for (let n = 0; n <= 3000; n++) {
      const b = BigInt(n);
      const r = iroot(b, k);
      assert.ok(pow(r, k) <= b, `iroot(${n},${k}) lower`);
      assert.ok(b < pow(r + 1n, k), `iroot(${n},${k}) upper`);
    }
  }
});

test("iroot: perfect powers exact, neighbours straddle", () => {
  for (let k = 2; k <= 6; k++) {
    for (let m = 1n; m <= 60n; m++) {
      const p = pow(m, k);
      assert.equal(iroot(p, k), m);
      assert.equal(iroot(p - 1n, k), m - 1n);
      assert.equal(iroot(p + 1n, k), m);
      assert.equal(irootRem(p, k).remainder, 0n);
    }
  }
});

test("iroot: degree 2 agrees with the isqrt fast path", () => {
  let x = 3n;
  for (let i = 0; i < 300; i++) {
    x = x * 6364136223846793005n + 1442695040888963407n;
    if (x < 0n) x = -x;
    assert.equal(iroot(x, 2), isqrt(x));
  }
});

test("iroot: rejects negative input and bad degrees", () => {
  assert.throws(() => iroot(-5n, 3), RangeError);
  assert.throws(() => iroot(8n, 1), RangeError);
  assert.throws(() => iroot(8n, 0), RangeError);
  assert.throws(() => iroot(8n, 2.5), RangeError);
});

test("iroot: large exact powers", () => {
  assert.equal(iroot(2n ** 3600n, 3), 2n ** 1200n);
  assert.equal(iroot(2n ** 3600n - 1n, 3), 2n ** 1200n - 1n);
  assert.equal(iroot(10n ** 900n, 9), 10n ** 100n);
});
