// 可恢复的精度细化比较协议。
// 判定规则（对差值证据 d = a - b）：
//   lower(d) > 0        -> gt
//   upper(d) < 0        -> lt
//   d 无误差项且中心为 0 -> eq（唯一的相等证据：精确相等，绝不用 epsilon）
//   否则                -> 区间重叠；若预算内可细化则细化后重判，否则保持 pending/undecided

import { cmp, ZERO } from './rational.ts';
import type { Rational } from './rational.ts';
import { boundsOf, evidenceOf, refineValue, subtractEvidence } from './value.ts';
import type { CompareValue } from './value.ts';

export type Verdict = 'lt' | 'eq' | 'gt' | 'undecided';

export interface TraceEntry {
  readonly step: number; // 已消耗的细化步数
  readonly lower: Rational; // 当前 a-b 的区间下界
  readonly upper: Rational; // 当前 a-b 的区间上界
  readonly exact: boolean; // 当前差值是否无误差项
}

export interface CompareState {
  readonly a: CompareValue;
  readonly b: CompareValue;
  readonly stepsUsed: number;
  readonly status: 'done' | 'pending';
  readonly verdict: Verdict; // pending 时恒为 'undecided'
  readonly trace: readonly TraceEntry[];
}

export function compareExact(a: Rational, b: Rational): 'lt' | 'eq' | 'gt' {
  const c = cmp(a, b);
  return c < 0 ? 'lt' : c > 0 ? 'gt' : 'eq';
}

function evaluate(
  a: CompareValue,
  b: CompareValue,
  stepsUsed: number,
  trace: readonly TraceEntry[],
  budget: number,
): CompareState {
  let va = a;
  let vb = b;
  let used = stepsUsed;
  let remaining = budget;
  const log = [...trace];
  for (;;) {
    const diff = subtractEvidence(evidenceOf(va), evidenceOf(vb));
    const { lower, upper } = boundsOf(diff);
    const isExact = diff.errors.length === 0;
    log.push({ step: used, lower, upper, exact: isExact });
    if (cmp(lower, ZERO) > 0) {
      return { a: va, b: vb, stepsUsed: used, status: 'done', verdict: 'gt', trace: log };
    }
    if (cmp(upper, ZERO) < 0) {
      return { a: va, b: vb, stepsUsed: used, status: 'done', verdict: 'lt', trace: log };
    }
    if (isExact) {
      // 无误差且未分离，当且仅当中心恰为 0：这是唯一的“确切相等”证据。
      return { a: va, b: vb, stepsUsed: used, status: 'done', verdict: 'eq', trace: log };
    }
    const ra = refineValue(va);
    const rb = refineValue(vb);
    if (remaining > 0 && (ra !== null || rb !== null)) {
      va = ra ?? va;
      vb = rb ?? vb;
      used += 1;
      remaining -= 1;
      continue;
    }
    return { a: va, b: vb, stepsUsed: used, status: 'pending', verdict: 'undecided', trace: log };
  }
}

export function startCompare(a: CompareValue, b: CompareValue): CompareState {
  return evaluate(a, b, 0, [], 0);
}

// 用追加预算恢复一个 pending 状态的比较；done 状态直接原样返回（幂等）。
export function resumeCompare(state: CompareState, budget: number): CompareState {
  if (state.status === 'done' || budget <= 0) return state;
  return evaluate(state.a, state.b, state.stepsUsed, state.trace, budget);
}

export function compare(a: CompareValue, b: CompareValue, budget = 0): CompareState {
  return evaluate(a, b, 0, [], budget);
}
