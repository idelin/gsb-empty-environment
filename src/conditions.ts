/**
 * 条件语言：仅限整数比较、有限集合成员判断、布尔运算。
 * 无任何外部调用能力，求值是纯函数。
 */
import type { AttributeStore } from './attributes.ts';

export type Condition =
  | { op: 'intCmp'; attr: string; cmp: '<' | '<=' | '==' | '!=' | '>=' | '>'; value: number }
  | { op: 'inSet'; attr: string; value: string }
  | { op: 'setContainsAll'; attr: string; values: readonly string[] }
  | { op: 'boolIs'; attr: string; value: boolean }
  | { op: 'not'; cond: Condition }
  | { op: 'and'; conds: readonly Condition[] }
  | { op: 'or'; conds: readonly Condition[] };

export function evalCondition(cond: Condition, attrs: AttributeStore): boolean {
  switch (cond.op) {
    case 'intCmp': {
      const actual = attrs.getInt(cond.attr);
      switch (cond.cmp) {
        case '<': return actual < cond.value;
        case '<=': return actual <= cond.value;
        case '==': return actual === cond.value;
        case '!=': return actual !== cond.value;
        case '>=': return actual >= cond.value;
        case '>': return actual > cond.value;
      }
      break;
    }
    case 'inSet':
      return attrs.getSet(cond.attr).includes(cond.value);
    case 'setContainsAll': {
      const set = attrs.getSet(cond.attr);
      return cond.values.every((v) => set.includes(v));
    }
    case 'boolIs':
      return attrs.getBool(cond.attr) === cond.value;
    case 'not':
      return !evalCondition(cond.cond, attrs);
    case 'and':
      return cond.conds.every((c) => evalCondition(c, attrs));
    case 'or':
      return cond.conds.some((c) => evalCondition(c, attrs));
  }
}
