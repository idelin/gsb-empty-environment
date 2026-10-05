import { Fraction } from './fraction.ts';
import type { FractionLike } from './fraction.ts';
import { Grid } from './grid.ts';
import type { Cell } from './grid.ts';
import { MinHeap } from './heap.ts';

/**
 * Caller-supplied heuristic. `admissible` is a promise by the caller that
 * h never overestimates the true remaining cost; the search trusts this
 * declaration when deciding whether a result is proven optimal.
 * h(goal) is treated as 0 internally.
 */
export interface HeuristicSpec {
  h: (cell: Cell, goal: Cell, grid: Grid) => FractionLike;
  admissible: boolean;
}

export interface AStarOptions {
  heuristic?: HeuristicSpec;
  /** Expansion budget; the search stops after this many node expansions. */
  maxExpansions?: number;
}

export type SearchStatus = 'optimal' | 'feasible' | 'interrupted' | 'infeasible';

export interface AStarResult {
  status: SearchStatus;
  /** Best feasible path found so far (null when none). */
  path: Cell[] | null;
  /** Feasible upper bound: exact cost of `path`. */
  cost: Fraction | null;
  /**
   * Certified lower bound on the optimum. With an admissible heuristic it is
   * min f over the live open list; otherwise min g (still valid because edge
   * costs are non-negative). Equals `cost` once the search is exhaustive.
   */
  lowerBound: Fraction | null;
  /**
   * True only when the heuristic was declared admissible and the search
   * terminated by popping the goal or by draining the open list.
   * Always false for non-admissible heuristics, even when `exhausted`.
   */
  provenOptimal: boolean;
  /** True when no live open nodes remain (search space fully explored). */
  exhausted: boolean;
  /** True when the expansion budget ran out with live nodes left. */
  interrupted: boolean;
  expansions: number;
  /** How often an already-expanded node was expanded again (reopened). */
  reopens: number;
  /** g of the goal when it was first generated (naive early-exit answer). */
  firstGeneratedGoalCost: Fraction | null;
  /** g of the goal at its first pop (can be suboptimal if h overestimates). */
  firstGoalPopCost: Fraction | null;
}

const ZERO_HEURISTIC: HeuristicSpec = { h: () => 0, admissible: true };

export function astar(grid: Grid, start: Cell, goal: Cell, options: AStarOptions = {}): AStarResult {
  const heuristic = options.heuristic ?? ZERO_HEURISTIC;
  const maxExpansions = options.maxExpansions ?? Number.POSITIVE_INFINITY;
  if (!grid.isWalkable(start)) throw new Error('start cell is blocked');
  if (!grid.isWalkable(goal)) throw new Error('goal cell is blocked');

  const width = grid.width;
  const keyOf = (c: Cell): number => c.y * width + c.x;
  const cellOf = (key: number): Cell => ({ x: key % width, y: Math.floor(key / width) });
  const startKey = keyOf(start);
  const goalKey = keyOf(goal);

  const hOf = (cell: Cell): Fraction => {
    const key = keyOf(cell);
    if (key === goalKey) return Fraction.ZERO;
    return Fraction.of(heuristic.h(cell, goal, grid));
  };

  const gScore = new Map<number, Fraction>();
  const parent = new Map<number, number>();
  const expanded = new Set<number>();
  const heap = new MinHeap();
  let seq = 0;

  gScore.set(startKey, Fraction.ZERO);
  heap.push({ key: startKey, f: hOf(start), g: Fraction.ZERO, seq: seq++ });

  let expansions = 0;
  let reopens = 0;
  let incumbent: Fraction | null = null;
  let firstGeneratedGoalCost: Fraction | null = null;
  let firstGoalPopCost: Fraction | null = null;
  let proven = false;

  const noteGoalCost = (g: Fraction): void => {
    if (firstGeneratedGoalCost === null) firstGeneratedGoalCost = g;
    if (incumbent === null || g.lt(incumbent)) incumbent = g;
  };

  for (;;) {
    if (expansions >= maxExpansions) break;
    const entry = heap.pop();
    if (entry === undefined) break;
    const current = gScore.get(entry.key);
    if (current === undefined || !current.equals(entry.g)) continue; // stale

    if (entry.key === goalKey) {
      if (firstGoalPopCost === null) firstGoalPopCost = entry.g;
      noteGoalCost(entry.g);
      if (heuristic.admissible) {
        // With an admissible h, the first goal pop settles the optimum.
        proven = true;
        break;
      }
      continue; // h may overestimate: keep looking for a cheaper goal entry
    }

    if (incumbent !== null && entry.g.gte(incumbent)) continue; // cannot improve

    if (expanded.has(entry.key)) {
      reopens++;
    } else {
      expanded.add(entry.key);
    }
    expansions++;

    const from = cellOf(entry.key);
    for (const step of grid.neighbors(from)) {
      const tentative = entry.g.add(step.cost);
      const nKey = keyOf(step.cell);
      const old = gScore.get(nKey);
      if (old !== undefined && tentative.gte(old)) continue;
      gScore.set(nKey, tentative);
      parent.set(nKey, entry.key);
      heap.push({ key: nKey, f: tentative.add(hOf(step.cell)), g: tentative, seq: seq++ });
      if (nKey === goalKey) noteGoalCost(tentative);
    }
  }

  // Scan remaining open entries: drop stale and incumbent-dominated ones.
  let lowerBound: Fraction | null = null;
  let hasLive = false;
  for (const e of heap.raw()) {
    const current = gScore.get(e.key);
    if (current === undefined || !current.equals(e.g)) continue;
    if (incumbent !== null && e.g.gte(incumbent)) continue;
    hasLive = true;
    const bound = heuristic.admissible ? e.f : e.g;
    if (lowerBound === null || bound.lt(lowerBound)) lowerBound = bound;
  }
  const exhausted = !hasLive;
  if (incumbent !== null && (lowerBound === null || incumbent.lt(lowerBound))) {
    lowerBound = incumbent;
  }

  const interrupted = hasLive && expansions >= maxExpansions;
  const provenOptimal = heuristic.admissible && incumbent !== null && (proven || exhausted);

  let path: Cell[] | null = null;
  if (incumbent !== null) {
    const rev: Cell[] = [];
    let k: number | undefined = goalKey;
    while (k !== undefined) {
      rev.push(cellOf(k));
      k = parent.get(k);
    }
    rev.reverse();
    path = rev;
  }

  let status: SearchStatus;
  if (provenOptimal) {
    status = 'optimal';
  } else if (interrupted) {
    status = 'interrupted';
  } else if (incumbent !== null) {
    status = 'feasible';
  } else {
    status = 'infeasible';
  }

  return {
    status,
    path,
    cost: incumbent,
    lowerBound,
    provenOptimal,
    exhausted,
    interrupted,
    expansions,
    reopens,
    firstGeneratedGoalCost,
    firstGoalPopCost,
  };
}
