import { test } from "node:test";
import assert from "node:assert/strict";
import {
  bestApprox,
  Budget,
  continuedFractionTerms,
  eqRational,
  makeRational,
  parseDecimal,
} from "../src/index.ts";

const PI = parseDecimal("3.14159265358979323846264338327950288");

function okValue(r: ReturnType<typeof bestApprox>) {
  assert.equal(r.status, "ok");
  if (r.status !== "ok") throw new Error("unreachable");
  return r;
}

test("continuedFractionTerms: known expansions", () => {
  assert.deepEqual(continuedFractionTerms(355n, 113n), [3n, 7n, 16n]);
  assert.deepEqual(continuedFractionTerms(1n, 2n), [0n, 2n]);
  assert.deepEqual(continuedFractionTerms(-3n, 2n), [-2n, 2n]); // floor-based
  assert.deepEqual(continuedFractionTerms(42n, 1n), [42n]);
});

test("pi: classical convergents and semiconvergents", () => {
  assert.deepEqual(okValue(bestApprox(PI, 1n)).value, { num: 3n, den: 1n });
  assert.deepEqual(okValue(bestApprox(PI, 7n)).value, { num: 22n, den: 7n });
  assert.deepEqual(okValue(bestApprox(PI, 100n)).value, { num: 311n, den: 99n });
  assert.deepEqual(okValue(bestApprox(PI, 113n)).value, { num: 355n, den: 113n });
  assert.deepEqual(okValue(bestApprox(PI, 1_000_000n)).value, {
    num: 3126535n,
    den: 995207n,
  });
});

test("semiconvergent jump: best pick is a semiconvergent, not a convergent", () => {
  // x = 100/201 = [0; 2, 100]; convergents 0/1, 1/2, 100/201.
  // maxDen = 150 -> t = floor((150 - 1) / 2) = 74 -> 74/149 beats 1/2.
  const x = makeRational(100n, 201n);
  const r = okValue(bestApprox(x, 150n));
  assert.deepEqual(r.value, { num: 74n, den: 149n });
  assert.equal(r.exact, false);
});

test("exactly representable target: error 0", () => {
  const r = okValue(bestApprox(makeRational(355n, 113n), 113n));
  assert.deepEqual(r.value, { num: 355n, den: 113n });
  assert.deepEqual(r.error, { num: 0n, den: 1n });
  assert.equal(r.exact, true);
  const i = okValue(bestApprox(makeRational(42n, 1n), 1n));
  assert.deepEqual(i.value, { num: 42n, den: 1n });
  assert.equal(i.exact, true);
});

test("maxDen = 1 returns the best integer", () => {
  assert.deepEqual(okValue(bestApprox(PI, 1n)).value, { num: 3n, den: 1n });
  assert.deepEqual(okValue(bestApprox(parseDecimal("2.7"), 1n)).value, { num: 3n, den: 1n });
  assert.deepEqual(okValue(bestApprox(parseDecimal("-2.7"), 1n)).value, { num: -3n, den: 1n });
});

test("equidistant tie-breaking: smaller denominator, then smaller |p|", () => {
  const half = okValue(bestApprox(makeRational(1n, 2n), 1n));
  assert.deepEqual(half.value, { num: 0n, den: 1n });
  assert.deepEqual(half.tied, [{ num: 0n, den: 1n }, { num: 1n, den: 1n }]);
  const sevenHalves = okValue(bestApprox(makeRational(7n, 2n), 1n));
  assert.deepEqual(sevenHalves.value, { num: 3n, den: 1n });
  assert.deepEqual(sevenHalves.tied, [{ num: 3n, den: 1n }, { num: 4n, den: 1n }]);
});

test("negatives are exact mirrors: best(-x) = -best(x)", () => {
  for (const s of ["3.14159265", "-3.14159265", "2.5", "-2.5", "-0.5", "-100/201"]) {
    const x = parseDecimal(s);
    const pos = okValue(bestApprox(x.num < 0n ? makeRational(-x.num, x.den) : x, 100n));
    const got = okValue(bestApprox(x, 100n));
    const want = x.num < 0n ? { num: -pos.value.num, den: pos.value.den } : pos.value;
    assert.deepEqual(got.value, want, `mirror for ${s}`);
  }
  assert.deepEqual(okValue(bestApprox(makeRational(-7n, 2n), 1n)).value, { num: -3n, den: 1n });
});

test("huge denominator bound 10^30: jump stays exact and within bound", () => {
  const r = okValue(bestApprox(PI, 10n ** 30n));
  assert.ok(r.value.den <= 10n ** 30n);
  assert.ok(r.value.den > 10n ** 20n, "should use a genuinely large denominator");
  // self-consistency: the same fraction must win with maxDen exactly its denominator
  const again = okValue(bestApprox(PI, r.value.den));
  assert.ok(eqRational(again.value, r.value));
});

test("budget interruption returns partial progress", () => {
  const r = bestApprox(PI, 10n ** 30n, new Budget(3));
  assert.equal(r.status, "budget-exhausted");
  if (r.status === "budget-exhausted") {
    assert.ok(r.partial.lastConvergent !== null);
  }
  // enough budget -> ok again
  assert.equal(bestApprox(PI, 10n ** 30n, new Budget(10_000)).status, "ok");
});

test("invalid maxDen rejected", () => {
  assert.throws(() => bestApprox(PI, 0n), RangeError);
});
