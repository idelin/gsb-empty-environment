import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational } from "../src/rational.ts";
import { GridMap, type Cell, type Graph } from "../src/graph.ts";
import { aStar } from "../src/astar.ts";
import { dijkstra } from "../src/dijkstra.ts";

const R = (s: string | number): Rational => Rational.of(s);

/** Explicit adjacency-list graph for hand-built counterexamples. */
function mapGraph(edges: Record<string, Array<[string, string]>>): Graph<string> {
  return {
    key: (n) => n,
    *neighbors(n: string) {
      for (const [to, c] of edges[n] ?? []) yield { to, cost: R(c) };
    },
  };
}

test("rational arithmetic is exact and normalized", () => {
  assert.equal(R("1/3").add(R("1/6")).toString(), "1/2");
  assert.equal(R("0.1").add(R("0.2")).toString(), "3/10");
  assert.equal(R("2/4").toString(), "1/2");
  assert.equal(R("-3/-9").toString(), "1/3");
  assert.equal(R("1/3").mul(R("3")).toString(), "1");
  assert.ok(R("1/3").gt(R("1/4")));
  assert.ok(R("0").isZero());
  assert.throws(() => R("1/0"));
});

test("zero-cost cycles: A* terminates and matches Dijkstra", () => {
  // B <-> C is a zero-cost cycle; cheapest path S->B->C->G costs 1/2.
  const graph = mapGraph({
    S: [["A", "0"], ["B", "0"]],
    A: [["G", "5"]],
    B: [["C", "0"]],
    C: [["B", "0"], ["G", "1/2"]],
    G: [],
  });
  const ref = dijkstra(graph, "S", "G");
  assert.equal(ref.cost!.toString(), "1/2");

  const res = aStar<string>({
    graph, start: "S", goal: "G",
    heuristic: () => Rational.ZERO,
    admissible: true,
  });
  assert.equal(res.status, "optimal");
  assert.equal(res.upperBound!.toString(), ref.cost!.toString());
  assert.equal(res.lowerBound!.toString(), "1/2");
  assert.deepEqual(res.path, ["S", "B", "C", "G"]);
});

test("zero-cost cycle on a grid with zero enter-cost cells", () => {
  // 3x1 corridor, middle cell free to enter (0 cost both ways across it).
  const grid = new GridMap(3, 1, {
    moveCost: (_f, t) => (t.x === 1 ? "0" : "1/3"),
  });
  const start: Cell = { x: 0, y: 0 };
  const goal: Cell = { x: 2, y: 0 };
  const ref = dijkstra(grid, start, goal);
  const res = aStar<Cell>({
    graph: grid, start, goal,
    heuristic: () => Rational.ZERO,
    admissible: true,
  });
  assert.equal(res.status, "optimal");
  assert.equal(res.upperBound!.toString(), ref.cost!.toString()); // 0 + 1/3
  assert.equal(res.upperBound!.toString(), "1/3");
});

test("overestimating heuristic: result flagged unproven, Dijkstra disagrees", () => {
  // Optimal is S->B->G = 3, but h(B)=100 scares A* into S->A->G = 5.
  const graph = mapGraph({
    S: [["A", "1"], ["B", "2"]],
    A: [["G", "4"]],
    B: [["G", "1"]],
    G: [],
  });
  const h = (n: string): Rational =>
    n === "B" ? R("100") : Rational.ZERO;

  const ref = dijkstra(graph, "S", "G");
  assert.equal(ref.cost!.toString(), "3");

  const res = aStar<string>({
    graph, start: "S", goal: "G", heuristic: h, admissible: false,
  });
  assert.equal(res.status, "unproven");
  assert.equal(res.upperBound!.toString(), "5"); // feasible, but suboptimal
  assert.ok(res.upperBound!.gt(ref.cost!));
  assert.equal(res.lowerBound, null); // no certificate without admissibility

  // Same heuristic, exhaustive mode: open list drains, g(goal) provably optimal.
  const full = aStar<string>({
    graph, start: "S", goal: "G", heuristic: h,
    admissible: false, exhaustive: true,
  });
  assert.equal(full.status, "optimal");
  assert.equal(full.upperBound!.toString(), "3");
  assert.deepEqual(full.path, ["S", "B", "G"]);
});

