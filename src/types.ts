// 资源向量：资源名 -> 数量（不可再生，只减不增）
export interface ResourceVector {
  [resource: string]: number;
}

// 动作：消耗资源、在有限状态机上迁移、可达成若干目标
export interface Action {
  name: string;
  cost: number;                 // 动作成本（次目标：同收益下取最小）
  consumes: ResourceVector;     // 不可再生资源消耗
  from: string[];               // 有限状态空间约束：允许触发的前置状态
  to: string;                   // 执行后迁移到的状态
  requiresGoals?: string[];     // 前置目标（必须先达成）
  achieves?: string[];          // 本动作达成的目标
}

// 目标：可任选若干项达成，每项带来收益
export interface Goal {
  id: string;
  profit: number;               // 收益（主目标：最大化）
}

export interface PlannerInput {
  initialState: string;         // 有限状态机初始状态
  initialResources: ResourceVector; // 资源预算
  actions: Action[];
  goals: Goal[];
  maxSteps?: number;            // 计划长度上限（默认 64）
  nearDistance?: number;        // “看似接近目标”的距离阈值（默认 2）
}

// 计划中的一步，含执行后的资源余额
export interface PlanStep {
  action: string;
  cost: number;
  consumes: ResourceVector;
  balances: ResourceVector;     // 本步执行后的资源余额
  state: string;                // 本步执行后所处状态
  achievedGoals: string[];      // 本步新达成的目标
}

// 被关键资源卡住的目标
export interface BlockedGoal {
  goal: string;
  profit: number;
  distance: number;             // 忽略资源时到达该目标还需的动作数（-1 表示图上不可达）
  missing: ResourceVector;      // 各关键资源的缺口下界
}

// 死端：无法再通过任何目标，但至少一个目标“看似接近”却被资源卡死
export interface DeadEnd {
  prefix: string[];             // 到达该死端的动作前缀
  state: string;
  remaining: ResourceVector;
  profit: number;
  cost: number;
  blockedGoals: BlockedGoal[];
}

export interface PlanSummary {
  profit: number;
  cost: number;
  steps: PlanStep[];
  achievedGoals: string[];
  finalState: string;
  finalBalances: ResourceVector;
}

export interface PlanResult extends PlanSummary {
  deadEnds: DeadEnd[];          // 搜索过程中识别出的死端
  explored: number;             // 实际扩展的结点数（统计）
}
