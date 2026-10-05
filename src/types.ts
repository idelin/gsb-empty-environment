/** 一份参赛作品。 */
export interface Work {
  id: string;
  /** 作品所属专业方向。 */
  field: string;
}

/** 一位评委。 */
export interface Judge {
  id: string;
  /** 评委可评审的专业方向。 */
  fields: string[];
}

/** 一条回避关系：某评委不得评审某作品。 */
export interface ConflictPair {
  judgeId: string;
  workId: string;
}

export interface Problem {
  works: Work[];
  judges: Judge[];
  /** 回避关系列表（评委-作品）。 */
  conflicts?: ConflictPair[];
  /** 每份作品所需评委数，默认 3。 */
  judgesPerWork?: number;
  /** 任意评委对最多共同评审的作品数（联合限制），默认 2。 */
  maxPairCoJudging?: number;
  /** 规模上限（作品数），默认 12。 */
  maxWorks?: number;
  /** 单次回溯搜索的节点预算，默认 1_000_000。 */
  nodeLimit?: number;
}

/** workId -> 评委 id 列表。 */
export type Assignment = Record<string, string[]>;

export interface OkResult {
  status: 'ok';
  assignment: Assignment;
  /** 每位评委的工作量（评审作品数），未参与评审的评委为 0。 */
  loads: Record<string, number>;
  /** 工作量最大差 = 最大工作量 - 最小工作量（已最小化）。 */
  maxLoadDiff: number;
  /** 每个评委对共同评审的作品数，键为排序后用 | 连接的两个评委 id。 */
  pairCoJudging: Record<string, number>;
  nodesExplored: number;
}

/** 无解见证。 */
export type Witness =
  | {
      /** 回避见证：某作品合格评委不足。 */
      type: 'work-conflict';
      workId: string;
      eligibleJudgeIds: string[];
      /** 专业匹配但因回避被排除的评委。 */
      blockedByConflict: string[];
      required: number;
      detail: string;
    }
  | {
      /** 容量见证：某作品子集的合格评委集合容量不足（含评委对联合限制）。 */
      type: 'capacity';
      bound: 'judge-pair-slots' | 'pair-cojudging';
      workIds: string[];
      judgeIds: string[];
      requiredSlots: number;
      maxSlots: number;
      detail: string;
    }
  | {
      /** 组合核心：极小不可行作品子集（经完备搜索验证，删去任意一份即可行）。 */
      type: 'combinatorial-core';
      workIds: string[];
      judgeIds: string[];
      detail: string;
    };

export interface InfeasibleResult {
  status: 'infeasible';
  witness: Witness;
}

export interface UnsupportedResult {
  status: 'unsupported';
  reason: string;
  workCount: number;
  maxWorks: number;
}

export interface UndeterminedResult {
  status: 'undetermined';
  reason: string;
}

export type SolveResult = OkResult | InfeasibleResult | UnsupportedResult | UndeterminedResult;

export interface Verification {
  valid: boolean;
  violations: string[];
  loads: Record<string, number>;
  maxLoadDiff: number;
  pairCoJudging: Record<string, number>;
}
