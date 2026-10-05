import { Fraction } from './fraction.ts';
import type { FractionLike } from './fraction.ts';
import type { HeuristicSpec } from './astar.ts';
import type { Cell, Grid } from './grid.ts';

/** Zero heuristic (turns A* into Dijkstra). Always admissible. */
export function zeroHeuristic(): HeuristicSpec {
  return { h: () => 0, admissible: true };
}

/**
 * Manhattan distance scaled by a rational factor. Admissible on a grid when
 * `scale` does not exceed the minimum cell cost; the caller decides what to
 * declare via `declaredAdmissible`.
 */
export function manhattanHeuristic(scale: FractionLike, declaredAdmissible: boolean): HeuristicSpec {
  const s = Fraction.of(scale);
  if (s.isNegative()) throw new Error('heuristic scale must be non-negative');
  return {
    h: (cell: Cell, goal: Cell, _grid: Grid): Fraction => {
      const d = Math.abs(cell.x - goal.x) + Math.abs(cell.y - goal.y);
      return s.mul(d);
    },
    admissible: declaredAdmissible,
  };
}

/**
 * Convenience: Manhattan scaled by the grid's minimum cell cost, which is
 * guaranteed admissible (and consistent) for 4-connected grids.
 */
export function admissibleManhattan(grid: Grid): HeuristicSpec {
  return manhattanHeuristic(grid.minCost(), true);
}
