import type { CostMatrix, Problem } from "./types.ts";

/** Deterministic PRNG (mulberry32) so synthetic data is reproducible locally. */
export function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface SynthOptions {
  requestCount?: number;
  nodeCount?: number;
  regionCount?: number;
  maxDemand?: number;
  maxCapacity?: number;
  maxCost?: number;
  /** Attach a previous plan referencing random (possibly removed) nodes. */
  withPrevious?: boolean;
  /** Remove the last node from the fleet (simulates a node going offline). */
  dropLastNode?: boolean;
}

export function generateInstance(seed: number, options: SynthOptions = {}): Problem {
  const {
    requestCount = 8,
    nodeCount = 3,
    regionCount = 2,
    maxDemand = 9,
    maxCapacity = 20,
    maxCost = 9,
    withPrevious = false,
    dropLastNode = false,
  } = options;
  const rand = mulberry32(seed);
  const pick = (n: number) => Math.floor(rand() * n);

  const regions = Array.from({ length: regionCount }, (_, i) => `R${i}`);
  const costMatrix: CostMatrix = {};
  for (const from of regions) {
    costMatrix[from] = {};
    for (const to of regions) {
      costMatrix[from][to] = from === to ? 0 : 1 + pick(maxCost);
    }
  }

  const allNodes = Array.from({ length: nodeCount }, (_, i) => ({
    id: `n${i}`,
    region: regions[pick(regions.length)],
    capacity: 1 + pick(maxCapacity),
  }));
  const nodes = dropLastNode && allNodes.length > 1 ? allNodes.slice(0, -1) : allNodes;

  const requests = Array.from({ length: requestCount }, (_, i) => ({
    id: `r${String(i).padStart(2, "0")}`,
    region: regions[pick(regions.length)],
    demand: 1 + pick(maxDemand),
  }));

  const problem: Problem = { requests, nodes, costMatrix };
  if (withPrevious) {
    const previous: Record<string, string> = {};
    for (const request of requests) {
      if (rand() < 0.8) {
        previous[request.id] = allNodes[pick(allNodes.length)].id;
      }
    }
    problem.previous = previous;
  }
  return problem;
}