test("early goal acceptance (generation-time) is a counterexample vs pop-time", () => {
  // Goal is generated with g=10 from S, but S->A->G costs 2.
  const graph = mapGraph({
    S: [["A", "1"], ["G", "10"]],
    A: [["G", "1"]],
    G: [],
  });
  const ref = dijkstra(graph, "S", "G");
  assert.equal(ref.cost!.toString(), "2");

  const naive = aStar<string>({
    graph, start: "S", goal: "G",
    heuristic: () => Rational.ZERO,
    admissible: true,
    stopOnGoalGeneration: true, // the classic bug, for demonstration
  });
  assert.equal(naive.upperBound!.toString(), "10"); // suboptimal

  const proper = aStar<string>({
    graph, start: "S", goal: "G",
    heuristic: () => Rational.ZERO,
    admissible: true,
  });
  assert.equal(proper.status, "optimal");
  assert.equal(proper.upperBound!.toString(), ref.cost!.toString());
  assert.deepEqual(proper.path, ["S", "A", "G"]);
});

test("inconsistent-but-admissible heuristic reopens closed nodes", () => {
  // h is admissible but inconsistent: h(A)=4 > cost(A,B)+h(B)=1+0.
  // f(B)=4 < f(A)=5, so B is popped (closed) with the suboptimal g=4 via
  // S->B; expanding A then improves g(B) to 2 and B must be reopened.
  const graph = mapGraph({
    S: [["A", "1"], ["B", "4"]],
    A: [["B", "1"]],
    B: [["G", "5"]],
    G: [],
  });
  const h = (n: string): Rational => {
    switch (n) {
      case "A": return R("4"); // <= true A->G = 6, but inconsistent
      default: return Rational.ZERO;
    }
  };
  const res = aStar<string>({
    graph, start: "S", goal: "G", heuristic: h, admissible: true,
  });
  const ref = dijkstra(graph, "S", "G");
  assert.equal(res.status, "optimal");
  assert.equal(res.upperBound!.toString(), ref.cost!.toString());
  assert.equal(res.upperBound!.toString(), "7");
  // S, B (closed with g=4), A, B again (reopened with g=2), G.
  assert.equal(res.expansions, 5);
  assert.deepEqual(res.path, ["S", "A", "B", "G"]);
});

test("budget interruption keeps feasible upper bound and certified lower bound", () => {
  // 1x6 corridor, unit costs, admissible manhattan heuristic.
  const grid = new GridMap(6, 1);
  const start: Cell = { x: 0, y: 0 };
  const goal: Cell = { x: 5, y: 0 };
  const h = (c: Cell): Rational => R(Math.abs(c.x - 5));

  // Budget 2: cannot reach the goal; no path yet, but lower bound is valid.
  const tight = aStar<Cell>({
    graph: grid, start, goal, heuristic: h, admissible: true, budget: 2,
  });
  assert.equal(tight.status, "budget-exceeded");
  assert.equal(tight.path, null);
  assert.equal(tight.upperBound, null);
  assert.ok(tight.lowerBound !== null && tight.lowerBound.le(R("5")));

  // Budget 5: goal gets generated (feasible upper bound) before budget ends.
  const mid = aStar<Cell>({
    graph: grid, start, goal, heuristic: h, admissible: true, budget: 5,
  });
  assert.equal(mid.status, "budget-exceeded");
  assert.ok(mid.upperBound !== null && mid.upperBound.ge(R("5")));
  assert.ok(mid.path !== null);

  // Full budget: optimal.
  const full = aStar<Cell>({
    graph: grid, start, goal, heuristic: h, admissible: true,
  });
  assert.equal(full.status, "optimal");
  assert.equal(full.upperBound!.toString(), "5");
  assert.equal(full.lowerBound!.toString(), "5");
});

test("unreachable goal is reported", () => {
  const grid = new GridMap(3, 1, { blocked: [[1, 0]] });
  const res = aStar<Cell>({
    graph: grid, start: { x: 0, y: 0 }, goal: { x: 2, y: 0 },
    heuristic: () => Rational.ZERO, admissible: true,
  });
  assert.equal(res.status, "unreachable");
  assert.equal(res.path, null);
});

