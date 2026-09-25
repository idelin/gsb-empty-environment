/**
 * 策略求值引擎。
 *
 * - 规则携带 permit/deny 效果与义务；条件只读显式属性。
 * - deny 优先：存在适用 deny 时判定为拒绝，允许义务不执行，
 *   但被抑制的允许义务会显式记录（绝不静默丢弃）。
 * - 撤回规则后重新求值，先前被抑制的义务随之恢复。
 */
import type { AttributeStore } from './attributes.ts';
import { evalCondition, type Condition } from './conditions.ts';
import type { Obligation } from './obligations.ts';
import { mergeObligations, type MergeResult } from './solver.ts';

export interface Rule {
  id: string;
  effect: 'permit' | 'deny';
  condition: Condition;
  obligations: Obligation[];
  /** 撤回标记：被撤回的规则不参与求值 */
  withdrawn?: boolean;
}

export interface Policy {
  rules: Rule[];
}

export interface SuppressedObligation {
  obligation: Obligation;
  fromRule: string;
  suppressedBy: string[];
}

export interface Decision {
  effect: 'permit' | 'deny';
  /** 适用的规则 id（按声明顺序） */
  applicableRules: string[];
  /** 被 deny 抑制而未执行的允许义务（显式保留，不丢弃） */
  suppressed: SuppressedObligation[];
  /** 生效义务的合并结果 */
  merged: MergeResult;
}

export function evaluate(policy: Policy, attrs: AttributeStore): Decision {
  const active = policy.rules.filter((r) => r.withdrawn !== true);
  const applicable = active.filter((r) => evalCondition(r.condition, attrs));
  const denies = applicable.filter((r) => r.effect === 'deny');
  const permits = applicable.filter((r) => r.effect === 'permit');

  const denyIds = denies.map((r) => r.id);
  const suppressed: SuppressedObligation[] = [];

  let effectiveObligations: Obligation[];
  let effect: 'permit' | 'deny';

  if (denies.length > 0) {
    // deny 不执行允许义务：允许义务被抑制并显式记录来源与抑制者。
    effect = 'deny';
    for (const rule of permits) {
      for (const obligation of rule.obligations) {
        suppressed.push({ obligation, fromRule: rule.id, suppressedBy: denyIds });
      }
    }
    effectiveObligations = denies.flatMap((r) => r.obligations);
  } else {
    effect = 'permit';
    effectiveObligations = permits.flatMap((r) => r.obligations);
  }

  return {
    effect,
    applicableRules: applicable.map((r) => r.id),
    suppressed,
    merged: mergeObligations(effectiveObligations),
  };
}

/** 撤回规则：返回新策略（原策略不变），重新求值即可恢复被抑制的义务。 */
export function withdrawRule(policy: Policy, ruleId: string): Policy {
  let found = false;
  const rules = policy.rules.map((r) => {
    if (r.id === ruleId) {
      found = true;
      return { ...r, withdrawn: true };
    }
    return r;
  });
  if (!found) throw new Error(`规则不存在: ${ruleId}`);
  return { rules };
}

/** 恢复被撤回的规则。 */
export function reinstateRule(policy: Policy, ruleId: string): Policy {
  let found = false;
  const rules = policy.rules.map((r) => {
    if (r.id === ruleId) {
      found = true;
      return { ...r, withdrawn: false };
    }
    return r;
  });
  if (!found) throw new Error(`规则不存在: ${ruleId}`);
  return { rules };
}
