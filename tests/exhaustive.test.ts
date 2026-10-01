import { test } from "node:test";
import assert from "node:assert/strict";
import {
  bestApprox,
  bestApproxExhaustive,
  Budget,
  eqRational,
  makeRational,
  parseDecimal,
} from "../src/index.ts";

function assertConsistent(x: ReturnType<typeof makeRational>, maxDen: bigint): void {
  const fast = bestApprox(x, maxDen);
  const slow = bestApproxExhaustive(x, maxDen);
  assert.equal(fast.status, "ok");
  assert.equal(slow.status, "ok");
  if (fast.status !== "ok" || slow.status !== "ok") throw new Error("unreachable");
  assert.ok(
    eqRational(fast.value, slow.value),
    `winner mismatch for ${x.num}/${x.den} N=${maxDen}: ` +
      `cf=${fast.value.num}/${fast.value.den} exhaustive=${slow.value.num}/${slow.value.den}`,
  );
  assert.ok(
    eqRational(fast.error, slow.error),
    `error mismatch for ${x.num}/${x.den} N=${maxDen}`,
  );
  assert.equal(
    fast.tied.length,
    slow.tied.length,
    `tie-set size mismatch for ${x.num}/${x.den} N=${maxDen}: ` +
      `cf=[${fast.tied.map((t) => `${t.num}/${t.den}`)}] ` +
      `exhaustive=[${slow.tied.map((t) => `${t.num}/${t.den}`)}]`,
  );
  for (let i = 0; i < fast.tied.length; i++) {
    assert.ok(
      eqRational(fast.tied[i], slow.tied[i]),
      `tie-set mismatch for ${x.num}/${x.den} N=${maxDen}`,
    );
  }
}

test("cross-validation: CF vs exhaustive on a dense grid incl. negatives/integers", () => {
  for (let m = 1; m <= 40; m++) {
    for (let k = -80; k <= 80; k++) {
      const x = makeRational(BigInt(k), BigInt(m));
      for (const n of [1n, 2n, 7n, 30n]) {
        assertConsistent(x, n);
      }
    }
  }
});

test("cross-validation: deterministic pseudo-random larger cases", () => {
  let state = 0x12345678n;
  const next = (mod: bigint): bigint => {
    state = (state * 6364136223846793005n + 1442695040888963407n) & ((1n << 64n) - 1n);
    return state % mod;
  };
  for (let i = 0; i < 200; i++) {
    const num = next(2_000_000n) - 1_000_000n;
    const den = 1n + next(10_000n);
    const maxDen = 1n + next(2_000n);
    assertConsistent(makeRational(num, den), maxDen);
  }
});

test("cross-validation: pi decimal with maxDen = 100000", () => {
  const pi = parseDecimal("3.14159265358979323846264338327950288");
  assertConsistent(pi, 100_000n);
});

test("cross-validation: semiconvergent region with large partial quotient", () => {
  // x = 1000/3001 = [0; 3, 1000]; for 1501 <= N < 3001 the optimum is the
  // semiconvergent k/(3k+1) with k = floor((N-1)/3), reached by the t-jump.
  const x = makeRational(1000n, 3001n);
  for (const n of [100n, 334n, 500n, 800n, 1000n, 1500n, 1501n, 2000n, 3000n, 3001n]) {
    assertConsistent(x, n);
  }
  const r = bestApprox(x, 1501n);
  assert.equal(r.status, "ok");
  if (r.status === "ok") assert.deepEqual(r.value, { num: 500n, den: 1501n });
});

test("exhaustive: budget interruption reports progress", () => {
  const pi = parseDecimal("3.14159265358979323846264338327950288");
  const r = bestApproxExhaustive(pi, 1_000_000n, new Budget(10));
  assert.equal(r.status, "budget-exhausted");
  if (r.status === "budget-exhausted") {
    assert.equal(r.partial.checkedDenominators, 10n);
    assert.deepEqual(r.partial.bestSoFar, { num: 22n, den: 7n });
  }
});

test("exhaustive: equidistant tie sets match the documented rule", () => {
  const r = bestApproxExhaustive(makeRational(1n, 2n), 1n);
  assert.equal(r.status, "ok");
  if (r.status === "ok") {
    assert.deepEqual(r.tied, [{ num: 0n, den: 1n }, { num: 1n, den: 1n }]);
    assert.deepEqual(r.value, { num: 0n, den: 1n });
  }
  const neg = bestApproxExhaustive(makeRational(-3n, 2n), 1n);
  assert.equal(neg.status, "ok");
  if (neg.status === "ok") {
    assert.deepEqual(neg.value, { num: -1n, den: 1n });
    assert.deepEqual(neg.tied, [{ num: -1n, den: 1n }, { num: -2n, den: 1n }]);
  }
});
