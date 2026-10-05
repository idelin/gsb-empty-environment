import { readFileSync } from 'node:fs';
import { Grid } from './grid.ts';
import { astar } from './astar.ts';
import type { HeuristicSpec } from './astar.ts';
import { dijkstra } from './dijkstra.ts';
import { zeroHeuristic, manhattanHeuristic } from './heuristics.ts';
import type { Cell } from './grid.ts';

/**
 * Usage: node src/cli.ts <map.json>
 *
 * map.json:
 * {
 *   "grid": [[1, "1/3", 0], [null, 2, 1]],   // null = blocked
 *   "start": [0, 0],
 *   "goal": [2, 1],
 *   "heuristic": {"type": "manhattan", "scale": "1/2", "admissible": false},
 *   "maxExpansions": 100
 * }
 */

interface MapSpec {
  grid: (number | string | null)[][];
  start: [number, number];
  goal: [number, number];
  heuristic?: { type?: string; scale?: number | string; admissible?: boolean };
  maxExpansions?: number;
}

function buildHeuristic(spec: MapSpec['heuristic']): HeuristicSpec {
  if (!spec || spec.type === undefined || spec.type === 'zero') return zeroHeuristic();
  if (spec.type === 'manhattan') {
    return manhattanHeuristic(spec.scale ?? 1, spec.admissible ?? true);
  }
  throw new Error('unknown heuristic type: ' + String(spec.type));
}

function main(): void {
  const file = process.argv[2];
  if (!file) {
    console.error('usage: node src/cli.ts <map.json>');
    process.exit(2);
  }
  const spec = JSON.parse(readFileSync(file, 'utf8')) as MapSpec;
  const grid = new Grid(spec.grid);
  const start: Cell = { x: spec.start[0], y: spec.start[1] };
  const goal: Cell = { x: spec.goal[0], y: spec.goal[1] };

  const result = astar(grid, start, goal, {
    heuristic: buildHeuristic(spec.heuristic),
    maxExpansions: spec.maxExpansions,
  });
  const reference = dijkstra(grid, start, goal);

  const out = {
    status: result.status,
    provenOptimal: result.provenOptimal,
    cost: result.cost === null ? null : result.cost.toString(),
    lowerBound: result.lowerBound === null ? null : result.lowerBound.toString(),
    path: result.path,
    expansions: result.expansions,
    reopens: result.reopens,
    exhausted: result.exhausted,
    interrupted: result.interrupted,
    firstGeneratedGoalCost:
      result.firstGeneratedGoalCost === null ? null : result.firstGeneratedGoalCost.toString(),
    firstGoalPopCost: result.firstGoalPopCost === null ? null : result.firstGoalPopCost.toString(),
    dijkstra: {
      cost: reference.cost === null ? null : reference.cost.toString(),
      expansions: reference.expansions,
    },
  };
  console.log(JSON.stringify(out, null, 2));
}

main();
