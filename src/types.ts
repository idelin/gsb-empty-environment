/** 评委 */
export interface Judge {
  id: string;
  /** 专业方向列表 */
  specialties: readonly string[];
}

/** 作品 */
export interface Work {
  id: string;
  /** 评审所需专业 */
  specialty: string;
}

/** 回避关系：judgeId 不得评审 workId */
export interface Avoidance {
  judgeId: string;
  workId: string;
}

export interface SolveOptions {
  /** 每份作品的评委人数，默认 3 */
  teamSize?: number;
  /** 任意评委对最多共评的作品数（联合限制），默认 2 */
  maxPairShared?: number;
  /** 作品规模上限，默认 12 */
  maxWorks?: number;
  /** 单次求解的搜索节点预算，默认 1_000_000 */
  nodeBudget?: number;
  /** 单次求解的时间预算（毫秒），默认 30_000 */
  timeLimitMs?: number;
}

/** workId -> judgeId[] */
export type Assignment = Record<string, readonly string[]>;

export interface SearchStats {
  /** 搜索节点总数 */
  nodes: number;
  /** 尝试过的 (最大差, 负载下界) 组合数 */
  attempts: number;
  elapsedMs: number;
}

export interface Solved {
  status: 'optimal';
  assignment: Assignment;
  /** 每位评委的工作量（评审作品数） */
  loads: Record<string, number>;
  minLoad: number;
  maxLoad: number;
  /** 最优目标值：maxLoad - minLoad */
  spread: number;
  stats: SearchStats;
}

/** 回避见证：某作品可用评委不足 teamSize 人 */
export interface AvoidanceWitness {
  kind: 'avoidance';
  workId: string;
  eligibleJudgeIds: string[];
  required: number;
}

/**
 * 容量见证：作品子集 S 的可用评委池为 T，
 * S 中每份作品消耗 C(teamSize,2) 个评委对名额，
 * 而 T 内全部评委对在联合限制下最多提供 maxPairShared * C(|T|,2) 个，
 * demand > capacity 即不可行。
 */
export interface CapacityWitness {
  kind: 'capacity';
  workIds: string[];
  judgeIds: string[];
  demand: number;
  capacity: number;
}

/** 穷举全部负载界后仍不可行（无简单线性证书，由完备搜索证明） */
export interface ExhaustedWitness {
  kind: 'exhausted';
  detail: string;
}

export type Witness = AvoidanceWitness | CapacityWitness | ExhaustedWitness;

export interface Infeasible {
  status: 'infeasible';
  witness: Witness;
  stats: SearchStats;
}

export interface SolveError {
  status: 'error';
  message: string;
}

export type SolveResult = Solved | Infeasible | SolveError;
