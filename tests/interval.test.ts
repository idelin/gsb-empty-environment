import { test } from "node:test";
import assert from "node:assert/strict";
import {
  approximateInterval,
  Budget,
  intervalFromRoundedDecimal,
  makeInterval,
  makeRational,
  parseDecimal,
} from "../src/index.ts";

test("decided when all targets in the interval share the optimum", () => {
  const r = approximateInterval(
    makeInterval(parseDecimal("3.1415"), parseDecimal("3.1416")),
    100n,
  );
  assert.equal(r.status, "decided");
  if (r.status === "decided") {
    assert.deepEqual(r.value, { num: 311n, den: 99n });
    assert.deepEqual(r.worstError, r.errorAtHi);
  }
});

test("undecided with competing candidates and boundary evidence", () => {
  const r = approximateInterval(
    makeInterval(parseDecimal("3.1"), parseDecimal("3.2")),
    7n,
  );
  assert.equal(r.status, "undecided");
  if (r.status !== "undecided") throw new Error("unreachable");
  assert.deepEqual(r.reason, ["different optima at interval endpoints"]);
  assert.deepEqual(r.candidates, [
    { num: 22n, den: 7n },
    { num: 16n, den: 5n },
  ]);
  const e0 = r.evidence[0];
  const e1 = r.evidence[1];
  assert.equal(e0.optimalAtLo, true);
  assert.equal(e0.optimalAtHi, false);
  assert.equal(e1.optimalAtLo, false);
  assert.equal(e1.optimalAtHi, true);
  // equidistant point of 22/7 and 16/5 lies inside the interval
  assert.deepEqual(r.boundaries, [{ num: 111n, den: 35n }]);
});

test("undecided when an endpoint itself is tied", () => {
  const r = approximateInterval(
    makeInterval(makeRational(1n, 2n), makeRational(3n, 4n)),
    1n,
  );
  assert.equal(r.status, "undecided");
  if (r.status !== "undecided") throw new Error("unreachable");
  assert.ok(r.reason.includes("tie at lo"));
  assert.deepEqual(r.candidates, [{ num: 0n, den: 1n }, { num: 1n, den: 1n }]);
});

test("degenerate interval [x, x] behaves like a point target", () => {
  const r = approximateInterval(
    makeInterval(makeRational(355n, 113n), makeRational(355n, 113n)),
    113n,
  );
  assert.equal(r.status, "decided");
  if (r.status === "decided") {
    assert.deepEqual(r.value, { num: 355n, den: 113n });
    assert.deepEqual(r.worstError, { num: 0n, den: 1n });
  }
});

test("refinable precision: more digits flip undecided -> decided", () => {
  const coarse = approximateInterval(intervalFromRoundedDecimal("3.1"), 7n);
  assert.equal(coarse.status, "undecided");
  const finer = approximateInterval(intervalFromRoundedDecimal("3.14"), 7n);
  assert.equal(finer.status, "decided");
  if (finer.status === "decided") {
    assert.deepEqual(finer.value, { num: 22n, den: 7n });
  }
  const finest = approximateInterval(intervalFromRoundedDecimal("3.1416"), 7n);
  assert.equal(finest.status, "decided");
  if (finest.status === "decided" && finer.status === "decided") {
    assert.ok(finest.worstError.num * finer.worstError.den <
      finer.worstError.num * finest.worstError.den, "error bound shrinks");
  }
});

test("intervalFromRoundedDecimal builds the half-ulp interval", () => {
  const t = intervalFromRoundedDecimal("3.14");
  assert.deepEqual(t.lo, { num: 627n, den: 200n });
  assert.deepEqual(t.hi, { num: 629n, den: 200n });
  const neg = intervalFromRoundedDecimal("-0.5");
  assert.deepEqual(neg.lo, { num: -11n, den: 20n });
  assert.deepEqual(neg.hi, { num: -9n, den: 20n });
});

test("negative interval targets", () => {
  const r = approximateInterval(
    makeInterval(parseDecimal("-3.15"), parseDecimal("-3.13")),
    7n,
  );
  assert.equal(r.status, "decided");
  if (r.status === "decided") {
    assert.deepEqual(r.value, { num: -22n, den: 7n });
  }
});

test("interval: budget interruption propagates with stage", () => {
  const r = approximateInterval(
    makeInterval(parseDecimal("3.1"), parseDecimal("3.2")),
    100n,
    new Budget(1),
  );
  assert.equal(r.status, "budget-exhausted");
  if (r.status === "budget-exhausted") assert.equal(r.stage, "lo");
});

test("makeInterval rejects inverted bounds", () => {
  assert.throws(
    () => makeInterval(parseDecimal("3.2"), parseDecimal("3.1")),
    RangeError,
  );
});
