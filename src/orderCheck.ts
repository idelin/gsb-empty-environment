import { runEngine } from './engine.ts';
import { runReference } from './reference.ts';
import { normalizeResult } from './canonical.ts';
import type { Facts, Rule } from './types.ts';

export interface OrderCheckOptions {
  /** 随机洗牌次数（种子确定，可重现）。默认 16。 */
  readonly shuffles?: number;
  /** 规则数不超过该值时做全排列核对。默认 6（720 种）。 */
  readonly exhaustiveUpTo?: number;
  readonly seed?: number;
}

export interface OrderMismatch {
  readonly permutation: readonly string[];
  readonly baseline: 'engine' | 'reference';
  readonly expectedHash: string;
  readonly actualHash: string;
}

export interface OrderCheckReport {
  readonly checkedPermutations: number;
  readonly exhaustive: boolean;
  readonly consistent: boolean;
  readonly mismatches: readonly OrderMismatch[];
}

/** mulberry32：确定性伪随机数，保证核对可重现。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function shuffled<T>(items: readonly T[], rand: () => number): T[] {
  const out = [...items];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

function* permutations<T>(items: readonly T[]): Generator<T[]> {
  if (items.length <= 1) {
    yield [...items];
    return;
  }
  for (let i = 0; i < items.length; i++) {
    const rest = [...items.slice(0, i), ...items.slice(i + 1)];
    for (const tail of permutations(rest)) yield [items[i], ...tail];
  }
}

function buildPermutations(rules: readonly Rule[], opts: Required<OrderCheckOptions>): Rule[][] {
  const perms: Rule[][] = [
    [...rules],
    [...rules].reverse(),
    [...rules].sort((a, b) => a.id.localeCompare(b.id)),
    [...rules].sort((a, b) => a.priority - b.priority || a.id.localeCompare(b.id)),
  ];
  const rand = mulberry32(opts.seed);
  for (let i = 0; i < opts.shuffles; i++) perms.push(shuffled(rules, rand));
  if (rules.length <= opts.exhaustiveUpTo) {
    return [...permutations(rules)];
  }
  return perms;
}

/**
 * 排序无关性核对：对规则配置的一组排列（小规模时全排列），
 * 分别运行主引擎与朴素两阶段参考模型，规范化后逐字节比较。
 * 三者（任意排列的主引擎、任意排列的参考模型、基线）必须一致。
 */
export function checkOrderIndependence(
  rules: readonly Rule[],
  snapshot: Facts,
  options: OrderCheckOptions = {},
): OrderCheckReport {
  const opts: Required<OrderCheckOptions> = {
    shuffles: options.shuffles ?? 16,
    exhaustiveUpTo: options.exhaustiveUpTo ?? 6,
    seed: options.seed ?? 0x5eed,
  };
  const perms = buildPermutations(rules, opts);
  const mismatches: OrderMismatch[] = [];
  let baselineHash: string | undefined;

  for (const perm of perms) {
    const engineHash = normalizeResult(runEngine(perm, snapshot));
    const referenceHash = normalizeResult(runReference(perm, snapshot));
    baselineHash ??= engineHash;

    if (engineHash !== referenceHash) {
      mismatches.push({
        permutation: perm.map((r) => r.id),
        baseline: 'reference',
        expectedHash: referenceHash,
        actualHash: engineHash,
      });
    }
    if (engineHash !== baselineHash) {
      mismatches.push({
        permutation: perm.map((r) => r.id),
        baseline: 'engine',
        expectedHash: baselineHash,
        actualHash: engineHash,
      });
    }
  }

  return {
    checkedPermutations: perms.length,
    exhaustive: rules.length <= opts.exhaustiveUpTo,
    consistent: mismatches.length === 0,
    mismatches,
  };
}
