/**
 * 无缓存重算参考实现。
 *
 * 每次调用都新建验证器与解析缓存，从带外信任锚与证明束重新推导，
 * 用于与宿主决策结果做独立对照；多次重算必须得到逐字段一致的结论。
 */

import { decideCookieCarry, type DecisionContext } from './decision.ts';
import type { CarryDecision } from './types.ts';

function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(stableStringify).join(',')}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  return `{${keys.map((key) => `${JSON.stringify(key)}:${stableStringify(record[key])}`).join(',')}}`;
}

/** 去掉每次重算都会变化的计时刻字段，得到稳定指纹。 */
export function decisionFingerprint(decision: CarryDecision): string {
  const { computedAtMs: _ignored, ...stable } = decision;
  void _ignored;
  return stableStringify(stable);
}

export interface RecomputationCheck {
  readonly consistent: boolean;
  readonly first: CarryDecision;
  readonly second: CarryDecision;
  readonly fingerprint: string;
}

/**
 * 以两个不同的 nowMs 独立重算同一快照，确认结论完全一致。
 */
export function recomputeReference(context: DecisionContext, secondNowMs: number): RecomputationCheck {
  const first = decideCookieCarry(context);
  const second = decideCookieCarry({ ...context, nowMs: secondNowMs });
  const firstFingerprint = decisionFingerprint(first);
  const secondFingerprint = decisionFingerprint(second);
  return {
    consistent: firstFingerprint === secondFingerprint,
    first,
    second,
    fingerprint: firstFingerprint,
  };
}
