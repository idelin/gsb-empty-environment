export interface RequestSpec {
  id: string;
  region: string;
  demand: number;
}

export interface NodeSpec {
  id: string;
  region: string;
  capacity: number;
}

/** costMatrix[requestRegion][nodeRegion] -> non-negative finite number */
export type CostMatrix = Record<string, Record<string, number>>;

export interface Problem {
  requests: RequestSpec[];
  nodes: NodeSpec[];
  costMatrix: CostMatrix;
  /** Previous plan: requestId -> nodeId. May reference nodes that are now gone. */
  previous?: Record<string, string>;
}

export interface SolveOptions {
  /** Exact search budget: max number of branch-and-bound search nodes. */
  budget?: number;
}

export interface LedgerEntry {
  capacity: number;
  used: number;
  remaining: number;
  requests: string[];
}

export interface Migration {
  requestId: string;
  from: string;
  to: string;
}

export interface AllocationStats {
  totalDemand: number;
  admittedDemand: number;
  rejectedDemand: number;
  crossRegionCost: number;
  migratedCount: number;
  searchNodes: number;
  budget: number;
  budgetExhausted: boolean;
}

export interface AllocationResult {
  /** requestId -> nodeId, or null when rejected. Keys sorted by requestId. */
  assignment: Record<string, string | null>;
  /** Rejected request ids, sorted. */
  rejected: string[];
  /** Capacity ledger per node. Keys sorted by nodeId. remaining is always >= 0. */
  ledger: Record<string, LedgerEntry>;
  /** Requests admitted both before and now, but on a different node. */
  migrations: Migration[];
  stats: AllocationStats;
  /** false when the search budget was exhausted before proving optimality. */
  optimal: boolean;
}
