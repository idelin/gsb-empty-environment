import { Fraction } from './fraction.ts';
import { Grid } from './grid.ts';
import type { Cell } from './grid.ts';
import { MinHeap } from './heap.ts';

export interface DijkstraResult {
  path: Cell[] | null;
  cost: Fraction | null;
  expansions: number;
}

/**
 * Plain Dijkstra used as an independent reference to cross-check A*.
 * Handles zero-cost edges via lazy deletion and strict-improvement updates.
 */
export function dijkstra(grid: Grid, start: Cell, goal: Cell): DijkstraResult {
  if (!grid.isWalkable(start)) throw new Error('start cell is blocked');
  if (!grid.isWalkable(goal)) throw new Error('goal cell is blocked');

  const width = grid.width;
  const keyOf = (c: Cell): number => c.y * width + c.x;
  const cellOf = (key: number): Cell => ({ x: key % width, y: Math.floor(key / width) });
  const startKey = keyOf(start);
  const goalKey = keyOf(goal);

  const dist = new Map<number, Fraction>();
  const parent = new Map<number, number>();
  const heap = new MinHeap();
  let seq = 0;

  dist.set(startKey, Fraction.ZERO);
  heap.push({ key: startKey, f: Fraction.ZERO, g: Fraction.ZERO, seq: seq++ });

  let expansions = 0;
  let finalCost: Fraction | null = null;

  for (;;) {
    const entry = heap.pop();
    if (entry === undefined) break;
    const current = dist.get(entry.key);
    if (current === undefined || !current.equals(entry.g)) continue; // stale
    if (entry.key === goalKey) {
      finalCost = entry.g;
      break;
    }
    expansions++;
    const from = cellOf(entry.key);
    for (const step of grid.neighbors(from)) {
      const tentative = entry.g.add(step.cost);
      const nKey = keyOf(step.cell);
      const old = dist.get(nKey);
      if (old !== undefined && tentative.gte(old)) continue;
      dist.set(nKey, tentative);
      parent.set(nKey, entry.key);
      heap.push({ key: nKey, f: tentative, g: tentative, seq: seq++ });
    }
  }

  let path: Cell[] | null = null;
  if (finalCost !== null) {
    const rev: Cell[] = [];
    let k: number | undefined = goalKey;
    while (k !== undefined) {
      rev.push(cellOf(k));
      k = parent.get(k);
    }
    rev.reverse();
    path = rev;
  }

  return { path, cost: finalCost, expansions };
}
