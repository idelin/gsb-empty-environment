import type { Rational } from './rational.ts';
import { lcm } from './rational.ts';

/**
 * Largest remainder method (Hamilton), the baseline used when no extra
 * constraints exist.
 *
 * Explicit rounding rule:
 *   1. Scale all rational weights to a common denominator, giving integer
 *      numerators N_i and total scale S = sum(N_i).
 *   2. Ideal share of item i is the exact rational q_i = T * N_i / S.
 *   3. Each item receives floor(q_i) (exact BigInt floor division).
 *   4. The remaining R = T - sum(floors) units go one each to the R items
 *      with the largest remainders r_i = T*N_i - floor_i*S.
 *   5. Ties on equal remainders are broken by stable ID: ascending
 *      lexicographic order of the item id string. Input order is irrelevant.
 */

export interface ScaledWeights {
  /** Item ids, sorted ascending (stable order). */
  readonly ids: readonly string[];
  /** Integer numerators on a common denominator, aligned with ids. */
  readonly numerators: readonly bigint[];
  /** S = sum(numerators); ideal share of item i is T * N_i / S. */
  readonly scale: bigint;
}

export function scaleWeights(
  items: readonly { id: string; weight: Rational }[],
): ScaledWeights {
  if (items.length === 0) throw new Error('apportion: no items');
  const seen = new Set<string>();
  let commonDen = 1n;
  for (const it of items) {
    if (seen.has(it.id)) throw new Error(`apportion: duplicate id: ${it.id}`);
    seen.add(it.id);
    if (it.weight.num < 0n) {
      throw new Error(`apportion: negative weight rejected for id ${it.id}`);
    }
    commonDen = lcm(commonDen, it.weight.den);
  }
  const sorted = [...items].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  const numerators = sorted.map((it) => it.weight.num * (commonDen / it.weight.den));
  const scale = numerators.reduce((acc, v) => acc + v, 0n);
  if (scale <= 0n) throw new Error('apportion: total weight must be positive');
  return { ids: sorted.map((it) => it.id), numerators, scale };
}

/**
 * Largest remainder apportionment. Returns allocation aligned with `ids`.
 * Guarantees: sum of result === total (conservation); every remainder tie
 * resolved by ascending stable id.
 */
export function largestRemainder(
  ids: readonly string[],
  numerators: readonly bigint[],
  scale: bigint,
  total: bigint,
): bigint[] {
  if (total < 0n) throw new Error('apportion: negative total rejected');
  const n = ids.length;
  const floors = new Array<bigint>(n);
  const remainders = new Array<bigint>(n);
  let used = 0n;
  for (let i = 0; i < n; i++) {
    const exact = total * numerators[i];
    floors[i] = exact / scale; // non-negative => truncation is floor
    remainders[i] = exact - floors[i] * scale;
    used += floors[i];
  }
  let rest = total - used; // 0 <= rest < n
  const order = [...Array(n).keys()].sort((a, b) => {
    if (remainders[a] !== remainders[b]) {
      return remainders[a] > remainders[b] ? -1 : 1;
    }
    return ids[a] < ids[b] ? -1 : ids[a] > ids[b] ? 1 : 0;
  });
  const out = [...floors];
  for (let k = 0n; k < rest; k++) {
    out[order[Number(k)]] += 1n;
  }
  return out;
}

/**
 * Exact scaled total deviation of an allocation from the ideal shares:
 *   sum_i |x_i * S - T * N_i|   (units of 1/S of one allocation unit).
 * Pure BigInt; comparable across candidate allocations of the same problem.
 */
export function scaledDeviation(
  values: readonly bigint[],
  numerators: readonly bigint[],
  scale: bigint,
  total: bigint,
): bigint {
  let dev = 0n;
  for (let i = 0; i < values.length; i++) {
    const d = values[i] * scale - total * numerators[i];
    dev += d < 0n ? -d : d;
  }
  return dev;
}
