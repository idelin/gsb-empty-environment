// 数据模型：多项目发布规划

export interface ProjectInput {
  id: string;
  deps: string[]; // 依赖的项目：必须在严格更早的批次发布（同一依赖环内则同批）
}

export interface PlanRequest {
  capacity: number; // 每批容量上限（按项目个数计）
  projects: ProjectInput[];
}

export interface TogetherReason {
  projects: string[]; // 同批发布的一组项目
  kind: 'cycle' | 'capacity';
  detail: string; // 人类可读的原因说明
}

export interface Batch {
  index: number; // 0 = 最先发布
  projects: string[];
  reasons: TogetherReason[];
}

export interface CycleWitness {
  kind: 'cycle-exceeds-capacity';
  component: string[]; // 必须同批发布的强连通分量（依赖环）
  cycle: string[]; // 分量中一条具体的依赖环，首尾相同，如 [a, b, c, a]
  size: number;
  capacity: number;
  detail: string;
}

export type Witness = CycleWitness;

export type PlanResult =
  | { ok: true; batchCount: number; batches: Batch[] }
  | { ok: false; witness: Witness };
