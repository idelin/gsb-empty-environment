/** 一个项目及其直接前置依赖（dependsOn 里的项目必须先发布）。 */
export interface ProjectSpec {
  name: string;
  dependsOn?: string[];
}

/** 发布规划输入：每批容量上限（按项目个数计）与项目列表。 */
export interface PlanInput {
  capacity: number;
  projects: ProjectSpec[];
}

/** 一个发布批次：1 起始的批次号、同批项目、以及“为什么一起发布”的原因。 */
export interface PlannedBatch {
  index: number;
  projects: string[];
  reasons: string[];
}

/** 不可行见证：某个必须同批发布的依赖环超过了批次容量。 */
export interface CycleWitness {
  kind: 'oversized-cycle';
  /** 环所在的强连通分量全部成员 */
  members: string[];
  /** 一条具体的依赖环路径，如 ["A","B","C","A"] 表示 A 依赖 B 依赖 C 依赖 A */
  cycle: string[];
  size: number;
  capacity: number;
  message: string;
}

export type PlanResult =
  | { ok: true; batchCount: number; batches: PlannedBatch[] }
  | { ok: false; witness: CycleWitness };
