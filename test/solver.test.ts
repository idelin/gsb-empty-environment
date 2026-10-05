import assert from "node:assert/strict";
import test from "node:test";
import { enumerateMinHittingSets, InputError, solve } from "../src/solver.ts";

function popcount(x: number): number {
  let c = 0;
  while (x !== 0) {
    x &= x - 1;
    c++;
  }
  return c;
}

test("single failed path, no ok observations: any one edge explains it", () => {
  const result = solve({
    topology: { edges: [["A", "B"], ["B", "C"], ["C", "D"]] },
    observations: [{ id: "p1", path: ["A", "B", "C", "D"], result: "fail" }],
  });
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.minFaultEdges, 1);
  assert.deepEqual(result.solutions, [["e0"], ["e1"], ["e2"]]);
});

test("ok observation forces edges healthy, shrinking candidates", () => {
  const result = solve({
    topology: { edges: [["A", "B"], ["B", "C"], ["A", "C"]] },
    observations: [
      { id: "good", path: ["A", "B"], result: "ok" },
      { id: "bad", path: ["A", "B", "C"], result: "fail" },
    ],
  });
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.minFaultEdges, 1);
  assert.deepEqual(result.solutions, [["e1"]]);
});

test("shared edge covers two failed paths", () => {
  const result = solve({
    topology: { edges: [["A", "B"], ["B", "C"], ["C", "D"], ["D", "E"]] },
    observations: [
      { id: "p1", path: ["A", "B", "C", "D"], result: "fail" },
      { id: "p2", path: ["B", "C", "D", "E"], result: "fail" },
    ],
  });
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.minFaultEdges, 1);
  assert.deepEqual(result.solutions, [["e1"], ["e2"]]);
});

test("disjoint failed paths need one edge each, all ties listed", () => {
  const result = solve({
    topology: { edges: [["A", "B"], ["B", "C"], ["D", "E"]] },
    observations: [
      { id: "p1", path: ["A", "B", "C"], result: "fail" },
      { id: "p2", path: ["D", "E"], result: "fail" },
    ],
  });
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.minFaultEdges, 2);
  assert.deepEqual(result.solutions, [["e0", "e2"], ["e1", "e2"]]);
});

test("no observations at all: empty fault set", () => {
  const result = solve({
    topology: { edges: [["A", "B"]] },
    observations: [],
  });
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.minFaultEdges, 0);
  assert.deepEqual(result.solutions, [[]]);
});

test("contradiction: failed path fully covered by one ok observation", () => {
  const result = solve({
    topology: { edges: [["A", "B"], ["B", "C"]] },
    observations: [
      { id: "good", path: ["A", "B", "C"], result: "ok" },
      { id: "bad", path: ["A", "B", "C"], result: "fail" },
    ],
  });
  assert.equal(result.status, "contradictory");
  if (result.status !== "contradictory") return;
  assert.equal(result.minUnsatisfiableCoreSize, 2);
  assert.deepEqual(result.cores, [["bad", "good"]]);
});

test("contradiction: minimum core needs two ok observations", () => {
  const result = solve({
    topology: { edges: [["A", "B"], ["B", "C"], ["C", "D"]] },
    observations: [
      { id: "ok1", path: ["A", "B"], result: "ok" },
      { id: "ok2", path: ["C", "D"], result: "ok" },
      { id: "ok3", path: ["B", "C"], result: "ok" },
      { id: "bad", path: ["A", "B", "C", "D"], result: "fail" },
    ],
  });
  assert.equal(result.status, "contradictory");
  if (result.status !== "contradictory") return;
  assert.equal(result.minUnsatisfiableCoreSize, 4);
  assert.deepEqual(result.cores, [["bad", "ok1", "ok2", "ok3"]]);
});

test("contradiction: smallest core wins among several violated failures", () => {
  const result = solve({
    topology: { edges: [["A", "B"], ["B", "C"], ["C", "D"]] },
    observations: [
      { id: "ok1", path: ["A", "B"], result: "ok" },
      { id: "ok2", path: ["B", "C"], result: "ok" },
      { id: "bad1", path: ["A", "B"], result: "fail" },
      { id: "bad2", path: ["A", "B", "C"], result: "fail" },
    ],
  });
  assert.equal(result.status, "contradictory");
  if (result.status !== "contradictory") return;
  assert.equal(result.minUnsatisfiableCoreSize, 2);
  assert.deepEqual(result.cores, [["bad1", "ok1"]]);
});

