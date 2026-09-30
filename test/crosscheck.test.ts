import { test } from "node:test";
import assert from "node:assert/strict";
import * as R from "../src/rational.ts";
import { parseProblem } from "../src/model.ts";
import type { ProblemSpec } from "../src/model.ts";
import { solve } from "../src/solver.ts";
import { exhaustive } from "../src/exhaustive.ts";

/** Deterministic seeded PRNG (LCG) — no Math.random, reproducible runs. */
function lcg(seed: number) {
  let s = seed >>> 0;
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 0x100000000;
  };
}

function randomProblem(rand: () => number, index: number): ProblemSpec {
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];
  const sources = ["cal", "tmp"];
  const nComp = 3 + Math.floor(rand() * 2); // 3..4
  const components = Array.from({ length: nComp }, (_, ci) => {
    const nGrades = 2 + Math.floor(rand() * 2); // 2..3
    const sensitivities: Record<string, string> = {};
    for (const s of sources) {
      if (rand() < 0.8) sensitivities[s] = pick(["-2", "-1", "-0.5", "0.5", "1", "1.5", "2"]);
    }
    const grades = Array.from({ length: nGrades }, (_, gi) => {
      const errors: Record<string, [string, string]> = {};
      for (const s of sources) {
        if (rand() < 0.7) {
          const mag = (1 + Math.floor(rand() * 9)) / 100; // 0.01..0.09
          const asym = Math.floor(rand() * 3) / 100;
          errors[s] = [`-${mag}`, `${(mag - asym).toFixed(2)}`];
        }
      }
      // Better grades trend cheaper-error but not monotonically, to make
      // the cheapest-all selection sometimes infeasible.
      const cost = (1 + Math.floor(rand() * 20) + gi * 3).toString();
      return { id: `g${gi}`, cost, errors };
    });
    return { id: `c${ci}`, sensitivities, grades };
  });

  const spec: ProblemSpec = {
    errorLimit: pick(["0.08", "0.12", "0.2", "0.35"]),
    components,
  };
  // Sprinkle constraints on some instances.
  if (index % 3 === 0 && nComp >= 2) {
    spec.exclusions = [{
      a: { component: "c0", grade: "g0" },
      b: { component: "c1", grade: "g0" },
    }];
  }
  if (index % 4 === 0 && nComp >= 3) {
    spec.dependencies = [{
      if: { component: "c1", grade: "g1" },
      requires: { component: "c2", grade: "g0" },
    }];
  }
  return spec;
}

test("branch & bound matches exhaustive enumeration on 60 seeded small instances", () => {
  for (let i = 0; i < 60; i++) {
    const spec = randomProblem(lcg(0xC0FFEE + i * 7919), i);
    const p = parseProblem(spec);
    const r = solve(p);
    const ex = exhaustive(p);
    assert.equal(ex.status, "exhausted-all");
    if (ex.bestCost === null) {
      assert.equal(r.status, "infeasible", `instance ${i}: B&B must agree no feasible solution exists`);
      assert.equal(r.upperBound, null);
    } else {
      assert.equal(r.status, "optimal", `instance ${i}`);
      assert.ok(R.eq(r.upperBound!, ex.bestCost), `instance ${i}: B&B ${R.toCanonical(r.upperBound!)} != exhaustive ${R.toCanonical(ex.bestCost)}`);
      assert.ok(R.eq(r.lowerBound!, ex.bestCost), `instance ${i}: proven LB must equal optimum`);
    }
  }
});
