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

export type RegionCostMatrix = Record<string, Record<string, number>>;

export type PreviousPlan = Record<string, string | null>;

export interface AllocationInput {
  requests: RequestSpec[];
  nodes: NodeSpec[];
  costs: RegionCostMatrix;
  previous?: PreviousPlan;
}

export interface BudgetOptions {
  maxStates?: number;
  timeMs?: number;
}

export interface Migration {
  requestId: string;
  from: string | null;
  to: string | null;
}

export interface CapacityEntry {
  nodeId: string;
  region: string;
  capacity: number;
  used: number;
  remaining: number;
}

export interface SolveStats {
  statesExplored: number;
  maxStates: number;
  admittedDemand: number;
  totalDemand: number;
  rejectedCount: number;
  crossRegionCost: number;
  migratedCount: number;
}

export interface SolveResult {
  assignments: Record<string, string | null>;
  rejected: string[];
  capacityLedger: CapacityEntry[];
  migrations: Migration[];
  provenOptimal: boolean;
  stats: SolveStats;
}