test("contradiction: failed observation with single-node path is a core of size 1", () => {
  const result = solve({
    topology: { edges: [["A", "B"]] },
    observations: [{ id: "bad", path: ["A"], result: "fail" }],
  });
  assert.equal(result.status, "contradictory");
  if (result.status !== "contradictory") return;
  assert.equal(result.minUnsatisfiableCoreSize, 1);
  assert.deepEqual(result.cores, [["bad"]]);
});

test("triangle of two-hop failed paths: any two edges explain all", () => {
  const result = solve({
    topology: { edges: [["A", "B"], ["B", "C"], ["C", "A"]] },
    observations: [
      { id: "p1", path: ["A", "B", "C"], result: "fail" },
      { id: "p2", path: ["B", "C", "A"], result: "fail" },
      { id: "p3", path: ["C", "A", "B"], result: "fail" },
    ],
  });
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.minFaultEdges, 2);
  assert.deepEqual(result.solutions, [["e0", "e1"], ["e0", "e2"], ["e1", "e2"]]);
});

test("every edge individually failed: unique maximal solution", () => {
  const nodes = ["A", "B", "C", "D", "E"];
  const edges: [string, string][] = [];
  const observations = [];
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      edges.push([nodes[i], nodes[j]]);
      observations.push({ id: `p${i}${j}`, path: [nodes[i], nodes[j]], result: "fail" as const });
    }
  }
  const result = solve({ topology: { edges }, observations });
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.minFaultEdges, 10);
  assert.equal(result.solutionCount, 1);
});

test("input validation errors", () => {
  assert.throws(() => solve(null), InputError);
  assert.throws(
    () => solve({ topology: { edges: [["A", "B"]] }, observations: [{ id: "x", path: ["A", "C"], result: "fail" }] }),
    /no edge between "A" and "C"/,
  );
  assert.throws(
    () => solve({ topology: { edges: [["A", "B"], ["B", "A"]] }, observations: [] }),
    /duplicate edge/,
  );
  assert.throws(
    () => solve({ topology: { edges: [["A", "B"]] }, observations: [{ id: "x", path: ["A"], result: "bad" }] }),
    /must be "ok" or "fail"/,
  );
  const tooMany: [string, string][] = Array.from({ length: 26 }, (_, i) => [`N${i}`, `N${i + 1}`]);
  assert.throws(
    () => solve({ topology: { edges: tooMany }, observations: [] }),
    /maximum supported is 25/,
  );
});

test("hitting set solver matches brute force on random instances", () => {
  let seed = 0x2f6e2b1;
  const rand = () => {
    seed ^= seed << 13;
    seed ^= seed >>> 17;
    seed ^= seed << 5;
    return (seed >>> 0) / 0x100000000;
  };
  for (let trial = 0; trial < 300; trial++) {
    const numEdges = 1 + Math.floor(rand() * 8);
    const numSets = 1 + Math.floor(rand() * 10);
    const sets: number[] = [];
    for (let s = 0; s < numSets; s++) {
      let mask = 0;
      for (let e = 0; e < numEdges; e++) {
        if (rand() < 0.4) mask |= 1 << e;
      }
      if (mask !== 0) sets.push(mask);
    }
    if (sets.length === 0) continue;

    const { size, solutions } = enumerateMinHittingSets(sets, numEdges);

    let bruteBest = Number.POSITIVE_INFINITY;
    const bruteSolutions: number[] = [];
    for (let mask = 0; mask < 1 << numEdges; mask++) {
      if (!sets.every((s) => (s & mask) !== 0)) continue;
      const pc = popcount(mask);
      if (pc < bruteBest) {
        bruteBest = pc;
        bruteSolutions.length = 0;
      }
      if (pc === bruteBest) bruteSolutions.push(mask);
    }
    bruteSolutions.sort((a, b) => a - b);

    assert.equal(size, bruteBest, `trial ${trial}: size mismatch`);
    assert.deepEqual(solutions, bruteSolutions, `trial ${trial}: solutions mismatch`);
  }
});

