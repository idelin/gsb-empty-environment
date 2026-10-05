export interface ChangeSpec {
  id: string;
  cost: number;
  deps?: string[];
}

export interface ProblemSpec {
  budget: number;
  changes: ChangeSpec[];
}

export interface Combo {
  mask: bigint;
  changes: string[];
  cost: number;
  pairMask: bigint;
  pairCount: number;
}

export interface ComboInfo {
  changes: string[];
  cost: number;
  pairs: number;
}

export interface UncoveredPair {
  a: string;
  b: string;
  reason: string;
}

export interface SolutionInfo {
  combos: ComboInfo[];
  totalCost: number;
  coveredPairs: number;
}

export interface Analysis {
  budget: number;
  changeCount: number;
  legalComboCount: number;
  totalPairs: number;
  reachablePairs: number;
  exact: SolutionInfo;
  greedy: SolutionInfo;
  gap: { absolute: number; percent: number };
  uncovered: UncoveredPair[];
  unselectable: string[];
}
