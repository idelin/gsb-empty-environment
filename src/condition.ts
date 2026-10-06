import type { Condition, ConditionTrace, Facts } from './types.ts';

function evalLeaf(cond: Condition, snapshot: Facts): boolean {
  switch (cond.op) {
    case 'eq':
      return Object.is(snapshot[cond.field], cond.value);
    case 'neq':
      return !Object.is(snapshot[cond.field], cond.value);
    case 'lt':
    case 'lte':
    case 'gt':
    case 'gte': {
      const actual = snapshot[cond.field];
      if (typeof actual !== 'number') return false;
      if (cond.op === 'lt') return actual < cond.value;
      if (cond.op === 'lte') return actual <= cond.value;
      if (cond.op === 'gt') return actual > cond.value;
      return actual >= cond.value;
    }
    case 'in':
      return cond.values.some((v) => Object.is(snapshot[cond.field], v));
    case 'exists':
      return cond.field in snapshot;
    default:
      throw new Error(`not a leaf condition: ${cond.op}`);
  }
}

/**
 * 对快照求值并返回完整轨迹。求值只读取 snapshot，
 * 与本轮任何规则的赋值结果隔离（快照语义）。
 */
export function evaluate(cond: Condition, snapshot: Facts): ConditionTrace {
  switch (cond.op) {
    case 'and': {
      const children = cond.all.map((c) => evaluate(c, snapshot));
      return { condition: cond, result: children.every((c) => c.result), children };
    }
    case 'or': {
      const children = cond.any.map((c) => evaluate(c, snapshot));
      return { condition: cond, result: children.some((c) => c.result), children };
    }
    case 'not': {
      const child = evaluate(cond.cond, snapshot);
      return { condition: cond, result: !child.result, children: [child] };
    }
    default:
      return { condition: cond, result: evalLeaf(cond, snapshot), children: [] };
  }
}
