import type { Level } from "./types.ts";

export interface LevelRun<T> {
  level: Level;
  items: T[];
}

/** Group adjacent equal-level logical items into runs. */
export function runs<T>(levels: readonly Level[], items: readonly T[]): LevelRun<T>[] {
  const out: LevelRun<T>[] = [];
  for (let i = 0; i < levels.length; i++) {
    const level = levels[i]!;
    const item = items[i]!;
    const last = out[out.length - 1];
    if (last !== undefined && last.level === level) last.items.push(item);
    else out.push({ level, items: [item] });
  }
  return out;
}

/**
 * UBA X2 / L2 style visual reordering for a fragment whose direction levels
 * are already resolved (no weak-type resolution performed here).
 *
 * From the highest level down to 1, reverse every contiguous run of items
 * whose level is >= the current level. Missing intermediate levels are still
 * visited; together with parity this realizes the nested mirror semantics.
 *
 * Returns logical indices in visual (left -> right) order.
 */
export function visualOrder(levels: readonly Level[]): number[] {
  const order = levels.map((_, i) => i);
  if (order.length <= 1) return order;
  let maxLevel = 0;
  for (const level of levels) if (level > maxLevel) maxLevel = level;
  for (let level = maxLevel; level >= 1; level--) {
    let start = -1;
    for (let i = 0; i <= order.length; i++) {
      const inside = i < order.length && levels[order[i]!]! >= level;
      if (inside && start === -1) start = i;
      if (!inside && start !== -1) {
        reverseRange(order, start, i - 1);
        start = -1;
      }
    }
  }
  return order;
}

function reverseRange(arr: number[], lo: number, hi: number): void {
  while (lo < hi) {
    const tmp = arr[lo]!;
    arr[lo] = arr[hi]!;
    arr[hi] = tmp;
    lo++;
    hi--;
  }
}
