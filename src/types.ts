/** 资源定义：不可再生，取值域为 [0, capacity] 的整数（有限状态空间）。 */
export interface ResourceSpec {
  name: string;
  /** 初始预算，同时也是该资源的上界。 */
  capacity: number;
}

/** 动作：消耗不可再生资源，达成若干目标项。 */
export interface ActionSpec {
  name: string;
  /** 动作成本（次目标：在收益相同的前提下最小化总成本）。 */
  cost: number;
  /** 各资源消耗量（只消耗、不补充）。 */
  consumes: Record<string, number>;
  /** 执行后达成的目标名集合。 */
  achieves: string[];
}

/** 目标项：可任选若干达成，每项带来固定收益。 */
export interface GoalSpec {
  name: string;
  profit: number;
}

/** 规划问题：有限状态空间作为输入约束。 */
export interface Problem {
  resources: ResourceSpec[];
  actions: ActionSpec[];
  goals: GoalSpec[];
}
