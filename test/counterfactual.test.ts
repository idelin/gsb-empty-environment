import { test } from "node:test";
import assert from "node:assert/strict";
import { comparePipelines } from "../src/compare.ts";
import {
  applySubstitution,
  runCounterfactuals,
  compareAndConfirm,
} from "../src/counterfactual.ts";
import { evaluate } from "../src/dag.ts";
import { meanA, meanB, roundA, roundB } from "./helpers.ts";

test("substituting B's rounding rule into A makes outputs exactly equal", () => {
  const rep = comparePipelines(roundA, roundB, { x: "0.125" });
  const cfs = runCounterfactuals(roundA, roundB, { x: "0.125" }, rep.rootCauses);
  assert.equal(cfs.length, 1);
  assert.equal(cfs[0].confirmed, true);
  assert.equal(cfs[0].outputsAfter?.total.modifiedA, "3/25");
});

test("a non-cause substitution does NOT confirm", () => {
  // change A's places instead of its mode: outputs must still differ
  const modified = applySubstitution(roundA, {
    type: "rounding",
    nodeId: "r",
    places: 3,
    mode: "half-up",
  });
  const ea = evaluate(modified, { x: "0.125" });
  const eb = evaluate(roundB, { x: "0.125" });
  assert.equal(ea.outputs.get("total")!.value.toString(), "1/8"); // 0.125 unchanged at 3 places
  assert.equal(eb.outputs.get("total")!.value.toString(), "3/25");
});

test("denominator counterfactual confirms /N vs /(N-1)", () => {
  const { report, counterfactuals } = compareAndConfirm(meanA, meanB, {
    a: "1",
    b: "2",
    c: "4",
  });
  assert.equal(report.rootCauses[0].kind, "denominator");
  const cf = counterfactuals.find((c) => c.cause.kind === "denominator");
  assert.equal(cf?.confirmed, true);
  assert.equal(cf?.outputsAfter?.mean.modifiedA, "7/2");
});

test("bypass-round counterfactual for a structural extra stage", () => {
  const withRound = {
    name: "A",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "r", op: "round", a: "x", places: 0, mode: "half-up" as const },
      { id: "out", op: "output", a: "r", name: "o" },
    ],
  };
  const plain = {
    name: "B",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "out", op: "output", a: "x", name: "o" },
    ],
  };
  const rep = comparePipelines(withRound, plain, { x: "2.5" });
  assert.equal(rep.status, "output-divergence");
  const structural = rep.rootCauses.find((c) => c.kind === "structural");
  assert.ok(structural);
  const cfs = runCounterfactuals(withRound, plain, { x: "2.5" }, [structural]);
  assert.equal(cfs[0].confirmed, true);
  assert.equal(cfs[0].outputsAfter?.o.modifiedA, "5/2");
});
