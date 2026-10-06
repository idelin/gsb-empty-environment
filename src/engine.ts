import { evaluate } from './condition.ts';
import type {
  ConditionTrace,
  EngineResult,
  FactValue,
  Facts,
  FieldConflict,
  FieldEvidence,
  Rule,
  RuleEvidence,
  RuleOutcome,
} from './types.ts';

interface Proposal {
  readonly ruleId: string;
  readonly priority: number;
  readonly value: FactValue;
  readonly trace: ConditionTrace;
}

interface FieldAccumulator {
  readonly field: string;
  /** 全部命中提案（含被压制者），按到达顺序记录。 */
  readonly matched: Proposal[];
  /** 条件未命中但会给该字段赋值的规则。 */
  readonly notMatched: { readonly ruleId: string; readonly priority: number; readonly trace: ConditionTrace }[];
}

/**
 * 主引擎：单遍流式聚合。
 * 每条规则的条件只对输入快照求值一次（快照语义：规则永远读不到
 * 本轮其他规则的赋值），命中提案即流入各字段聚合器；遍历结束后
 * 逐字段按优先级仲裁出赢家或冲突。
 */
export function runEngine(rules: readonly Rule[], snapshot: Facts): EngineResult {
  const fields = new Map<string, FieldAccumulator>();

  const accumulatorFor = (field: string): FieldAccumulator => {
    let acc = fields.get(field);
    if (!acc) {
      acc = { field, matched: [], notMatched: [] };
      fields.set(field, acc);
    }
    return acc;
  };

  // 阶段一（融合在单遍中）：条件求值只依赖 snapshot，与聚合互不干扰。
  for (const rule of rules) {
    const trace = evaluate(rule.when, snapshot);
    for (const [field, value] of Object.entries(rule.set)) {
      const acc = accumulatorFor(field);
      if (trace.result) {
        acc.matched.push({ ruleId: rule.id, priority: rule.priority, value, trace });
      } else {
        acc.notMatched.push({ ruleId: rule.id, priority: rule.priority, trace });
      }
    }
  }

  // 阶段二：逐字段仲裁。
  const facts: Record<string, FactValue> = {};
  const conflicts: FieldConflict[] = [];
  const evidence: Record<string, FieldEvidence> = {};

  for (const acc of fields.values()) {
    const bestPriority = acc.matched.reduce((max, p) => Math.max(max, p.priority), -Infinity);
    const top = acc.matched.filter((p) => p.priority === bestPriority);
    const distinctValues = [...new Set(top.map((p) => p.value))];

    let status: FieldEvidence['status'];
    let winnerValue: FactValue | undefined;
    let conflict: FieldEvidence['conflict'];

    if (top.length === 0) {
      status = 'unset';
    } else if (distinctValues.length === 1) {
      status = 'resolved';
      winnerValue = top[0].value;
      facts[acc.field] = top[0].value;
    } else {
      status = 'conflict';
      conflict = {
        priority: bestPriority,
        values: distinctValues,
        ruleIds: top.map((p) => p.ruleId),
      };
      conflicts.push({ field: acc.field, priority: bestPriority, values: distinctValues, ruleIds: conflict.ruleIds });
    }

    const outcomeOf = (p: Proposal): RuleOutcome => {
      if (status === 'conflict') return p.priority === bestPriority ? 'conflicted' : 'loser';
      if (p.priority < bestPriority) return 'loser';
      return top.length === 1 ? 'winner' : 'co-winner';
    };

    const candidates: RuleEvidence[] = [
      ...acc.matched.map((p) => ({
        ruleId: p.ruleId,
        priority: p.priority,
        matched: true,
        trace: p.trace,
        proposedValue: p.value,
        outcome: outcomeOf(p),
      })),
      ...acc.notMatched.map((n) => ({
        ruleId: n.ruleId,
        priority: n.priority,
        matched: false,
        trace: n.trace,
        outcome: 'not-matched' as const,
      })),
    ];

    evidence[acc.field] = {
      field: acc.field,
      status,
      ...(status === 'resolved' ? { winnerValue, winningPriority: bestPriority } : {}),
      ...(conflict ? { conflict } : {}),
      candidates,
    };
  }

  return { facts, conflicts, evidence };
}
