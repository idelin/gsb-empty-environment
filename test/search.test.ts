import { test } from "node:test";
import assert from "node:assert/strict";
import { searchMinimalCounterexample, spaceSize } from "../src/search.ts";
import type { Pipeline } from "../src/dag.ts";
import { roundA, roundB } from "./helpers.ts";

test("finds the minimal counterexample 1/8 for half-up vs half-even", () => {
  const rep = searchMinimalCounterexample(
    roundA,
    roundB,
    { slots: ["x"], maxNum: 1, maxDen: 200, includeNegative: false, includeZero: false },
    50000
  );
  assert.equal(rep.status, "counterexample-found");
  assert.equal(rep.counterexample?.x, "1/8"); // 0.125: lowest-height tie at 2 places
  assert.equal(rep.compareReport?.firstDivergence?.aValue, "13/100");
  assert.equal(rep.compareReport?.firstDivergence?.bValue, "3/25");
});

test("budget exhaustion is reported as undecided, not as equivalence", () => {
  const rep = searchMinimalCounterexample(
    roundA,
    roundB,
    { slots: ["x"], maxNum: 1, maxDen: 200, includeNegative: false, includeZero: false },
    5
  );
  assert.equal(rep.status, "undecided-budget-exhausted");
  assert.equal(rep.tested, 5);
  assert.equal(rep.totalCandidates, 200);
});

test("equivalent pipelines: finite space fully enumerated, no divergence", () => {
  const assoc1: Pipeline = {
    name: "A",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "y", op: "input", slot: "y" },
      { id: "s1", op: "add", a: "x", b: "y" },
      { id: "s2", op: "mul", a: "s1", b: "s1" },
      { id: "out", op: "output", a: "s2", name: "o" },
    ],
  };
  const assoc2: Pipeline = {
    name: "B",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "y", op: "input", slot: "y" },
      { id: "xx", op: "mul", a: "x", b: "x" },
      { id: "yy", op: "mul", a: "y", b: "y" },
      { id: "xy", op: "mul", a: "x", b: "y" },
      { id: "xy2", op: "add", a: "xy", b: "xy" },
      { id: "s", op: "add", a: "xx", b: "yy" },
      { id: "t", op: "add", a: "s", b: "xy2" },
      { id: "out", op: "output", a: "t", name: "o" },
    ],
  };
  const space = { slots: ["x", "y"], maxNum: 2, maxDen: 2, includeNegative: true, includeZero: true };
  const rep = searchMinimalCounterexample(assoc1, assoc2, space, 100000);
  assert.equal(rep.status, "no-divergence-in-space");
  assert.equal(rep.tested, spaceSize(space) ** 2);
});

test("spaceSize counts reduced rationals", () => {
  // p in 1..2, q in 1..2, reduced: 1/1, 1/2, 2/1 -> 3, signed -> 6, +0 -> 7
  assert.equal(spaceSize({ slots: ["x"], maxNum: 2, maxDen: 2 }), 7);
});
