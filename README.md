# grid-path

Grid pathfinding with **exact non-negative rational costs** (BigInt fractions,
no floating-point drift) and a caller-supplied heuristic. Requires Node.js
>= 23.6 (runs TypeScript directly via type stripping; no build step).

## Features

- **Exact rational arithmetic** — costs like `1/3` or `0.1` are stored as
  reduced BigInt fractions (`src/rational.ts`), so zero-cost cycles and
  fractional weights never accumulate error.
- **Re-openable A\*** (`src/astar.ts`) — no permanent closed set: a node is
  re-expanded whenever a strictly better `g` arrives, so inconsistent (but
  admissible) heuristics still terminate with the optimum.
- **Optimality certification** — the caller declares whether the heuristic is
  admissible. If not, the result is reported as `unproven`: the returned path
  is a feasible upper bound, not a certified optimum. With `exhaustive: true`
  the search drains the open list, after which `g(goal)` is provably optimal
  regardless of the heuristic (label-correcting argument).
- **Bounds** — every result carries `upperBound` (cost of the best complete
  path found) and, when the heuristic is admissible, `lowerBound`
  (min `f` over the frontier, a certified lower bound on the optimum).
- **Budget interruption** — `budget` caps the number of expansions. The goal
  is recorded as a feasible upper bound the moment it is *generated*, so an
  interrupted search still returns the best complete path seen so far.
- **Goal accepted on pop, not generation** — the classic bug of stopping when
  the goal is first generated is available only as `stopOnGoalGeneration`
  for demonstrating counterexamples in tests.
- **Dijkstra reference** (`src/dijkstra.ts`) — used by the test suite to
  cross-check A* on zero-cost cycles, overestimating heuristics, and
  early-goal counterexamples, plus randomized fuzzing on rational-cost grids.

## Usage

```ts
import { Rational } from "./src/rational.ts";
import { GridMap } from "./src/graph.ts";
import { aStar } from "./src/astar.ts";

const grid = new GridMap(5, 5, {
  blocked: [[2, 2]],
  moveCost: (_from, to) => (to.x === 3 ? "1/3" : "1"), // non-negative rationals
});

const result = aStar({
  graph: grid,
  start: { x: 0, y: 0 },
  goal: { x: 4, y: 4 },
  heuristic: (c) => Rational.of(Math.abs(c.x - 4) + Math.abs(c.y - 4)).mul(Rational.of("1/3")),
  admissible: true,   // caller's claim; false => result may be "unproven"
  budget: 10000,      // optional expansion cap
});
// result: { status, path, upperBound, lowerBound, expansions }
```

### Status values

| status            | meaning                                                        |
|-------------------|----------------------------------------------------------------|
| `optimal`         | returned path is provably optimal                              |
| `unproven`        | feasible path, optimality not certified (inadmissible h)       |
| `budget-exceeded` | expansion budget hit; any path is a feasible upper bound only  |
| `unreachable`     | open list exhausted, goal never reached                        |

## CLI

```
node src/cli.ts examples/problem.json
```

See `examples/problem.json` for the input format (grid, blocked cells,
per-cell rational entry costs, heuristic type/scale, admissibility flag,
budget). Output is JSON with status, exact cost, lower bound, and path.

## Tests

```
npm test        # node --test test/*.test.ts
```

Covers: exact rational arithmetic, zero-cost cycles (graph and grid),
heuristic overestimation (flagged `unproven`, Dijkstra disagrees; exhaustive
mode recovers the optimum), generation-time vs pop-time goal acceptance,
closed-node reopening under an inconsistent admissible heuristic, budget
interruption bounds, unreachable goals, and seeded fuzz runs cross-checked
against Dijkstra.
