import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import assert from "node:assert/strict";
import { solve } from "../src/allocator.ts";
import { solveExhaustive } from "../src/exhaustive.ts";
import { generateInstance, mulberry32 } from "../src/synth.ts";
import type { AllocationResult, Problem } from "../src/types.ts";

const CLI = new URL("../src/cli.ts", import.meta.url).pathname;

function greedyNearestAdmitted(problem: Problem): number {
  const remaining = new Map(problem.nodes.map((n) => [n.id, n.capacity]));
  let admitted = 0;
  for (const request of problem.requests) {
    const candidates = problem.nodes
      .filter((n) => remaining.get(n.id)! >= request.demand)
      .sort(
        (a, b) =>
          problem.costMatrix[request.region][a.region] -
            problem.costMatrix[request.region][b.region] || (a.id < b.id ? -1 : 1),
      );
    const pick = candidates[0];
    if (pick) {
      remaining.set(pick.id, remaining.get(pick.id)! - request.demand);
      admitted += request.demand;
    }
  }
  return admitted;
}

function assertFeasible(problem: Problem, result: AllocationResult): void {
  const nodeById = new Map(problem.nodes.map((n) => [n.id, n]));
  const used = new Map(problem.nodes.map((n) => [n.id, 0]));
  let admitted = 0;
  for (const request of problem.requests) {
    const target = result.assignment[request.id];
    if (target === null) {
      assert.ok(result.rejected.includes(request.id), `${request.id} must be listed as rejected`);
      continue;
    }
    const node = nodeById.get(target);
    assert.ok(node, `assignment targets a live node (${request.id} -> ${target})`);
    used.set(target, used.get(target)! + request.demand);
    admitted += request.demand;
  }
  for (const [nodeId, load] of used) {
    assert.ok(load <= nodeById.get(nodeId)!.capacity, `node ${nodeId} is not over capacity`);
    assert.equal(result.ledger[nodeId].used, load, `ledger matches actual load on ${nodeId}`);
    assert.equal(
      result.ledger[nodeId].remaining,
      nodeById.get(nodeId)!.capacity - load,
      `ledger remaining matches on ${nodeId}`,
    );
  }
  assert.equal(result.stats.admittedDemand, admitted);
  assert.equal(result.rejected.length, new Set(result.rejected).size);
}

function shuffled<T>(items: T[], rand: () => number): T[] {
  const copy = [...items];
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [copy[i], copy[j]] = [copy[j], copy[i]];
  }
  return copy;
}

test("branch and bound beats greedy nearest-node on admitted demand", () => {
  // Fragmentation: greedy fills nA with r1, pushes r2 cross-region into nB,
  // and then r3 (demand 8) fits nowhere. Optimal admits 13, greedy 10.
  const problem: Problem = {
    requests: [
      { id: "r1", region: "R", demand: 5 },
      { id: "r2", region: "R", demand: 5 },
      { id: "r3", region: "S", demand: 8 },
    ],
    nodes: [
      { id: "nA", region: "R", capacity: 8 },
      { id: "nB", region: "S", capacity: 8 },
    ],
    costMatrix: {
      R: { R: 0, S: 1 },
      S: { R: 1, S: 0 },
    },
  };
  assert.equal(greedyNearestAdmitted(problem), 10);
  const result = solve(problem, { budget: 100_000 });
  assert.equal(result.optimal, true);
  assert.equal(result.stats.admittedDemand, 13);
  assert.ok(result.stats.admittedDemand > greedyNearestAdmitted(problem));
  // Tie-break keeps the smallest-id request admitted and local.
  assert.deepEqual(result.assignment, { r1: "nA", r2: null, r3: "nB" });
  assert.deepEqual(result.rejected, ["r2"]);
  assertFeasible(problem, result);
});

test("a request larger than every node is rejected, others still admitted", () => {
  const problem: Problem = {
    requests: [
      { id: "big", region: "R", demand: 10 },
      { id: "small", region: "R", demand: 3 },
    ],
    nodes: [
      { id: "n1", region: "R", capacity: 5 },
      { id: "n2", region: "S", capacity: 5 },
    ],
    costMatrix: {
      R: { R: 0, S: 2 },
      S: { R: 2, S: 0 },
    },
  };
  const result = solve(problem, { budget: 100_000 });
  assert.equal(result.optimal, true);
  assert.deepEqual(result.rejected, ["big"]);
  assert.equal(result.assignment.big, null);
  assert.equal(result.assignment.small, "n1");
  assert.equal(result.ledger.n1.remaining, 2);
  assert.equal(result.stats.admittedDemand, 3);
  assertFeasible(problem, result);
});

