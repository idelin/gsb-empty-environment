import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import * as R from "../src/rational.ts";
import { analyze, parseProblem } from "../src/model.ts";
import type { ProblemSpec } from "../src/model.ts";
import { solve } from "../src/solver.ts";
import { exhaustive } from "../src/exhaustive.ts";

const sampleSpec = JSON.parse(readFileSync(new URL("../examples/sample.json", import.meta.url), "utf8")) as ProblemSpec;

test("sample: cheapest grades violate the bound; optimum upgrades exactly enough", () => {
  const p = parseProblem(sampleSpec);
  // Cheapest constraint-respecting pick: ref/std, sensor/a, amp/cheap, adc/fast, psu/hi (dep).
  const cheap = new Map([[0, 0], [1, 0], [2, 0], [3, 1], [4, 1]]);
  assert.ok(!analyze(p, cheap).feasible, "cheapest selection must exceed the error bound");

  const r = solve(p);
  assert.equal(r.status, "optimal");
  assert.equal(R.toCanonical(r.upperBound!), "27");
  assert.equal(R.toCanonical(r.lowerBound!), "27"); // proven: LB == UB
  const names = Object.fromEntries([...r.bestAssignment!].map(([ci, gi]) => [p.components[ci].id, p.components[ci].grades[gi].id]));
  assert.deepEqual(names, { ref: "prec", sensor: "b", amp: "mid", adc: "fast", psu: "hi" });
  assert.equal(names.psu, "hi"); // dependency: adc/fast forced psu/hi
  assert.ok(analyze(p, r.bestAssignment!).feasible);
});

test("mutual exclusion is respected and binding", () => {
  const p = parseProblem({
    errorLimit: "0.1",
    components: [
      { id: "a", sensitivities: { s: "1" }, grades: [
        { id: "g1", cost: "1", errors: { s: ["0", "0.04"] } },
        { id: "g2", cost: "5", errors: { s: ["0", "0.01"] } },
      ]},
      { id: "b", sensitivities: { s: "1" }, grades: [
        { id: "h1", cost: "1", errors: { s: ["0", "0.04"] } },
        { id: "h2", cost: "5", errors: { s: ["0", "0.01"] } },
      ]},
    ],
    exclusions: [{ a: { component: "a", grade: "g1" }, b: { component: "b", grade: "h1" } }],
  });
  const r = solve(p);
  assert.equal(r.status, "optimal");
  // g1+h1 (cost 2, error 0.08 <= 0.1) is error-feasible but excluded -> cost 6.
  assert.equal(R.toCanonical(r.upperBound!), "6");
  const ex = exhaustive(p);
  assert.equal(R.toCanonical(ex.bestCost!), "6");
});

test("dependency forces required grade", () => {
  const p = parseProblem({
    errorLimit: "1",
    components: [
      { id: "x", grades: [{ id: "free", cost: "1" }, { id: "locked", cost: "0.5" }] },
      { id: "y", grades: [{ id: "base", cost: "1" }, { id: "pro", cost: "10" }] },
    ],
    dependencies: [{ if: { component: "x", grade: "locked" }, requires: { component: "y", grade: "pro" } }],
  });
  const r = solve(p);
  assert.equal(r.status, "optimal");
  // x/locked (0.5) would force y/pro (10) = 10.5; x/free + y/base = 2 wins.
  assert.equal(R.toCanonical(r.upperBound!), "2");
});

test("budget exhaustion => undecided, bounds reported, optimality NOT claimed", () => {
  const p = parseProblem(sampleSpec);
  const r = solve(p, { maxNodes: 3 });
  assert.equal(r.status, "undecided");
  assert.ok(r.budgetExhausted);
  assert.equal(r.upperBound, null); // no feasible incumbent yet
  assert.ok(r.lowerBound !== null, "a proven frontier lower bound must still be reported");
  // Frontier LB: amp/cheap(2) committed; the exclusion then forbids adc/fast,
  // so adc/slow(6); + ref min 4 + sensor min 3 + psu min 1 = 16.
  assert.equal(R.toCanonical(r.lowerBound!), "16");
  // The reported LB must not exceed the true optimum (27).
  assert.ok(R.cmp(r.lowerBound!, R.parse("27")) <= 0);
});

test("infeasible instance is reported as such, never as optimal", () => {
  const p = parseProblem({
    errorLimit: "0.001",
    components: [
      { id: "a", sensitivities: { s: "1" }, grades: [{ id: "g", cost: "1", errors: { s: ["-0.5", "0.5"] } }] },
    ],
  });
  const r = solve(p);
  assert.equal(r.status, "infeasible");
  assert.equal(r.upperBound, null);
  const ex = exhaustive(p);
  assert.equal(ex.bestCost, null);
});

test("big integers stay exact (no Number round-trip)", () => {
  const big1 = "123456789012345678901234567890";
  const big2 = "98765432109876543210987654321.123456789012";
  const p = parseProblem({
    errorLimit: "0.000000000001",
    components: [
      { id: "a", sensitivities: { s: "1" }, grades: [
        { id: "g1", cost: big1, errors: { s: ["-0.0000000000005", "0.0000000000005"] } },
        { id: "g2", cost: big2, errors: { s: ["-0.0000000000004", "0.0000000000004"] } },
      ]},
      { id: "b", sensitivities: { s: "1" }, grades: [
        { id: "h1", cost: "0.000000000000000001", errors: { s: ["-0.0000000000004", "0.0000000000004"] } },
      ]},
    ],
  });
  const r = solve(p);
  assert.equal(r.status, "optimal");
  // Both grades of a are feasible with h1 (worst 9e-13 resp. 8e-13 <= 1e-12);
  // g2 is cheaper, so the optimum is big2 + 1e-18 — computed exactly.
  const expected = R.add(R.parse(big2), R.parse("0.000000000000000001"));
  assert.equal(R.toCanonical(r.upperBound!), R.toCanonical(expected));
  // And the canonical form is the exact 41-digit numerator over 10^18:
  assert.equal(R.toCanonical(r.upperBound!), "98765432109876543210987654321123456789012000001/1000000000000000000");
});
