import { test } from "node:test";
import assert from "node:assert/strict";
import * as R from "../src/rational.ts";
import { analyze, parseProblem } from "../src/model.ts";

test("shared calibration source: correlated bound is tight, not an independent abs-sum", () => {
  // ref and sensor share source "cal" with opposite sensitivities and
  // asymmetric intervals, so their errors partially cancel.
  const p = parseProblem({
    errorLimit: "0.05",
    components: [
      { id: "ref", sensitivities: { cal: "1" }, grades: [{ id: "g", cost: "1", errors: { cal: ["-0.04", "0.01"] } }] },
      { id: "sen", sensitivities: { cal: "-1" }, grades: [{ id: "g", cost: "1", errors: { cal: ["-0.03", "0.01"] } }] },
    ],
  });
  const a = analyze(p, new Map([[0, 0], [1, 0]]));
  // Correlated: [-0.04,0.01] + (-1)*[-0.03,0.01] = [-0.05, 0.04] -> worst 0.05 (feasible).
  assert.equal(R.toCanonical(a.perSource[0].interval.lo), "-1/20");
  assert.equal(R.toCanonical(a.perSource[0].interval.hi), "1/25");
  assert.equal(R.toCanonical(a.worst), "1/20");
  assert.ok(a.feasible);
  // A naive independent absolute-value sum would claim 0.04+0.03 = 0.07 > limit.
  // That bound is not tight and is never what we compute.
});

test("negative sensitivity contributes by absolute value", () => {
  const p = parseProblem({
    errorLimit: "1",
    components: [
      { id: "x", sensitivities: { s: "-2" }, grades: [{ id: "g", cost: "1", errors: { s: ["-0.1", "0.3"] } }] },
    ],
  });
  const a = analyze(p, new Map([[0, 0]]));
  // -2 * [-0.1, 0.3] = [-0.6, 0.2]; worst |.| = 0.6 = |-2| * 0.3.
  assert.equal(R.toCanonical(a.total.lo), "-3/5");
  assert.equal(R.toCanonical(a.total.hi), "1/5");
  assert.equal(R.toCanonical(a.worst), "3/5");
});

test("independent sources add as intervals", () => {
  const p = parseProblem({
    errorLimit: "0.09",
    components: [
      { id: "a", sensitivities: { s1: "1" }, grades: [{ id: "g", cost: "1", errors: { s1: ["-0.05", "0.05"] } }] },
      { id: "b", sensitivities: { s2: "1" }, grades: [{ id: "g", cost: "1", errors: { s2: ["-0.04", "0.04"] } }] },
    ],
  });
  const a = analyze(p, new Map([[0, 0], [1, 0]]));
  assert.equal(R.toCanonical(a.worst), "9/100");
  assert.ok(a.feasible); // exactly at the limit
});

test("boundary: worst == limit is feasible", () => {
  const p = parseProblem({
    errorLimit: "0.09",
    components: [
      { id: "a", sensitivities: { s1: "1" }, grades: [{ id: "g", cost: "1", errors: { s1: ["-0.09", "0.09"] } }] },
    ],
  });
  assert.ok(analyze(p, new Map([[0, 0]])).feasible);
});

test("validation rejects bad references and inverted intervals", () => {
  assert.throws(() =>
    parseProblem({
      errorLimit: "1",
      components: [{ id: "a", grades: [{ id: "g", cost: "1", errors: { s: ["0.5", "-0.5"] } }] }],
    }),
  );
  assert.throws(() =>
    parseProblem({
      errorLimit: "1",
      components: [{ id: "a", grades: [{ id: "g", cost: "1" }] }],
      exclusions: [{ a: { component: "a", grade: "g" }, b: { component: "a", grade: "nope" } }],
    }),
  );
});