test("equal cross-region cost is decided by the node-id vector", () => {
  const problem: Problem = {
    requests: [{ id: "r1", region: "R", demand: 3 }],
    nodes: [
      { id: "nb", region: "S", capacity: 10 },
      { id: "na", region: "S", capacity: 10 },
    ],
    costMatrix: {
      R: { S: 4 },
      S: { S: 0 },
    },
  };
  const result = solve(problem, { budget: 100_000 });
  assert.equal(result.optimal, true);
  assert.equal(result.stats.crossRegionCost, 4);
  assert.equal(result.assignment.r1, "na", "smaller node id wins the tie");

  const shuffledInput: Problem = {
    ...problem,
    nodes: [...problem.nodes].reverse(),
  };
  assert.deepEqual(solve(shuffledInput, { budget: 100_000 }), result);
});

test("node exit forces a full recompute and a migration plan", () => {
  const costMatrix = {
    R: { R: 0, S: 3 },
    S: { R: 3, S: 0 },
  };
  const requests = [
    { id: "r1", region: "R", demand: 4 },
    { id: "r2", region: "S", demand: 4 },
  ];
  const previous = { r1: "n1", r2: "n2" };

  const before = solve(
    {
      requests,
      nodes: [
        { id: "n1", region: "R", capacity: 10 },
        { id: "n2", region: "S", capacity: 10 },
      ],
      costMatrix,
      previous,
    },
    { budget: 100_000 },
  );
  assert.equal(before.stats.migratedCount, 0, "no churn when nothing changed");
  assert.deepEqual(before.migrations, []);

  const after = solve(
    {
      requests,
      nodes: [{ id: "n1", region: "R", capacity: 10 }], // n2 went offline
      costMatrix,
      previous,
    },
    { budget: 100_000 },
  );
  assert.equal(after.optimal, true);
  assert.deepEqual(after.assignment, { r1: "n1", r2: "n1" });
  assert.deepEqual(after.migrations, [{ requestId: "r2", from: "n2", to: "n1" }]);
  assert.equal(after.stats.migratedCount, 1);
  assert.equal(after.stats.crossRegionCost, 3);
  assert.equal(after.ledger.n1.used, 8);
  assert.ok(after.ledger.n1.remaining >= 0);
});

test("capacity shrink recomputes the whole batch without over-allocation", () => {
  const problem: Problem = {
    requests: [
      { id: "r1", region: "R", demand: 6 },
      { id: "r2", region: "R", demand: 5 },
    ],
    nodes: [
      { id: "n1", region: "R", capacity: 8 }, // used to be 16
      { id: "n2", region: "S", capacity: 8 },
    ],
    costMatrix: {
      R: { R: 0, S: 5 },
      S: { R: 5, S: 0 },
    },
    previous: { r1: "n1", r2: "n1" },
  };
  const result = solve(problem, { budget: 100_000 });
  assert.equal(result.optimal, true);
  assert.equal(result.stats.admittedDemand, 11);
  // Both fit only by spilling one request cross-region; r1 stays put (fewer
  // migrations tie at 1 either way, node-id vector prefers r1 on n1).
  assert.deepEqual(result.assignment, { r1: "n1", r2: "n2" });
  assert.deepEqual(result.migrations, [{ requestId: "r2", from: "n1", to: "n2" }]);
  assertFeasible(problem, result);
});

test("results are identical under input permutations", () => {
  const rand = mulberry32(42);
  for (let seed = 1; seed <= 10; seed++) {
    const problem = generateInstance(seed, {
      requestCount: 7,
      nodeCount: 3,
      regionCount: 3,
      withPrevious: true,
      dropLastNode: seed % 2 === 0,
    });
    const baseline = solve(problem, { budget: 1_000_000 });
    const permuted: Problem = {
      requests: shuffled(problem.requests, rand),
      nodes: shuffled(problem.nodes, rand),
      costMatrix: problem.costMatrix,
      previous: problem.previous,
    };
    const rerun = solve(permuted, { budget: 1_000_000 });
    assert.deepEqual(rerun, baseline, `seed ${seed} is permutation invariant`);
  }
});

