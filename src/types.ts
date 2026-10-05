/** 输入：单个改动的描述。 */
export interface ChangeSpec {
  /** 改动唯一标识。 */
  id: string;
  /** 执行该改动的成本（占用执行预算）。 */
  cost: number;
  /** 依赖：选择本改动时必须同时选择的改动 id 列表（可传递）。 */
  requires?: string[];
  /** 冲突：与本改动互斥、不可同时选择的改动 id 列表（自动对称化）。 */
  conflicts?: string[];
}

/** 输入：一次规划问题。 */
export interface Instance {
  /** 执行预算：所选改动的总成本上限。 */
  budget: number;
  /** 候选改动，最多 18 个。 */
  changes: ChangeSpec[];
}

/** 归一化后的内部模型（下标即改动序号）。 */
export interface Model {
  n: number;
  ids: string[];
  costs: number[];
  /** closureMask[i]：选择 i 时依赖闭包（含 i 自身）的位掩码。 */
  closureMask: number[];
  /** closureCost[i]：依赖闭包的总成本。 */
  closureCost: number[];
  /** conflictMask[i]：与 i 冲突的改动集合位掩码（对称化后）。 */
  conflictMask: number[];
}

/** 一个可行方案。 */
export interface Solution {
  /** 选中改动的位掩码。 */
  mask: number;
  /** 总成本。 */
  cost: number;
  /** 覆盖的二阶交互数 = C(popcount(mask), 2)。 */
  coveredPairs: number;
}

/** 未覆盖原因分类。 */
export type UncoveredReason =
  | { kind: "conflict"; a: string; b: string; detail: string }
  | { kind: "budget"; a: string; b: string; minCost: number; budget: number }
  | { kind: "sacrifice"; a: string; b: string; bestWithPair: number; optimal: number }
  | { kind: "costlier"; a: string; b: string; costWithPair: number; optimalCost: number };

export interface UncoveredPair {
  a: string;
  b: string;
  reason: UncoveredReason;
}

/** 精确求解的完整结果。 */
export interface ExactResult {
  optimal: Solution;
  /** 可达二阶交互总数（存在合法方案同时包含两者的改动对数）。 */
  reachablePairs: number;
  /** 全部二阶交互数 C(n, 2)。 */
  totalPairs: number;
  /** 最优方案未覆盖的交互及原因。 */
  uncovered: UncoveredPair[];
}