// Deterministic PRNG for reproducible fuzzing.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("fuzz: A* (admissible) matches Dijkstra on random rational-cost grids", () => {
  const rand = mulberry32(20261005);
  for (let trial = 0; trial < 200; trial++) {
    const w = 2 + Math.floor(rand() * 6);
    const hgt = 2 + Math.floor(rand() * 6);
    const blocked: Array<[number, number]> = [];
    const enter = new Map<string, string>();
    for (let x = 0; x < w; x++) {
      for (let y = 0; y < hgt; y++) {
        if (rand() < 0.2) blocked.push([x, y]);
        // Rational costs, sometimes zero (zero-cost moves/cycles).
        const num = rand() < 0.25 ? 0 : 1 + Math.floor(rand() * 5);
        const den = 1 + Math.floor(rand() * 4);
        enter.set(`${x},${y}`, `${num}/${den}`);
      }
    }
    const start: Cell = { x: 0, y: 0 };
    const goal: Cell = { x: w - 1, y: hgt - 1 };
    const grid = new GridMap(w, hgt, {
      blocked: blocked.filter(([x, y]) =>
        !(x === start.x && y === start.y) && !(x === goal.x && y === goal.y)),
      moveCost: (_f, t) => enter.get(`${t.x},${t.y}`)!,
    });

    // Admissible heuristic: min cell cost x manhattan distance.
    let minCost = Rational.ONE;
    for (const v of enter.values()) minCost = Rational.min(minCost, R(v));
    const h = (c: Cell): Rational =>
      R(Math.abs(c.x - goal.x) + Math.abs(c.y - goal.y)).mul(minCost);

    const ref = dijkstra(grid, start, goal);
    const res = aStar<Cell>({
      graph: grid, start, goal, heuristic: h, admissible: true,
    });

    if (ref.cost === null) {
      assert.equal(res.status, "unreachable", `trial ${trial}`);
    } else {
      assert.equal(res.status, "optimal", `trial ${trial}`);
      assert.equal(res.upperBound!.toString(), ref.cost.toString(), `trial ${trial}`);
      assert.equal(res.lowerBound!.toString(), ref.cost.toString(), `trial ${trial}`);
      // Path cost sanity check.
      let total = Rational.ZERO;
      for (let i = 1; i < res.path!.length; i++) {
        total = total.add(R(enter.get(`${res.path![i].x},${res.path![i].y}`)!));
      }
      assert.equal(total.toString(), ref.cost.toString(), `trial ${trial}`);
    }
  }
});

test("fuzz: inadmissible heuristic + exhaustive search still matches Dijkstra", () => {
  const rand = mulberry32(1234567);
  for (let trial = 0; trial < 100; trial++) {
    const w = 2 + Math.floor(rand() * 5);
    const hgt = 2 + Math.floor(rand() * 5);
    const enter = new Map<string, string>();
    for (let x = 0; x < w; x++) {
      for (let y = 0; y < hgt; y++) {
        enter.set(`${x},${y}`, `${1 + Math.floor(rand() * 4)}/${1 + Math.floor(rand() * 3)}`);
      }
    }
    const start: Cell = { x: 0, y: 0 };
    const goal: Cell = { x: w - 1, y: hgt - 1 };
    const grid = new GridMap(w, hgt, {
      moveCost: (_f, t) => enter.get(`${t.x},${t.y}`)!,
    });
    // Wildly overestimating heuristic: not admissible.
    const h = (c: Cell): Rational =>
      R((Math.abs(c.x - goal.x) + Math.abs(c.y - goal.y)) * 37);

    const ref = dijkstra(grid, start, goal);
    const res = aStar<Cell>({
      graph: grid, start, goal, heuristic: h,
      admissible: false, exhaustive: true,
    });
    assert.equal(res.status, "optimal", `trial ${trial}`);
    assert.equal(res.upperBound!.toString(), ref.cost!.toString(), `trial ${trial}`);
  }
});
