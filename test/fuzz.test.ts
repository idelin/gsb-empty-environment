import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { solve } from "../src/solver.ts";
import type { Problem } from "../src/model.ts";

interface BruteResult {
  status: "ok" | "contradiction";
  minFaults: number;
  solutions: string[];
}

function bruteForce(problem: Problem): BruteResult {
  const okEdges = new Set<string>();
  for (const observation of problem.observations) {
    if (observation.status === "ok") {
      observation.path.forEach((edge) => okEdges.add(edge));
    }
  }
  for (const observation of problem.observations) {
    if (
      observation.status === "fail" &&
      observation.path.every((edge) => okEdges.has(edge))
    ) {
      return { status: "contradiction", minFaults: 0, solutions: [] };
    }
  }
  const linkCount = problem.links.length;
  let best = Number.POSITIVE_INFINITY;
  let solutions: string[] = [];
  for (let mask = 0; mask < 1 << linkCount; mask += 1) {
    const edges = problem.links.filter((_, i) => (mask & (1 << i)) !== 0);
    if (edges.some((edge) => okEdges.has(edge))) {
      continue;
    }
    const hitsAll = problem.observations
      .filter((observation) => observation.status === "fail")
      .every((observation) =>
        observation.path.some((edge) => edges.includes(edge)),
      );
    if (!hitsAll) {
      continue;
    }
    if (edges.length < best) {
      best = edges.length;
      solutions = [edges.slice().sort().join(",")];
    } else if (edges.length === best) {
      solutions.push(edges.slice().sort().join(","));
    }
  }
  return {
    status: "ok",
    minFaults: best,
    solutions: [...new Set(solutions)].sort(),
  };
}

function bruteMinUnsatCores(problem: Problem): string[] {
  const count = problem.observations.length;
  let minSize = Number.POSITIVE_INFINITY;
  const cores = new Set<string>();
  for (let mask = 1; mask < 1 << count; mask += 1) {
    const subset: Problem = {
      links: problem.links,
      observations: problem.observations.filter(
        (_, i) => (mask & (1 << i)) !== 0,
      ),
    };
    if (bruteForce(subset).status !== "contradiction") {
      continue;
    }
    const size = subset.observations.length;
    if (size < minSize) {
      minSize = size;
      cores.clear();
    }
    if (size === minSize) {
      cores.add(
        subset.observations
          .map((observation) => observation.id)
          .sort()
          .join(","),
      );
    }
  }
  return [...cores].sort();
}

function makeRandom(seedStart: number): () => number {
  let seed = seedStart;
  return () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
}

describe("fuzz against brute force", () => {
  it("matches exhaustive enumeration on random instances", () => {
    const rand = makeRandom(20261005);
    for (let trial = 0; trial < 500; trial += 1) {
      const linkCount = 1 + Math.floor(rand() * 9);
      const links = Array.from({ length: linkCount }, (_, i) => `e${i}`);
      const observationCount = 1 + Math.floor(rand() * 9);
      const observations = Array.from({ length: observationCount }, (_, k) => ({
        id: `o${k}`,
        path: links.filter(() => rand() < 0.5),
        status: (rand() < 0.4 ? "ok" : "fail") as "ok" | "fail",
      }));
      const problem: Problem = { links, observations };
      const got = solve(problem);
      const want = bruteForce(problem);
      assert.equal(
        got.status,
        want.status,
        `status mismatch for ${JSON.stringify(problem)}`,
      );
      if (got.status === "ok" && want.status === "ok") {
        assert.equal(got.minFaults, want.minFaults);
        assert.deepEqual(
          got.solutions.map((s) => s.slice().sort().join(",")).sort(),
          want.solutions,
        );
      } else if (got.status === "contradiction") {
        const gotCores = got.minUnsatObservationSubsets.map((core) =>
          core.slice().sort().join(","),
        );
        assert.deepEqual(
          [...gotCores].sort(),
          bruteMinUnsatCores(problem),
          `core mismatch for ${JSON.stringify(problem)}`,
        );
      }
    }
  });
});
