/** 输入的原始规则：前提为字面量列表（合取），结论为单个字面量。 */
export interface RuleInput {
  name?: string;
  premise: string[];
  conclusion: string;
}

/** 问题输入（JSON 格式）。 */
export interface ProblemInput {
  variables: string[];
  assignment: Record<string, boolean>;
  modifiable?: Record<string, number>;
  rules: RuleInput[];
}

/** 解析后的字面量。 */
export interface Literal {
  variable: string;
  negated: boolean;
}

/** 编译后的规则（变量已转为下标，取值 0/1）。 */
export interface CompiledRule {
  name: string;
  premiseVars: number[];
  premiseVals: number[];
  conclVar: number;
  conclVal: number;
  premiseText: string[];
  conclusionText: string;
}

/** 编译后的问题。 */
export interface CompiledProblem {
  varNames: string[];
  initial: Uint8Array;
  /** fixed[v] = true 表示该变量不可修改。 */
  fixed: boolean[];
  /** flipCost[v] 为翻转成本（仅对可修改变量有意义）。 */
  flipCost: number[];
  rules: CompiledRule[];
}

/** 一处变量翻转。 */
export interface Flip {
  variable: string;
  from: boolean;
  to: boolean;
  cost: number;
}

/** 单条规则的复核结果。 */
export interface RuleCheck {
  name: string;
  satisfied: boolean;
  premiseHolds: boolean;
  conclusionHolds: boolean;
  premiseValues: { text: string; value: boolean }[];
  conclusionValue: boolean;
}

/** 求解结果。 */
export interface SolveResult {
  satisfiable: boolean;
  /** 最低修复成本；无解时为 Infinity。 */
  cost: number;
  flips: Flip[];
  finalAssignment: Record<string, boolean>;
  ruleChecks: RuleCheck[];
  /** 无解时的最小冲突核心（删除其中任意一条即可恢复可解）。 */
  core: CompiledRule[];
}
