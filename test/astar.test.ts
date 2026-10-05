import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Fraction } from '../src/fraction.ts';
import { Grid } from '../src/grid.ts';
import type { Cell } from '../src/grid.ts';
import { astar } from '../src/astar.ts';
import { dijkstra } from '../src/dijkstra.ts';
import { admissibleManhattan, manhattanHeuristic, zeroHeuristic } from '../src/heuristics.ts';

function pathCost(grid: Grid, path: Cell[]): Fraction {
  let total = Fraction.ZERO;
  for (let i = 1; i < path.length; i++) {
    const c = grid.costAt(path[i].x, path[i].y);
    assert.ok(c !== null, 'path must not cross blocked cells');
    total = total.add(c);
    const dx = Math.abs(path[i].x - path[i - 1].x);
    const dy = Math.abs(path[i].y - path[i - 1].y);
    assert.equal(dx + dy, 1, 'path steps must be 4-connected');
  }
  return total;
}

test('Fraction: exact rational arithmetic', () => {
  assert.equal(Fraction.of('1/3').add('1/6').toString(), '1/2');
  assert.ok(Fraction.of('0.5').equals(Fraction.of('1/2')));
  assert.equal(Fraction.of('1e-3').toString(), '1/1000');
  assert.equal(Fraction.of(2).mul('3/4').toString(), '3/2');
  assert.ok(Fraction.of('2/4').equals(Fraction.of('1/2')));
  assert.ok(Fraction.of('1/3').lt(Fraction.of('1/2')));
  assert.throws(() => Fraction.of('1/0'));
});

test('Grid: rejects negative costs', () => {
  assert.throws(() => new Grid([[-1]]));
  assert.throws(() => new Grid([[1, 2], [3]]));
});

test('rational costs stay exact: 1/3 + 1/6 = 1/2', () => {
  const grid = new Grid([
    [1, '1/3'],
    ['1/2', '1/6'],
  ]);
  const start = { x: 0, y: 0 };
  const goal = { x: 1, y: 1 };
  const result = astar(grid, start, goal, { heuristic: zeroHeuristic() });
  assert.ok(result.provenOptimal);
  assert.equal(result.cost!.toString(), '1/2');
  assert.deepEqual(result.path, [start, { x: 1, y: 0 }, goal]);
  const ref = dijkstra(grid, start, goal);
  assert.ok(result.cost!.equals(ref.cost!));
});

test('zero-cost cycles terminate and stay optimal', () => {
  const grid = new Grid([
    [1, 0, 0],
    [0, 0, 1],
    [0, 1, '1/2'],
  ]);
  const start = { x: 0, y: 0 };
  const goal = { x: 2, y: 2 };
  const result = astar(grid, start, goal, { heuristic: zeroHeuristic() });
  assert.ok(result.provenOptimal);
  assert.equal(result.cost!.toString(), '3/2');
  const ref = dijkstra(grid, start, goal);
  assert.ok(result.cost!.equals(ref.cost!));
  assert.ok(pathCost(grid, result.path!).equals(result.cost!));

  // Pure zero-cost ring: everything is free, must still terminate.
  const free = new Grid([
    [0, 0, 0],
    [0, 0, 0],
    [0, 0, 0],
  ]);
  const r2 = astar(free, start, goal, { heuristic: zeroHeuristic() });
  assert.ok(r2.provenOptimal);
  assert.ok(r2.cost!.isZero());
});

test('counterexample: overestimating heuristic reopens closed nodes', () => {
  // Layout (enter costs):        Heuristic: h(A)=10 overestimates (true 2),
  //   y=0:  S(0,0) A(1,0) B(2,0)  everything else is exact Manhattan.
  //   y=1:  C(0,1) D(1,1) G(2,1)  C costs 5, so the greedy run reaches G
  //   costs: row0 all 1, C=5      via C-D-G with cost 7 before the cheap
  //                               route S-A-B-G (cost 3) is discovered.
  const grid = new Grid([
    [1, 1, 1],
    [5, 1, 1],
  ]);
  const start = { x: 0, y: 0 };
  const goal = { x: 2, y: 1 };
  const heuristic = {
    h: (cell: Cell, g: Cell): number =>
      cell.x === 1 && cell.y === 0 ? 10 : Math.abs(cell.x - g.x) + Math.abs(cell.y - g.y),
    admissible: false,
  };
  const result = astar(grid, start, goal, { heuristic });

  // A naive greedy search would stop at the first goal pop with cost 7.
  assert.equal(result.firstGoalPopCost!.toString(), '7');
  assert.equal(result.firstGeneratedGoalCost!.toString(), '7');
  // Continuing past it finds the true optimum via the reopened node B.
  assert.equal(result.cost!.toString(), '3');
  assert.ok(result.reopens >= 1, 'closed node B must be reopened');
  assert.deepEqual(result.path, [start, { x: 1, y: 0 }, { x: 2, y: 0 }, goal]);
  // Heuristic not admissible => never marked proven, even when exhaustive.
  assert.equal(result.provenOptimal, false);
  assert.equal(result.exhausted, true);
  assert.equal(result.status, 'feasible');
  assert.ok(result.lowerBound!.equals(result.cost!));
  const ref = dijkstra(grid, start, goal);
  assert.ok(result.cost!.equals(ref.cost!));
});

