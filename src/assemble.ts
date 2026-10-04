import type {
  AllocationInput,
  CapacityEntry,
  Migration,
  NodeSpec,
  SolveResult,
} from './types.js';

export interface AssembleStats {
  statesExplored: number;
  maxStates: number;
}

function compareId(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function assembleResult(
  input: AllocationInput,
  assignmentOf: (requestId: string) => string | null,
  provenOptimal: boolean,
  stats: AssembleStats,
): SolveResult {
  const previous: Record<string, string | null> = input.previous ?? {};
  const nodeById: Map<string, NodeSpec> = new Map();
  const usedByNode: Map<string, number> = new Map();
  for (const n of input.nodes) {
    nodeById.set(n.id, n);
    usedByNode.set(n.id, 0);
  }

  const assignments: Record<string, string | null> = {};
  const rejected: string[] = [];
  let admittedDemand = 0;
  let totalDemand = 0;
  let crossRegionCost = 0;

  const sortedRequests = [...input.requests].sort((a, b) => compareId(a.id, b.id));
  for (const r of sortedRequests) {
    totalDemand += r.demand;
    const nodeId = assignmentOf(r.id);
    assignments[r.id] = nodeId;
    if (nodeId === null) {
      rejected.push(r.id);
      continue;
    }
    const node = nodeById.get(nodeId);
    if (node === undefined) {
      throw new Error(`internal: assignment references unknown node ${nodeId}`);
    }
    usedByNode.set(node.id, (usedByNode.get(node.id) ?? 0) + r.demand);
    admittedDemand += r.demand;
    if (node.region !== r.region) {
      crossRegionCost += input.costs[r.region][node.region];
    }
  }

  const capacityLedger: CapacityEntry[] = [...input.nodes]
    .sort((a, b) => compareId(a.id, b.id))
    .map((n) => {
      const used = usedByNode.get(n.id) ?? 0;
      return {
        nodeId: n.id,
        region: n.region,
        capacity: n.capacity,
        used,
        remaining: n.capacity - used,
      };
    });

  const migrations: Migration[] = [];
  for (const requestId of Object.keys(previous).sort()) {
    const from = previous[requestId];
    const to = assignments[requestId];
    if (from !== to) migrations.push({ requestId, from, to });
  }

  return {
    assignments,
    rejected,
    capacityLedger,
    migrations,
    provenOptimal,
    stats: {
      statesExplored: stats.statesExplored,
      maxStates: stats.maxStates,
      admittedDemand,
      totalDemand,
      rejectedCount: rejected.length,
      crossRegionCost,
      migratedCount: migrations.length,
    },
  };
}
