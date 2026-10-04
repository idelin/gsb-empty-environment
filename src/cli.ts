#!/usr/bin/env node
/**
 * CLI: node src/cli.ts problem.json
 *
 * Problem JSON:
 * {
 *   "width": 5, "height": 5, "diagonal": false,
 *   "blocked": [[1, 1], "2,2"],
 *   "enterCost": { "3,0": "1/3" },        // per-cell entry cost, default "1"
 *   "start": [0, 0], "goal": [4, 4],
 *   "heuristic": { "type": "manhattan" | "zero", "scale": "1/2" },
 *   "admissible": true,
 *   "budget": 10000,                       // optional
 *   "exhaustive": false                    // optional
 * }
 */
import { readFileSync } from "node:fs";
import { Rational } from "./rational.ts";
import { GridMap, type Cell } from "./graph.ts";
import { aStar } from "./astar.ts";

interface ProblemFile {
  width: number;
  height: number;
  diagonal?: boolean;
  blocked?: Array<[number, number] | string>;
  enterCost?: Record<string, number | string>;
  start: [number, number];
  goal: [number, number];
  heuristic?: { type?: string; scale?: number | string };
  admissible?: boolean;
  budget?: number;
  exhaustive?: boolean;
}

function main(): void {
  const file = process.argv[2];
  if (!file) {
    console.error("usage: node src/cli.ts problem.json");
    process.exit(2);
  }
  const problem = JSON.parse(readFileSync(file, "utf8")) as ProblemFile;

  const enterCost = new Map<string, Rational>();
  for (const [k, v] of Object.entries(problem.enterCost ?? {})) {
    enterCost.set(k, Rational.of(v));
  }

  const grid = new GridMap(problem.width, problem.height, {
    diagonal: problem.diagonal,
    blocked: problem.blocked,
    moveCost: (_from, to) => enterCost.get(`${to.x},${to.y}`) ?? Rational.ONE,
  });

  const scale = Rational.of(problem.heuristic?.scale ?? 1);
  const goal: Cell = { x: problem.goal[0], y: problem.goal[1] };
  const hType = problem.heuristic?.type ?? "manhattan";
  const heuristic = (c: Cell): Rational => {
    if (hType === "zero") return Rational.ZERO;
    const d = Math.abs(c.x - goal.x) + Math.abs(c.y - goal.y);
    return Rational.of(d).mul(scale);
  };

  const result = aStar<Cell>({
    graph: grid,
    start: { x: problem.start[0], y: problem.start[1] },
    goal,
    heuristic,
    admissible: problem.admissible ?? true,
    budget: problem.budget,
    exhaustive: problem.exhaustive,
  });

  console.log(JSON.stringify({
    status: result.status,
    cost: result.upperBound?.toString() ?? null,
    lowerBound: result.lowerBound?.toString() ?? null,
    expansions: result.expansions,
    path: result.path?.map((c) => [c.x, c.y]) ?? null,
  }, null, 2));
}

main();