test("bounded search matches exhaustive search on small instances (<=10 requests)", () => {
  for (let seed = 1; seed <= 25; seed++) {
    const problem = generateInstance(seed, {
      requestCount: 1 + (seed % 8),
      nodeCount: 1 + (seed % 3),
      regionCount: 2 + (seed % 2),
      withPrevious: true,
      dropLastNode: seed % 3 === 0,
    });
    const fast = solve(problem, { budget: 10_000_000 });
    const reference = solveExhaustive(problem);
    assert.equal(fast.optimal, true, `seed ${seed} solved to proven optimality`);
    assert.deepEqual(fast.assignment, reference.assignment, `seed ${seed} assignment`);
    assert.equal(fast.stats.admittedDemand, reference.stats.admittedDemand);
    assert.equal(fast.stats.crossRegionCost, reference.stats.crossRegionCost);
    assert.equal(fast.stats.migratedCount, reference.stats.migratedCount);
    assertFeasible(problem, fast);
  }

  const ten = generateInstance(99, {
    requestCount: 10,
    nodeCount: 3,
    regionCount: 2,
    withPrevious: true,
  });
  const fast = solve(ten, { budget: 10_000_000 });
  const reference = solveExhaustive(ten);
  assert.equal(fast.optimal, true);
  assert.deepEqual(fast.assignment, reference.assignment);
});

test("exhausted budget returns a feasible plan flagged as not proven optimal", () => {
  const problem = generateInstance(1234, {
    requestCount: 12,
    nodeCount: 3,
    regionCount: 3,
    withPrevious: true,
  });
  const interrupted = solve(problem, { budget: 5 });
  assert.equal(interrupted.optimal, false);
  assert.equal(interrupted.stats.budgetExhausted, true);
  assert.ok(interrupted.stats.searchNodes <= 6);
  assertFeasible(problem, interrupted);

  const proven = solve(problem, { budget: 50_000_000 });
  assert.equal(proven.optimal, true);
  assert.ok(
    interrupted.stats.admittedDemand <= proven.stats.admittedDemand,
    "interrupted plan never claims more demand than the proven optimum",
  );

  const zeroBudget = solve(problem, { budget: 0 });
  assert.equal(zeroBudget.optimal, false);
  assertFeasible(problem, zeroBudget);
  assert.equal(zeroBudget.stats.admittedDemand, 0, "falls back to the all-rejected plan");
});

test("empty and degenerate inputs stay well-defined", () => {
  const costMatrix = { R: { R: 0 } };
  const noRequests = solve({ requests: [], nodes: [{ id: "n1", region: "R", capacity: 3 }], costMatrix });
  assert.equal(noRequests.optimal, true);
  assert.equal(noRequests.stats.admittedDemand, 0);

  const noNodes = solve({
    requests: [{ id: "r1", region: "R", demand: 1 }],
    nodes: [],
    costMatrix: { R: {} },
  });
  assert.equal(noNodes.optimal, true, "all-rejected is trivially optimal");
  assert.deepEqual(noNodes.rejected, ["r1"]);
});

test("CLI solves a file, verifies exhaustively, and demos synthetic data", () => {
  const dir = mkdtempSync(join(tmpdir(), "alloc-test-"));
  const input = join(dir, "input.json");
  const problem = generateInstance(7, { requestCount: 6, nodeCount: 2, withPrevious: true });
  writeFileSync(input, JSON.stringify({ ...problem, budget: 1_000_000 }));

  const out = execFileSync(process.execPath, [CLI, "solve", input, "--exhaustive"], {
    encoding: "utf8",
  });
  const parsed = JSON.parse(out);
  assert.equal(parsed.optimal, true);
  assert.equal(parsed.verification.checked, true);
  assert.equal(parsed.verification.matches, true);
  assert.ok(parsed.ledger);
  assert.ok(Array.isArray(parsed.rejected));
  assert.ok(Array.isArray(parsed.migrations));

  const demo = execFileSync(process.execPath, [CLI, "demo", "--seed", "3"], {
    encoding: "utf8",
  });
  const demoProblem = JSON.parse(demo);
  assert.ok(Array.isArray(demoProblem.requests));
  assert.ok(demoProblem.costMatrix);

  const piped = execFileSync(process.execPath, [CLI, "solve", "-"], {
    encoding: "utf8",
    input: demo,
  });
  assert.equal(JSON.parse(piped).optimal, true);
});