test("unsat cores match brute force on random instances", () => {
  let seed = 0x1b873593;
  const rand = () => {
    seed ^= seed << 13;
    seed ^= seed >>> 17;
    seed ^= seed << 5;
    return (seed >>> 0) / 0x100000000;
  };

  for (let trial = 0; trial < 200; trial++) {
    // Random path graph with 3-6 edges; observations are random subpaths.
    const numEdges = 3 + Math.floor(rand() * 4);
    const nodes = Array.from({ length: numEdges + 1 }, (_, i) => `N${i}`);
    const edges: [string, string][] = nodes.slice(0, -1).map((n, i) => [n, nodes[i + 1]]);

    const observations: { id: string; path: string[]; result: "ok" | "fail" }[] = [];
    const numObs = 2 + Math.floor(rand() * 5);
    for (let k = 0; k < numObs; k++) {
      const a = Math.floor(rand() * nodes.length);
      const b = Math.floor(rand() * nodes.length);
      const [lo, hi] = a <= b ? [a, b] : [b, a];
      observations.push({
        id: `o${k}`,
        path: nodes.slice(lo, hi + 1),
        result: rand() < 0.5 ? "ok" : "fail",
      });
    }

    const result = solve({ topology: { edges }, observations });

    // Brute force over observation subsets.
    const maskOf = (obs: (typeof observations)[number]) => {
      let m = 0;
      for (let i = 1; i < obs.path.length; i++) {
        const u = obs.path[i - 1];
        const v = obs.path[i];
        const idx = edges.findIndex(
          ([a, b]) => (a === u && b === v) || (a === v && b === u),
        );
        m |= 1 << idx;
      }
      return m;
    };
    const satisfiable = (subset: number[]) => {
      let healthy = 0;
      for (const i of subset) {
        if (observations[i].result === "ok") healthy |= maskOf(observations[i]);
      }
      return subset.every(
        (i) => observations[i].result === "ok" || (maskOf(observations[i]) & ~healthy) !== 0,
      );
    };
    let bruteBest = Number.POSITIVE_INFINITY;
    const bruteCores: string[][] = [];
    for (let m = 0; m < 1 << observations.length; m++) {
      const subset: number[] = [];
      for (let i = 0; i < observations.length; i++) {
        if (m & (1 << i)) subset.push(i);
      }
      if (satisfiable(subset)) continue;
      // minimal by inclusion?
      const isMinimal = subset.every((i) =>
        satisfiable(subset.filter((j) => j !== i)),
      );
      if (!isMinimal) continue;
      if (subset.length < bruteBest) {
        bruteBest = subset.length;
        bruteCores.length = 0;
      }
      if (subset.length === bruteBest) {
        bruteCores.push(subset.map((i) => observations[i].id).sort());
      }
    }
    bruteCores.sort((a, b) => a.join("").localeCompare(b.join("")));

    if (bruteBest === Number.POSITIVE_INFINITY) {
      assert.equal(result.status, "ok", `trial ${trial}: expected ok`);
      // Verify every reported solution actually satisfies all observations.
      if (result.status !== "ok") continue;
      assert.ok(result.solutionCount > 0);
      for (const sol of result.solutions) {
        assert.equal(sol.length, result.minFaultEdges);
        const faulty = new Set(sol);
        for (const obs of observations) {
          const m = maskOf(obs);
          const onFault = edges.some((_, idx) => faulty.has(`e${idx}`) && (m & (1 << idx)) !== 0);
          if (obs.result === "fail") assert.ok(onFault, `trial ${trial}: ${obs.id} not explained`);
          else assert.ok(!onFault, `trial ${trial}: ${obs.id} covers a faulty edge`);
        }
      }
    } else {
      assert.equal(result.status, "contradictory", `trial ${trial}: expected contradiction`);
      if (result.status !== "contradictory") continue;
      assert.equal(result.minUnsatisfiableCoreSize, bruteBest, `trial ${trial}: core size`);
      assert.deepEqual(result.cores, bruteCores, `trial ${trial}: cores`);
    }
  }
});
