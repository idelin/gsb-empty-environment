/**
 * 最小反例搜索：在有限有理输入域内，按"复杂度"（|分子|+分母）递增枚举输入组合，
 * 找到使两条流水产生分歧的最小输入；预算耗尽而未找到时明确报告 budget-exhausted（未决），
 * 搜索空间耗尽而未找到时报告 exhausted-space。
 */

import { comparePipelines } from './compare.ts';
import type { ComparisonResult } from './compare.ts';
import type { Pipeline } from './dag.ts';
import { Rat } from './rational.ts';

export interface SearchOptions {
  sources: string[];
  /** 分子取值范围 [min, max]（闭区间）。 */
  numeratorRange: [bigint, bigint];
  /** 允许的分母（正整数）。 */
  denominators: bigint[];
  /** 最大求值次数（预算）。 */
  budget: number;
  /** 反例判定：'final' 仅看最终输出；'any' 中间分歧也算。 */
  mode?: 'final' | 'any';
}

export type SearchResult =
  | { status: 'found'; inputs: Record<string, Rat>; comparison: ComparisonResult; checked: number }
  | { status: 'exhausted-space'; checked: number }
  | { status: 'budget-exhausted'; checked: number };

function complexity(r: Rat): bigint {
  const an = r.n < 0n ? -r.n : r.n;
  return an + r.d;
}

function cmpBig(a: bigint, b: bigint): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/** 生成去重后的候选值，按 (|n|+d, d, n) 排序，保证"最小优先"。 */
export function candidates(numeratorRange: [bigint, bigint], denominators: bigint[]): Rat[] {
  const seen = new Set<string>();
  const list: Rat[] = [];
  for (const d of denominators) {
    if (d <= 0n) throw new Error(`分母必须为正: ${d}`);
    for (let n = numeratorRange[0]; n <= numeratorRange[1]; n++) {
      const r = Rat.of(n, d);
      const k = r.toString();
      if (!seen.has(k)) {
        seen.add(k);
        list.push(r);
      }
    }
  }
  list.sort((x, y) => {
    const c = cmpBig(complexity(x), complexity(y));
    if (c !== 0) return c;
    const cd = cmpBig(x.d, y.d);
    if (cd !== 0) return cd;
    return cmpBig(x.n, y.n);
  });
  return list;
}

/** 枚举长度为 len、元素取自 [0..k]、且最大值恰为 k 的下标元组。 */
function* tuplesWithMax(len: number, k: number, prefix: number[] = []): Generator<number[]> {
  if (len === 0) {
    if (prefix.length > 0 && Math.max(...prefix) === k) yield prefix;
    return;
  }
  for (let i = 0; i <= k; i++) yield* tuplesWithMax(len - 1, k, [...prefix, i]);
}

function isCounterexample(cmp: ComparisonResult, mode: 'final' | 'any'): boolean {
  if (cmp.finalStatus !== 'equal') return true;
  return mode === 'any' && cmp.intermediateOnly.length > 0;
}

export function searchCounterexample(a: Pipeline, b: Pipeline, opts: SearchOptions): SearchResult {
  const mode = opts.mode ?? 'final';
  const cands = candidates(opts.numeratorRange, opts.denominators);
  let checked = 0;
  for (let k = 0; k < cands.length; k++) {
    for (const idx of tuplesWithMax(opts.sources.length, k)) {
      if (checked >= opts.budget) return { status: 'budget-exhausted', checked };
      const inputs: Record<string, Rat> = {};
      opts.sources.forEach((s, i) => {
        inputs[s] = cands[idx[i]];
      });
      checked++;
      let cmp: ComparisonResult;
      try {
        cmp = comparePipelines(a, b, inputs);
      } catch {
        continue; // 输入落在定义域外（如除零），跳过，不计为反例
      }
      if (isCounterexample(cmp, mode)) {
        return { status: 'found', inputs, comparison: cmp, checked };
      }
    }
  }
  return { status: 'exhausted-space', checked };
}
