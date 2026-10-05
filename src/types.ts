/** 一个字面量：变量 var 必须等于 value。 */
export interface Literal {
  var: string;
  value: boolean;
}

/** 蕴含规则：前提（字面量合取）蕴含结论（单个字面量）。 */
export interface Rule {
  id?: string;
  if: Literal[];
  then: Literal;
}

/** 求解器输入。 */
export interface Instance {
  /** 所有变量的当前取值（最多 25 个）。 */
  variables: Record<string, boolean>;
  /** 允许翻转的变量及其成本（成本 >= 0）；未列出的变量不可修改。 */
  mutable?: Record<string, number>;
  /** 需要全部满足的蕴含规则。 */
  rules: Rule[];
}

/** 一处变量翻转。 */
export interface Change {
  var: string;
  from: boolean;
  to: boolean;
  cost: number;
}

/** 单条规则的复核结果。 */
export interface RuleCheck {
  rule: number;
  id?: string;
  premise: { var: string; expected: boolean; actual: boolean; holds: boolean }[];
  premiseHolds: boolean;
  conclusion: { var: string; expected: boolean; actual: boolean };
  satisfied: boolean;
}

export interface ConflictCoreRule extends RuleCheck {
  rule: number;
  id?: string;
}

export type SolveResult =
  | {
      status: "satisfied" | "repaired";
      totalCost: number;
      changes: Change[];
      assignment: Record<string, boolean>;
      verification: RuleCheck[];
    }
  | {
      status: "unsatisfiable";
      reason: string;
      conflictCore: ConflictCoreRule[];
      immutableVariablesInCore: string[];
    };
