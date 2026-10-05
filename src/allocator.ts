import type {
  AllocationResult,
  LedgerEntry,
  Migration,
  Problem,
  SolveOptions,
} from "./types.ts";

/**
 * Sentinel used in the tie-break vector for rejected requests.
 * It sorts after any real node id, so among otherwise-equal plans the
 * tie-break prefers plans where earlier (smaller-id) requests stay admitted.
 */
export const REJECTED = "\u{10FFFF}";

export const DEFAULT_BUDGET = 1_000_000;

export class AllocationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "AllocationError";
  }
}

interface IndexedRequest {
  id: string;
  region: string;
  demand: number;
  prev: string | null;
}

interface IndexedNode {
  id: string;
  region: string;
  capacity: number;
}

export interface Prepared {
  requests: IndexedRequest[];
  nodes: IndexedNode[];
  nodeIndex: Map<string, number>;
  costBetween: (requestRegion: string, nodeRegion: string) => number;
}

export interface Objective {
  admitted: number;
  cost: number;
  migrations: number;
  vector: string[];
}

/**
 * Lexicographic objective order:
 *   1. maximize admitted demand
 *   2. minimize cross-region cost
 *   3. minimize migrations vs the previous plan
 *   4. minimize the node-id vector (requests sorted by id, REJECTED sentinel last)
 * Returns a negative number when a is better than b.
 */
export function compareObjectives(a: Objective, b: Objective): number {
  if (a.admitted !== b.admitted) return b.admitted - a.admitted;
  if (a.cost !== b.cost) return a.cost - b.cost;
  if (a.migrations !== b.migrations) return a.migrations - b.migrations;
  const n = Math.min(a.vector.length, b.vector.length);
  for (let i = 0; i < n; i++) {
    if (a.vector[i] !== b.vector[i]) return a.vector[i] < b.vector[i] ? -1 : 1;
  }
  return a.vector.length - b.vector.length;
}

function fail(message: string): never {
  throw new AllocationError(message);
}

function checkNonNegativeInteger(value: unknown, label: string): void {
  if (typeof value !== "number" || !Number.isInteger(value) || value < 0) {
    fail(`${label} must be a non-negative integer, got ${JSON.stringify(value)}`);
  }
}

function checkId(value: unknown, label: string): asserts value is string {
  if (typeof value !== "string" || value.length === 0) {
    fail(`${label} must be a non-empty string`);
  }
  if (value === REJECTED) {
    fail(`${label} uses the reserved sentinel ${JSON.stringify(REJECTED)}`);
  }
}

/** Validate the problem and canonicalize it (requests and nodes sorted by id). */
export function prepare(problem: Problem): Prepared {
  if (problem === null || typeof problem !== "object") fail("problem must be an object");
  const { requests, nodes, costMatrix, previous } = problem;
  if (!Array.isArray(requests)) fail("problem.requests must be an array");
  if (!Array.isArray(nodes)) fail("problem.nodes must be an array");
  if (costMatrix === null || typeof costMatrix !== "object" || Array.isArray(costMatrix)) {
    fail("problem.costMatrix must be an object: costMatrix[requestRegion][nodeRegion]");
  }
  if (
    previous !== undefined &&
    (previous === null || typeof previous !== "object" || Array.isArray(previous))
  ) {
    fail("problem.previous must be an object mapping requestId to nodeId");
  }

  const seenRequests = new Set<string>();
  for (const r of requests) {
    if (r === null || typeof r !== "object") fail("every request must be an object");
    checkId(r.id, "request id");
    if (seenRequests.has(r.id)) fail(`duplicate request id ${JSON.stringify(r.id)}`);
    seenRequests.add(r.id);
    checkId(r.region, `region of request ${r.id}`);
    checkNonNegativeInteger(r.demand, `demand of request ${r.id}`);
  }

  const seenNodes = new Set<string>();
  for (const n of nodes) {
    if (n === null || typeof n !== "object") fail("every node must be an object");
    checkId(n.id, "node id");
    if (seenNodes.has(n.id)) fail(`duplicate node id ${JSON.stringify(n.id)}`);
    seenNodes.add(n.id);
    checkId(n.region, `region of node ${n.id}`);
    checkNonNegativeInteger(n.capacity, `capacity of node ${n.id}`);
  }

  const prev = (previous ?? {}) as Record<string, unknown>;
  for (const [requestId, nodeId] of Object.entries(prev)) {
    if (!seenRequests.has(requestId)) continue; // stale entries are ignored
    if (typeof nodeId !== "string" || nodeId.length === 0) {
      fail(`previous[${JSON.stringify(requestId)}] must be a node id string`);
    }
  }

  const costBetween = (requestRegion: string, nodeRegion: string): number => {
    const row = (costMatrix as Record<string, Record<string, unknown>>)[requestRegion];
    const value = row?.[nodeRegion];
    if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
      fail(
        `costMatrix[${JSON.stringify(requestRegion)}][${JSON.stringify(nodeRegion)}] ` +
          `must be a non-negative finite number`,
      );
    }
    return value;
  };
  for (const region of new Set(requests.map((r) => r.region))) {
    for (const nodeRegion of new Set(nodes.map((n) => n.region))) {
      costBetween(region, nodeRegion);
    }
  }

  const sortedNodes: IndexedNode[] = [...nodes]
    .sort((a, b) => (a.id < b.id ? -1 : 1))
    .map((n) => ({ id: n.id, region: n.region, capacity: n.capacity }));
  const nodeIndex = new Map<string, number>(sortedNodes.map((n, i) => [n.id, i]));
  const sortedRequests: IndexedRequest[] = [...requests]
    .sort((a, b) => (a.id < b.id ? -1 : 1))
    .map((r) => ({
      id: r.id,
      region: r.region,
      demand: r.demand,
      prev: typeof prev[r.id] === "string" ? (prev[r.id] as string) : null,
    }));

  return { requests: sortedRequests, nodes: sortedNodes, nodeIndex, costBetween };
}

