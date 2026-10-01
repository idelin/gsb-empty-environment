import { test } from "node:test";
import assert from "node:assert/strict";
import { rat } from "../src/rational.ts";
import { MeasurementStore } from "../src/measurement.ts";
import { constraint } from "../src/constraint.ts";
import { classifyConstraint } from "../src/classify.ts";

function storeWith(id: string, value: string, error: string | null): MeasurementStore {
  const s = new MeasurementStore();
  s.set(id, rat(value), error === null ? null : rat(error));
  return s;
}

test("interval crossing the threshold is needs-review, never centre-value pass", () => {
  // Centre 9.5 < 10 would "pass" by centre value; interval [9, 10.5] crosses.
  const s = storeWith("temp", "9.5", "1");
  const c = constraint("c1", [{ measurement: "temp", coeff: rat("1") }], "<=", rat("10"));
  const r = classifyConstraint(c, s);
  assert.equal(r.status, "needs-review");
  assert.equal(r.maxValue?.toString(), "21/2");
});

test("fully below threshold is determinate-pass", () => {
  const s = storeWith("temp", "9.5", "0.4");
  const c = constraint("c1", [{ measurement: "temp", coeff: rat("1") }], "<=", rat("10"));
  assert.equal(classifyConstraint(c, s).status, "determinate-pass");
});

test("fully above threshold is determinate-fail", () => {
  const s = storeWith("temp", "20", "1");
  const c = constraint("c1", [{ measurement: "temp", coeff: rat("1") }], "<=", rat("10"));
  assert.equal(classifyConstraint(c, s).status, "determinate-fail");
});

test("exactly touching a closed threshold passes", () => {
  const s = storeWith("temp", "10", "0");
  const c = constraint("c1", [{ measurement: "temp", coeff: rat("1") }], "<=", rat("10"));
  assert.equal(classifyConstraint(c, s).status, "determinate-pass");
});

test("exactly touching an open threshold fails when the point is the only option", () => {
  const s = storeWith("temp", "10", "0");
  const c = constraint("c1", [{ measurement: "temp", coeff: rat("1") }], "<", rat("10"));
  assert.equal(classifyConstraint(c, s).status, "determinate-fail");
});

test("touching an open threshold only at the interval edge is needs-review", () => {
  // interval [9, 10], threshold x < 10: worst case 10 is not < 10, best case 9 is.
  const s = storeWith("temp", "9.5", "0.5");
  const c = constraint("c1", [{ measurement: "temp", coeff: rat("1") }], "<", rat("10"));
  assert.equal(classifyConstraint(c, s).status, "needs-review");
});

test("closed threshold reached at the interval edge passes", () => {
  // interval [9, 10], threshold x <= 10: worst case 10 <= 10.
  const s = storeWith("temp", "9.5", "0.5");
  const c = constraint("c1", [{ measurement: "temp", coeff: rat("1") }], "<=", rat("10"));
  assert.equal(classifyConstraint(c, s).status, "determinate-pass");
});

test("unknown error is never treated as 0: cannot pass even with huge margin", () => {
  const s = storeWith("temp", "5", null);
  const c = constraint("c1", [{ measurement: "temp", coeff: rat("1") }], "<=", rat("100"));
  const r = classifyConstraint(c, s);
  assert.equal(r.status, "needs-review");
  assert.equal(r.maxValue, null); // unbounded above
});

test("unknown error with zero coefficient does not block classification", () => {
  const s = new MeasurementStore();
  s.set("known", rat("1"), rat("0.1"));
  s.set("mystery", rat("0"), null);
  const c = constraint(
    "c1",
    [
      { measurement: "known", coeff: rat("1") },
      { measurement: "mystery", coeff: rat("0") },
    ],
    "<=",
    rat("2"),
  );
  assert.equal(classifyConstraint(c, s).status, "determinate-pass");
});

test("linear combination: worst case uses |coeff| * error", () => {
  const s = new MeasurementStore();
  s.set("a", rat("5"), rat("1"));
  s.set("b", rat("3"), rat("0.5"));
  // 2a - b <= 12: centre 7, radius 2*1 + 0.5 = 2.5 -> max 9.5 <= 12 pass
  const c = constraint(
    "c1",
    [
      { measurement: "a", coeff: rat("2") },
      { measurement: "b", coeff: rat("-1") },
    ],
    "<=",
    rat("12"),
  );
  const r = classifyConstraint(c, s);
  assert.equal(r.status, "determinate-pass");
  assert.equal(r.maxValue?.toString(), "19/2");
});

test("negative error bound is rejected", () => {
  const s = new MeasurementStore();
  assert.throws(() => s.set("x", rat("1"), rat("-0.1")));
});
