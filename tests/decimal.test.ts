import { test } from "node:test";
import assert from "node:assert/strict";
import { frac } from "../src/fraction.ts";
import {
  expand,
  expandText,
  format,
  locateCycle,
  pageDigits,
  parseExact,
  preperiodInfo,
} from "../src/decimal.ts";

test("1/6 is 0.1(6): prefix length 1, period length 1", () => {
  const e = expand(frac(1n, 6n));
  assert.equal(e.kind, "repeating");
  assert.equal(e.preperiod, "1");
  assert.equal(e.period, "6");
  assert.equal(e.preperiodLength, 1);
  assert.equal(e.periodLength, 1);
  assert.equal(format(e), "0.1(6)");
});

test("known repeating expansions render canonically", () => {
  const cases: Array<[bigint, bigint, string]> = [
    [1n, 7n, "0.(142857)"],
    [1n, 3n, "0.(3)"],
    [2n, 3n, "0.(6)"],
    [1n, 28n, "0.03(571428)"],
    [1n, 202n, "0.0(0495)"],
    [1n, 12n, "0.08(3)"],
    [-1n, 6n, "-0.1(6)"],
    [7n, 6n, "1.1(6)"],
    [-7n, 6n, "-1.1(6)"],
  ];
  for (const [n, d, want] of cases) {
    assert.equal(expandText(frac(n, d)), want, `${n}/${d}`);
  }
});

test("terminating decimals carry no period; zero renders as 0", () => {
  assert.equal(expandText(frac(1n, 2n)), "0.5");
  assert.equal(expandText(frac(1n, 8n)), "0.125");
  assert.equal(expandText(frac(3n, 1n)), "3");
  assert.equal(expandText(frac(0n, 7n)), "0");
  assert.equal(expandText(frac(-4n, 2n)), "-2");
  const e = expand(frac(1n, 20n));
  assert.equal(e.kind, "terminating");
  assert.equal(e.periodLength, 0);
});

test("preperiodInfo is pure number theory (strip 2 and 5)", () => {
  assert.deepEqual(preperiodInfo(200n), { preLen: 3, coprime: 1n });
  assert.deepEqual(preperiodInfo(28n), { preLen: 2, coprime: 7n });
  assert.deepEqual(preperiodInfo(6n), { preLen: 1, coprime: 3n });
  assert.deepEqual(preperiodInfo(7n), { preLen: 0, coprime: 7n });
});

test("locateCycle reports prefix and period length without a huge string", () => {
  assert.deepEqual(locateCycle(frac(1n, 7n)), { kind: "repeating", preperiodLength: 0, periodLength: 6 });
  assert.deepEqual(locateCycle(frac(1n, 6n)), { kind: "repeating", preperiodLength: 1, periodLength: 1 });
  assert.deepEqual(locateCycle(frac(1n, 2n)), { kind: "terminating", preperiodLength: 1, periodLength: 0 });
});

test("budget exceeded is reported incomplete, not an infinite or false exact string", () => {
  // 100003 is prime; ord_100003(10) is far longer than 12 digits.
  const e = expand(frac(1n, 100003n), { maxDigits: 12, maxStates: 13 });
  assert.equal(e.kind, "incomplete");
  assert.equal(e.periodLength, null);
  assert.equal(e.preperiodLength, 0);
  assert.match(format(e), /\.\.\.$/);
  assert.ok(e.digitsProduced <= 12);
});

test("a long pure-preperiod denominator is also marked incomplete inside the budget", () => {
  // 1/10^60 terminates, but prefix length 60 exceeds a 10-digit budget.
  const d = 10n ** 60n;
  const e = expand(frac(1n, d), { maxDigits: 10 });
  assert.equal(e.kind, "incomplete");
  assert.equal(e.preperiodLength, 60);
  assert.equal(e.digitsProduced, 10);
});

test("paging a repeating decimal wraps by period index arithmetic", () => {
  const f = frac(1n, 7n);
  assert.equal(pageDigits(f, 0, 6).digits, "142857");
  assert.equal(pageDigits(f, 6, 6).digits, "142857");
  assert.equal(pageDigits(f, 1, 5).digits, "42857");
  const deep = pageDigits(f, 1_000_000, 7);
  assert.equal(deep.digits, "5714285");
  assert.equal(deep.truncated, false);
  assert.equal(deep.periodLength, 6);

  const sixth = frac(1n, 6n);
  assert.equal(pageDigits(sixth, 0, 5).digits, "16666");
  assert.equal(pageDigits(sixth, 100, 3).digits, "666");

  // page spanning the preperiod boundary into the period (1/28 = 0.03(571428))
  const twentyeighth = frac(1n, 28n);
  assert.equal(pageDigits(twentyeighth, 1, 8).digits, "35714285");
  assert.equal(pageDigits(twentyeighth, 0, 8).digits, "03571428");
});

test("paging a terminating decimal returns infinite zeros", () => {
  const p = pageDigits(frac(1n, 8n), 0, 6);
  assert.equal(p.digits, "125000");
  assert.equal(p.truncated, false);
  assert.equal(pageDigits(frac(1n, 8n), 3, 3).digits, "000");
});

test("paging an incomplete expansion marks truncation honestly", () => {
  const p = pageDigits(frac(1n, 100003n), 0, 12, { maxDigits: 11 });
  assert.equal(p.truncated, true);
  const p2 = pageDigits(frac(1n, 100003n), 0, 5, { maxDigits: 12 });
  assert.equal(p2.truncated, false);
});

test("paging validates arguments", () => {
  assert.throws(() => pageDigits(frac(1n), -1, 1), /non-negative/);
});

test("parseExact reconstructs terminating and repeating decimals algebraically", () => {
  assert.deepEqual(parseExact("0.1(6)"), frac(1n, 6n));
  assert.deepEqual(parseExact("0.(142857)"), frac(1n, 7n));
  assert.deepEqual(parseExact("0.03(571428)"), frac(1n, 28n));
  assert.deepEqual(parseExact("-0.1(6)"), frac(-1n, 6n));
  assert.deepEqual(parseExact("2.5"), frac(5n, 2n));
  assert.deepEqual(parseExact("-2"), frac(-2n));
  assert.deepEqual(parseExact("1.(9)"), frac(2n));
  assert.deepEqual(parseExact("0.999"), frac(999n, 1000n));
});

test("parseExact rejects truncated input: a prefix is never treated as exact", () => {
  assert.throws(() => parseExact("0.333..."), /exact/);
  assert.throws(() => parseExact("0.1("), /exact/);
});
