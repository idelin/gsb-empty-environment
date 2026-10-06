/** 事实值：规则库允许的原子值类型。 */
export type FactValue = string | number | boolean | null;

/** 事实快照：字段名 -> 值。规则条件只能读取该快照。 */
export type Facts = Readonly<Record<string, FactValue>>;

/** 条件表达式：针对快照求值的谓词树。 */
export type Condition =
  | { op: 'eq'; field: string; value: FactValue }
  | { op: 'neq'; field: string; value: FactValue }
  | { op: 'lt' | 'lte' | 'gt' | 'gte'; field: string; value: number }
  | { op: 'in'; field: string; values: readonly FactValue[] }
  | { op: 'exists'; field: string }
  | { op: 'and'; all: readonly Condition[] }
  | { op: 'or'; any: readonly Condition[] }
  | { op: 'not'; cond: Condition };

/** 规则：优先级 + 条件 + 一组字段赋值。priority 数值大者胜。 */
export interface Rule {
  readonly id: string;
  readonly priority: number;
  readonly when: Condition;
  readonly set: Readonly<Record<string, FactValue>>;
}

/** 条件求值轨迹节点：与 Condition 树同构，记录每个子表达式的真值。 */
export interface ConditionTrace {
  readonly condition: Condition;
  readonly result: boolean;
  readonly children: readonly ConditionTrace[];
}

export type RuleOutcome =
  | 'winner'        // 该字段唯一最高优先级来源
  | 'co-winner'     // 同优先级同值，并列胜出
  | 'loser'         // 命中但被更高优先级压制
  | 'conflicted'    // 同优先级异值，卷入冲突
  | 'not-matched'   // 条件未命中
  | 'not-applicable'; // 规则不给该字段赋值

/** 证据树节点：一条规则对某字段的完整判定证据。 */
export interface RuleEvidence {
  readonly ruleId: string;
  readonly priority: number;
  readonly matched: boolean;
  readonly trace: ConditionTrace;
  readonly proposedValue?: FactValue;
  readonly outcome: RuleOutcome;
}

export type FieldStatus = 'resolved' | 'conflict' | 'unset';

/** 字段级证据树：根为字段，子节点为各规则证据，规则下挂条件轨迹子树。 */
export interface FieldEvidence {
  readonly field: string;
  readonly status: FieldStatus;
  readonly winnerValue?: FactValue;
  readonly winningPriority?: number;
  readonly conflict?: {
    readonly priority: number;
    readonly values: readonly FactValue[];
    readonly ruleIds: readonly string[];
  };
  readonly candidates: readonly RuleEvidence[];
}

export interface FieldConflict {
  readonly field: string;
  readonly priority: number;
  readonly values: readonly FactValue[];
  readonly ruleIds: readonly string[];
}

export interface EngineResult {
  /** 仅包含 resolved 字段的最终赋值。 */
  readonly facts: Record<string, FactValue>;
  readonly conflicts: readonly FieldConflict[];
  /** 每个被提及字段的证据树。 */
  readonly evidence: Readonly<Record<string, FieldEvidence>>;
}