test('counterexample: goal generated early with suboptimal cost', () => {
  // Admissible h, but h(N2=(2,2))=5 is tight while h(N1=(2,0))=0 is loose,
  // so N1 expands first and generates the goal with cost 8; the optimum 6
  // goes through N2. Stopping at first generation would report 8.
  const grid = new Grid([
    [1, 1, 2, 9],
    [0, 9, 5, 9],
    [0, 0, 1, 9],
  ]);
  const start = { x: 0, y: 0 };
  const goal = { x: 2, y: 1 };
  const heuristic = {
    h: (cell: Cell): number => (cell.x === 2 && cell.y === 2 ? 5 : 0),
    admissible: true,
  };
  const result = astar(grid, start, goal, { heuristic });
  assert.equal(result.firstGeneratedGoalCost!.toString(), '8');
  assert.equal(result.cost!.toString(), '6');
  assert.ok(result.provenOptimal);
  assert.deepEqual(result.path, [
    start,
    { x: 0, y: 1 },
    { x: 0, y: 2 },
    { x: 1, y: 2 },
    { x: 2, y: 2 },
    goal,
  ]);
  const ref = dijkstra(grid, start, goal);
  assert.ok(result.cost!.equals(ref.cost!));
});

test('budget interruption keeps a feasible upper bound and a lower bound', () => {
  const grid = new Grid([
    [1, 1, 1],
    [1, 1, 1],
    [1, 1, 1],
  ]);
  const start = { x: 0, y: 0 };
  const goal = { x: 2, y: 2 };
  const heuristic = manhattanHeuristic(1, true);

  // Tiny budget: goal not even generated yet, but the lower bound holds.
  const r1 = astar(grid, start, goal, { heuristic, maxExpansions: 5 });
  assert.equal(r1.interrupted, true);
  assert.equal(r1.cost, null);
  assert.equal(r1.provenOptimal, false);
  assert.ok(r1.lowerBound !== null && r1.lowerBound.lte(Fraction.of(4)));

  // Larger budget: goal generated (cost 4) but open list not drained.
  const r2 = astar(grid, start, goal, { heuristic, maxExpansions: 7 });
  assert.equal(r2.interrupted, true);
  assert.equal(r2.cost!.toString(), '4');
  assert.equal(r2.provenOptimal, false);
  assert.ok(r2.lowerBound!.lte(r2.cost!));
  assert.ok(pathCost(grid, r2.path!).equals(r2.cost!));

  // Full budget proves optimality.
  const r3 = astar(grid, start, goal, { heuristic });
  assert.ok(r3.provenOptimal);
  assert.equal(r3.cost!.toString(), '4');
  assert.ok(r3.lowerBound!.equals(r3.cost!));
});

test('infeasible when goal is walled off', () => {
  const grid = new Grid([
    [1, null, 1],
    [null, 1, null],
    [1, null, 1],
  ]);
  const result = astar(grid, { x: 0, y: 0 }, { x: 1, y: 1 }, { heuristic: zeroHeuristic() });
  assert.equal(result.status, 'infeasible');
  assert.equal(result.cost, null);
  assert.equal(result.path, null);
  assert.equal(result.exhausted, true);
});

test('start equals goal costs zero', () => {
  const grid = new Grid([[5]]);
  const result = astar(grid, { x: 0, y: 0 }, { x: 0, y: 0 });
  assert.ok(result.provenOptimal);
  assert.ok(result.cost!.isZero());
  assert.deepEqual(result.path, [{ x: 0, y: 0 }]);
});

function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 2 ** 32;
  };
}

function randomGrid(rand: () => number): Grid {
  const pool: (number | string)[] = [0, '1/3', '1/2', 1, '3/2', 2];
  const rows: (number | string | null)[][] = [];
  for (let y = 0; y < 6; y++) {
    const row: (number | string | null)[] = [];
    for (let x = 0; x < 6; x++) {
      row.push(rand() < 0.15 ? null : pool[Math.floor(rand() * pool.length)]);
    }
    rows.push(row);
  }
  rows[0][0] = 1;
  rows[5][5] = 1;
  return new Grid(rows);
}

test('random grids: A* with admissible heuristic matches Dijkstra', () => {
  const rand = lcg(12345);
  for (let trial = 0; trial < 40; trial++) {
    const grid = randomGrid(rand);
    const start = { x: 0, y: 0 };
    const goal = { x: 5, y: 5 };
    const result = astar(grid, start, goal, { heuristic: admissibleManhattan(grid) });
    const ref = dijkstra(grid, start, goal);
    if (ref.cost === null) {
      assert.equal(result.cost, null, 'trial ' + trial);
      assert.equal(result.status, 'infeasible');
      continue;
    }
    assert.ok(result.cost !== null && result.cost.equals(ref.cost), 'trial ' + trial);
    assert.ok(result.provenOptimal, 'trial ' + trial);
    assert.ok(result.lowerBound!.equals(result.cost!), 'trial ' + trial);
    assert.ok(pathCost(grid, result.path!).equals(result.cost!), 'trial ' + trial);
  }
});

test('random grids: overestimating heuristic stays exhaustive and correct', () => {
  const rand = lcg(999);
  for (let trial = 0; trial < 40; trial++) {
    const grid = randomGrid(rand);
    const start = { x: 0, y: 0 };
    const goal = { x: 5, y: 5 };
    const inflated = grid.minCost().mul(3);
    const result = astar(grid, start, goal, {
      heuristic: manhattanHeuristic(inflated, false),
    });
    const ref = dijkstra(grid, start, goal);
    if (ref.cost === null) {
      assert.equal(result.cost, null, 'trial ' + trial);
      continue;
    }
    assert.ok(result.cost !== null && result.cost.equals(ref.cost), 'trial ' + trial);
    assert.equal(result.provenOptimal, false, 'trial ' + trial);
    assert.equal(result.exhausted, true, 'trial ' + trial);
    assert.ok(pathCost(grid, result.path!).equals(result.cost!), 'trial ' + trial);
  }
});