export function objectiveOf(prepared: Prepared, assignment: string[]): Objective {
  const { requests, nodes, nodeIndex, costBetween } = prepared;
  let admitted = 0;
  let cost = 0;
  let migrations = 0;
  for (let i = 0; i < requests.length; i++) {
    const target = assignment[i];
    if (target === REJECTED) continue;
    const request = requests[i];
    admitted += request.demand;
    cost += costBetween(request.region, nodes[nodeIndex.get(target)!].region);
    if (request.prev !== null && request.prev !== target) migrations += 1;
  }
  return { admitted, cost, migrations, vector: assignment.slice() };
}

interface StatsInput {
  searchNodes: number;
  budget: number;
  budgetExhausted: boolean;
}

export function buildResult(
  prepared: Prepared,
  assignment: string[],
  stats: StatsInput,
): AllocationResult {
  const { requests, nodes, nodeIndex, costBetween } = prepared;
  const assignmentOut: Record<string, string | null> = {};
  const rejected: string[] = [];
  const used = nodes.map(() => 0);
  const ledgerRequests = nodes.map((): string[] => []);
  const migrations: Migration[] = [];
  let admitted = 0;
  let cost = 0;
  let migratedCount = 0;

  for (let i = 0; i < requests.length; i++) {
    const request = requests[i];
    const target = assignment[i];
    if (target === REJECTED) {
      assignmentOut[request.id] = null;
      rejected.push(request.id);
      continue;
    }
    const ni = nodeIndex.get(target);
    if (ni === undefined) fail(`internal error: unknown node ${target}`);
    assignmentOut[request.id] = target;
    admitted += request.demand;
    used[ni] += request.demand;
    ledgerRequests[ni].push(request.id);
    cost += costBetween(request.region, nodes[ni].region);
    if (request.prev !== null && request.prev !== target) {
      migratedCount += 1;
      migrations.push({ requestId: request.id, from: request.prev, to: target });
    }
  }

  const ledger: Record<string, LedgerEntry> = {};
  for (let i = 0; i < nodes.length; i++) {
    ledger[nodes[i].id] = {
      capacity: nodes[i].capacity,
      used: used[i],
      remaining: nodes[i].capacity - used[i],
      requests: ledgerRequests[i],
    };
  }

  const totalDemand = requests.reduce((sum, r) => sum + r.demand, 0);
  return {
    assignment: assignmentOut,
    rejected,
    ledger,
    migrations,
    stats: {
      totalDemand,
      admittedDemand: admitted,
      rejectedDemand: totalDemand - admitted,
      crossRegionCost: cost,
      migratedCount,
      searchNodes: stats.searchNodes,
      budget: stats.budget,
      budgetExhausted: stats.budgetExhausted,
    },
    optimal: !stats.budgetExhausted,
  };
}

const BUDGET_EXHAUSTED = Symbol("budget-exhausted");

/**
 * Solve one whole batch from scratch (capacity shrinks / node exits are just
 * new inputs). Branch and bound with an exact search-node budget: when the
 * budget is exhausted the best feasible plan found so far is returned with
 * `optimal: false`; optimality is never claimed without proof.
 */
