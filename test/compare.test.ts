import { test } from "node:test";
import assert from "node:assert/strict";
import { comparePipelines } from "../src/compare.ts";
import type { Pipeline } from "../src/dag.ts";
import { Rational } from "../src/rational.ts";
import { SI_TABLE } from "../src/units.ts";
import { meanA, meanB, roundA, roundB } from "./helpers.ts";

test("first divergence is the rounding node, with exact values", () => {
  const rep = comparePipelines(roundA, roundB, { x: "0.125" });
  assert.equal(rep.status, "output-divergence");
  assert.equal(rep.firstDivergence?.aId, "r");
  assert.equal(rep.firstDivergence?.aValue, "13/100");
  assert.equal(rep.firstDivergence?.bValue, "3/25");
  assert.equal(rep.firstDivergence?.delta, "-1/100");
  assert.equal(rep.rootCauses.length, 1);
  assert.equal(rep.rootCauses[0].kind, "rounding");
  // exact difference chain root -> output
  const chain = rep.differenceChains.find((c) => c.output === "total");
  assert.ok(chain);
  assert.deepEqual(chain.steps.map((s) => s.aId), ["r", "out"]);
  assert.equal(chain.steps[1].bValue, "3/25");
});

test("identical pipelines are identical", () => {
  const rep = comparePipelines(roundA, roundA, { x: "0.125" });
  assert.equal(rep.status, "identical");
  assert.equal(rep.rootCauses.length, 0);
});

test("denominator convention /N vs /(N-1) is classified, DAG re-associated", () => {
  const rep = comparePipelines(meanA, meanB, { a: "1", b: "2", c: "4" });
  assert.equal(rep.status, "output-divergence");
  assert.equal(rep.rootCauses.length, 1);
  assert.equal(rep.rootCauses[0].kind, "denominator");
  assert.equal(rep.firstDivergence?.aId, "n");
  assert.equal(rep.divergentOutputs[0].aValue, "7/3");
  assert.equal(rep.divergentOutputs[0].bValue, "7/2");
  // equivalent-but-different sum groupings are aligned, not flagged
  assert.deepEqual(rep.unmatchedA, ["s1"]);
  assert.deepEqual(rep.unmatchedB, ["s"]);
});

test("final equal but intermediates differ is recorded", () => {
  const a: Pipeline = {
    name: "A",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "r1", op: "round", a: "x", places: 2, mode: "half-up" },
      { id: "r2", op: "round", a: "r1", places: 2, mode: "half-up" },
      { id: "out", op: "output", a: "r2", name: "o" },
    ],
  };
  const b: Pipeline = {
    name: "B",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "r1", op: "round", a: "x", places: 3, mode: "half-up" },
      { id: "r2", op: "round", a: "r1", places: 2, mode: "half-up" },
      { id: "out", op: "output", a: "r2", name: "o" },
    ],
  };
  const rep = comparePipelines(a, b, { x: "0.1244" });
  assert.equal(rep.status, "intermediate-divergence");
  assert.equal(rep.divergentOutputs.length, 0);
  assert.equal(rep.firstDivergence?.aId, "r1");
  assert.equal(rep.firstDivergence?.aValue, "3/25"); // 0.12
  assert.equal(rep.firstDivergence?.bValue, "31/250"); // 0.124
});

test("missing intermediate evidence -> cannot-locate", () => {
  const rep = comparePipelines(roundA, roundB, { x: "0.125" }, {
    keepTraceB: (id) => id === "x" || id === "out",
  });
  assert.equal(rep.status, "cannot-locate");
  assert.deepEqual(rep.missingEvidence, [{ aId: "r", bId: "r" }]);
  assert.equal(rep.divergentOutputs.length, 1);
});

test("unit dimension error is never masked by numeric closeness", () => {
  const a: Pipeline = {
    name: "A",
    nodes: [
      { id: "d", op: "input", slot: "d", unit: "m" },
      { id: "t", op: "input", slot: "t", unit: "s" },
      { id: "bad", op: "add", a: "d", b: "t" }, // m + s: nonsense
      { id: "out", op: "output", a: "bad", name: "o" },
    ],
  };
  const b: Pipeline = {
    name: "B",
    nodes: [
      { id: "d", op: "input", slot: "d", unit: "m" },
      { id: "t", op: "input", slot: "t", unit: "s" },
      { id: "ok", op: "add", a: "d", b: "d" }, // m + m
      { id: "out", op: "output", a: "ok", name: "o" },
    ],
  };
  // choose d,t so the NUMBERS coincide: 1 + 1 == 1 + 1
  const rep = comparePipelines(a, b, { d: "1", t: "1" });
  // values are equal but A carries a unit error: must be flagged
  assert.notEqual(rep.status, "identical");
  assert.equal(rep.firstDivergence?.unitError, "add: dimension mismatch m^1 vs s^1");
});

test("different conversion factors are a unit-convention root cause", () => {
  const approxTable = {
    ...SI_TABLE,
    in: { name: "in", dims: { m: 1 }, scale: Rational.parse("1/40") },
  };
  const a: Pipeline = {
    name: "A(exact)",
    nodes: [
      { id: "x", op: "input", slot: "x", unit: "in" },
      { id: "cm", op: "convert", a: "x", unit: "cm" },
      { id: "out", op: "output", a: "cm", name: "len" },
    ],
  };
  const b: Pipeline = { ...a, name: "B(approx)", unitTable: approxTable };
  const rep = comparePipelines(a, b, { x: "12" });
  assert.equal(rep.status, "output-divergence");
  assert.equal(rep.rootCauses.length, 1);
  assert.equal(rep.rootCauses[0].kind, "unit-conversion-factor");
  assert.equal(rep.firstDivergence?.aValue, "381/1250"); // 12 * 127/5000
  assert.equal(rep.firstDivergence?.bValue, "3/10"); // 12 * 1/40
});
