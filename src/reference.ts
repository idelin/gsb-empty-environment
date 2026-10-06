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
} from './types.ts';

interface FlatProposal {
  readonly ruleId: string;
  readonly priority: number;
  readonly field: string;
  readonly value: FactValue;
  readonly matched: boolean;
  readonly trace: ConditionTrace;
}

/**
 * 朴素两阶段参考模型：刻意不做任何流式优化。
 * 阶段一：把每条规则 x 每个赋值字段展开成扁平提案表，条件全部
 *         针对同一输入快照求值（规则间赋值互不可见）。
 * 阶段二：按字段分组，组内按优先级降序排序，取头部同优先级组
 *         仲裁：同值则胜出，异值则冲突。
 * 用于交叉核对主引擎结果及配置排序无关性。
 */
export function runReference(rules: readonly Rule[], snapshot: Facts): EngineResult {
  // ---- 阶段一：全量求值 ----
  const table: FlatProposal[] = [];
  for (const rule of rules) {
    const trace = evaluate(rule.when, snapshot);
    for (const [field, value] of Object.entries(rule.set)) {
      table.push({
        ruleId: rule.id,
        priority: rule.priority,
        field,
        value,
        matched: trace.result,
        trace,
      });
    }
  }

  // ---- 阶段二：分组仲裁 ----
  const byField = new Map<string, FlatProposal[]>();
  for (const row of table) {
    const group = byField.get(row.field);
    if (group) group.push(row);
    else byField.set(row.field, [row]);
  }

  const facts: Record<string, FactValue> = {};
  const conflicts: FieldConflict[] = [];
  const evidence: Record<string, FieldEvidence> = {};

  for (const [field, group] of byField) {
    const matched = group
      .filter((g) => g.matched)
      .sort((a, b) => b.priority - a.priority || a.ruleId.localeCompare(b.ruleId));

    const bestPriority = matched.length > 0 ? matched[0].priority : -Infinity;
    const top = matched.filter((m) => m.priority === bestPriority);
    const distinctValues = [...new Set(top.map((t) => t.value))];

    let status: FieldEvidence['status'];
    let conflict: FieldEvidence['conflict'];

    if (top.length === 0) {
      status = 'unset';
    } else if (distinctValues.length === 1) {
      status = 'resolved';
      facts[field] = top[0].value;
    } else {
      status = 'conflict';
      conflict = { priority: bestPriority, values: distinctValues, ruleIds: top.map((t) => t.ruleId) };
      conflicts.push({ field, priority: bestPriority, values: distinctValues, ruleIds: conflict.ruleIds });
    }

    const candidates: RuleEvidence[] = group.map((g) => {
      if (!g.matched) {
        return { ruleId: g.ruleId, priority: g.priority, matched: false, trace: g.trace, outcome: 'not-matched' };
      }
      let outcome: RuleEvidence['outcome'];
      if (status === 'conflict') {
        outcome = g.priority === bestPriority ? 'conflicted' : 'loser';
      } else if (g.priority < bestPriority) {
        outcome = 'loser';
      } else {
        outcome = top.length === 1 ? 'winner' : 'co-winner';
      }
      return {
        ruleId: g.ruleId,
        priority: g.priority,
        matched: true,
        trace: g.trace,
        proposedValue: g.value,
        outcome,
      };
    });

    evidence[field] = {
      field,
      status,
      ...(status === 'resolved' ? { winnerValue: facts[field], winningPriority: bestPriority } : {}),
      ...(conflict ? { conflict } : {}),
      candidates,
    };
  }

  return { facts, conflicts, evidence };
}