export function solve(problem: Problem, options: SolveOptions = {}): AllocationResult {
  const budget = options.budget ?? DEFAULT_BUDGET;
  checkNonNegativeInteger(budget, "budget");
  const prepared = prepare(problem);
  const { requests, nodes, nodeIndex, costBetween } = prepared;

  // The all-rejected plan is always feasible and seeds the incumbent.
  let bestAssignment = requests.map(() => REJECTED);
  let best = objectiveOf(prepared, bestAssignment);
  let visited = 0;
  let budgetExhausted = false;

  const searchNeeded = requests.length > 0 && nodes.length > 0;
  if (searchNeeded && budget === 0) {
    budgetExhausted = true;
  } else if (searchNeeded) {
    const count = requests.length;
    const liveNodeIds = new Set(nodes.map((n) => n.id));

    // Suffix bounds used for pruning.
    const demandSuffix = new Array<number>(count + 1).fill(0);
    const minCostSuffix = new Array<number>(count + 1).fill(0);
    const forcedMigrationSuffix = new Array<number>(count + 1).fill(0);
    for (let i = count - 1; i >= 0; i--) {
      const request = requests[i];
      demandSuffix[i] = demandSuffix[i + 1] + request.demand;
      let minCost = Infinity;
      for (const node of nodes) {
        minCost = Math.min(minCost, costBetween(request.region, node.region));
      }
      minCostSuffix[i] = minCostSuffix[i + 1] + minCost;
      // If every remaining request must be admitted, any request whose previous
      // node is gone is a forced migration.
      const forced = request.prev !== null && !liveNodeIds.has(request.prev) ? 1 : 0;
      forcedMigrationSuffix[i] = forcedMigrationSuffix[i + 1] + forced;
    }

    // Candidate nodes per request: cheapest region first, node id as tie-break.
    const orderPerRequest: number[][] = requests.map((request) =>
      nodes
        .map((node, index) => ({
          index,
          cost: costBetween(request.region, node.region),
          id: node.id,
        }))
        .sort((a, b) => a.cost - b.cost || (a.id < b.id ? -1 : 1))
        .map((entry) => entry.index),
    );

    const remaining = nodes.map((n) => n.capacity);
    const current = new Array<string>(count).fill(REJECTED);

    const dfs = (
      i: number,
      admitted: number,
      cost: number,
      migrations: number,
      prefixTied: boolean,
    ): void => {
      visited += 1;
      if (visited > budget) {
        budgetExhausted = true;
        throw BUDGET_EXHAUSTED;
      }
      if (i === count) {
        const candidate: Objective = {
          admitted,
          cost,
          migrations,
          vector: current.slice(),
        };
        if (compareObjectives(candidate, best) < 0) {
          best = candidate;
          bestAssignment = current.slice();
        }
        return;
      }

      const reachableAdmitted = admitted + demandSuffix[i];
      if (reachableAdmitted < best.admitted) return;
      // When only admitting everything left can tie the incumbent, every
      // remaining request must be admitted, so the suffix bounds become exact.
      // When the branch can still beat the incumbent on admitted demand, the
      // lower-priority objectives must not prune it.
      const mustAdmitAll = reachableAdmitted === best.admitted;
      let canOnlyTie = false;
      if (mustAdmitAll) {
        const costBound = cost + minCostSuffix[i];
        if (costBound > best.cost) return;
        const migrationBound = migrations + forcedMigrationSuffix[i];
        if (costBound === best.cost && migrationBound > best.migrations) return;
        canOnlyTie = costBound === best.cost && migrationBound === best.migrations;
      }

      const request = requests[i];
      for (const ni of orderPerRequest[i]) {
        if (remaining[ni] < request.demand) continue;
        const nodeId = nodes[ni].id;
        // Tie-break pruning: with an equal prefix and no chance to improve the
        // first three objectives, a larger vector entry can never win.
        if (prefixTied && canOnlyTie && nodeId > best.vector[i]) continue;
        remaining[ni] -= request.demand;
        current[i] = nodeId;
        dfs(
          i + 1,
          admitted + request.demand,
          cost + costBetween(request.region, nodes[ni].region),
          migrations + (request.prev !== null && request.prev !== nodeId ? 1 : 0),
          prefixTied && nodeId === best.vector[i],
        );
        remaining[ni] += request.demand;
      }
      if (!(prefixTied && canOnlyTie && REJECTED > best.vector[i])) {
        current[i] = REJECTED;
        dfs(i + 1, admitted, cost, migrations, prefixTied && best.vector[i] === REJECTED);
      }
      current[i] = REJECTED;
    };

    try {
      dfs(0, 0, 0, 0, true);
    } catch (error) {
      if (error !== BUDGET_EXHAUSTED) throw error;
    }
  }

  return buildResult(prepared, bestAssignment, {
    searchNodes: visited,
    budget,
    budgetExhausted,
  });
}
