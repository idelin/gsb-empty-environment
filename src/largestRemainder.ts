import { Fraction } from './fraction.ts';
import { normalize, type ItemInput } from './model.ts';

export interface LargestRemainderDetail {
  id: string;
  ideal: Fraction;
  floor: bigint;
  remainder: Fraction;
  grantedExtra: boolean;
  allocation: bigint;
}

export type LargestRemainderResult =
  | { status: 'ok'; total: bigint; allocation: Record<string, bigint>; details: LargestRemainderDetail[] }
  | { status: 'error'; error: string };

/**
 * Largest remainder method — the BASELINE used when no extra constraints
 * (item/group bounds) apply. Rounding rule is explicit:
 *   1. every item gets floor(ideal share);
 *   2. the leftover units go to the largest exact fractional remainders;
 *   3. equal remainders are broken by stable id order (ascending).
 * Conservation holds exactly: floors never overshoot and the leftover
 * (< number of items) is handed out one unit at a time.
 */
export function largestRemainder(
  items: ItemInput[],
  total: bigint,
): LargestRemainderResult {
  const norm = normalize({ total, items });
  if (!norm.ok) return { status: 'error', error: norm.error };
  const problem = norm.problem;

  const details: LargestRemainderDetail[] = problem.items.map((it, i) => {
    const ideal = problem.ideal[i];
    const floor = ideal.floor();
    return {
      id: it.id,
      ideal,
      floor,
      remainder: ideal.sub(new Fraction(floor, 1n)),
      grantedExtra: false,
      allocation: floor,
    };
  });

  let leftover = total - details.reduce((acc, d) => acc + d.floor, 0n);

  const order = details
    .map((d, i) => ({ i, remainder: d.remainder, id: d.id }))
    .sort((a, b) => {
      const c = b.remainder.cmp(a.remainder); // larger remainder first
      if (c !== 0) return c;
      return a.id < b.id ? -1 : a.id > b.id ? 1 : 0; // stable id tie-break
    });

  for (const entry of order) {
    if (leftover <= 0n) break;
    details[entry.i].allocation += 1n;
    details[entry.i].grantedExtra = true;
    leftover -= 1n;
  }

  const allocation: Record<string, bigint> = {};
  for (const d of details) allocation[d.id] = d.allocation;
  return { status: 'ok', total, allocation, details };
}
