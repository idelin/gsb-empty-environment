import { evaluateCondition, validateCondition, type AttrValue, type Condition } from './conditions.ts';
import { attrKey, parseAttrRef, type AttributeStore } from './attributes.ts';

export interface Rule {
  readonly id: string;
  readonly effect: 'allow' | 'deny';
  readonly when: Condition;
}

export interface Policy {
  readonly id: string;
  /** 规则版本：任何规则变更都必须递增，否则旧缓存结果可能被错误沿用。 */
  readonly version: number;
  readonly rules: readonly Rule[];
}

export function validatePolicy(policy: Policy): void {
  if (!policy.id) throw new TypeError('策略缺少 id');
  if (!Number.isInteger(policy.version) || policy.version < 1) {
    throw new TypeError(`策略版本必须是正整数，收到 ${String(policy.version)}`);
  }
  for (const rule of policy.rules) validateCondition(rule.when, `rule "${rule.id}"`);
}

export type Decision = 'allow' | 'deny';

export interface Evaluation {
  readonly decision: Decision;
  /** 本次判定实际读取的属性快照：规范键 -> 读取到的值（undefined 表示读取时缺失）。 */
  readonly reads: ReadonlyMap<string, AttrValue | undefined>;
}

/**
 * 对 (subject, context) 求值策略，并记录实际读取的属性。
 * 拒绝优先：任一 deny 规则命中即拒绝；否则任一 allow 规则命中即允许；
 * 都不命中默认拒绝。规则短路求值，未评估到的规则不会读取属性。
 */
export function evaluatePolicy(
  policy: Policy,
  subjectId: string,
  contextId: string,
  store: AttributeStore,
): Evaluation {
  const reads = new Map<string, AttrValue | undefined>();
  const read = (ref: string): AttrValue | undefined => {
    const { scope, name } = parseAttrRef(ref);
    const key = attrKey(scope, scope === 'subject' ? subjectId : contextId, name);
    if (!reads.has(key)) reads.set(key, store.getByKey(key));
    return reads.get(key);
  };

  for (const rule of policy.rules) {
    if (rule.effect === 'deny' && evaluateCondition(rule.when, read)) {
      return { decision: 'deny', reads };
    }
  }
  for (const rule of policy.rules) {
    if (rule.effect === 'allow' && evaluateCondition(rule.when, read)) {
      return { decision: 'allow', reads };
    }
  }
  return { decision: 'deny', reads };
}
