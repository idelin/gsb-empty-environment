/**
 * Visual reordering from PRE-ASSIGNED embedding levels.
 *
 * This intentionally implements only the level -> visual-order stage of
 * UAX #9 (the L2 / "reverse by levels" stage), not bidi classification.
 */

import type { Level } from './types.ts';

/**
 * Reverse-by-levels: from the highest level down to level 1, reverse every
 * maximal run of items whose level is >= the current level.
 *
 * Levels are assumed non-negative. The input is a permutation index array;
 * the result gives logical indices in visual left-to-right order.
 */
export function reorderByLevels(levels: readonly Level[]): number[] {
  const order = levels.map((_, i) => i);
  if (order.length < 2) return order;
  const maxLevel = Math.max(...levels);
  for (let level = maxLevel; level >= 1; level--) {
    let i = 0;
    while (i < order.length) {
      if (levels[order[i]]! < level) {
        i++;
        continue;
      }
      let j = i;
      while (j < order.length && levels[order[j]!]! >= level) j++;
      reverseRange(order, i, j);
      i = j;
    }
  }
  return order;
}

function reverseRange(arr: number[], from: number, to: number): void {
  let lo = from;
  let hi = to - 1;
  while (lo < hi) {
    const tmp = arr[lo]!;
    arr[lo] = arr[hi]!;
    arr[hi] = tmp;
    lo++;
    hi--;
  }
}

/**
 * Independent per-segment reference implementation, written directly from
 * the recursive UAX #9 rule rather than from the reverse-by-levels loop,
 * so agreement between the two is a meaningful cross-check.
 *
 * For the current segment take its minimum level m:
 *   - split into tokens: items at level m, and sub-segments above m;
 *   - even m -> emit tokens in logical order;
 *   - odd  m -> emit tokens in reverse logical order;
 *   - recurse into every sub-segment.
 */
export function reorderReference(levels: readonly Level[]): number[] {
  const emit = (seg: number[]): number[] => {
    if (seg.length === 0) return [];
    const minLevel = Math.min(...seg.map((i) => levels[i]!));
    const tokens: Array<{ kind: 'leaf'; index: number } | { kind: 'seg'; items: number[] }> = [];
    let run: number[] | null = null;
    for (const index of seg) {
      if (levels[index]! === minLevel) {
        run = null;
        tokens.push({ kind: 'leaf', index });
      } else {
        if (!run) {
          run = [];
          tokens.push({ kind: 'seg', items: run });
        }
        run.push(index);
      }
    }
    if ((minLevel & 1) === 1) tokens.reverse();
    const out: number[] = [];
    for (const token of tokens) {
      if (token.kind === 'leaf') out.push(token.index);
      else out.push(...emit(token.items));
    }
    return out;
  };
  return emit(levels.map((_, i) => i));
}

/**
 * Enumerate every bounded level sequence of length n over [0..maxLevel],
 * for exhaustive round-trip checking.
 */
export function* allLevelSequences(n: number, maxLevel: number): Generator<Level[]> {
  if (n === 0) {
    yield [];
    return;
  }
  const seq = new Array<Level>(n).fill(0);
  const total = (maxLevel + 1) ** n;
  for (let counter = 0; counter < total; counter++) {
    let v = counter;
    for (let i = 0; i < n; i++) {
      seq[i] = v % (maxLevel + 1);
      v = Math.floor(v / (maxLevel + 1));
    }
    yield seq.slice();
  }
}
