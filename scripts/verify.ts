// Brute-force cross-check of the exact DP planner on random small scenarios.
// Enumerates every set partition of the pick items and every location
// permutation per block, then compares the optimum with planPicking().
import { RoadGraph } from "../src/graph.ts";
import { planPicking } from "../src/planner.ts";
import type { Scenario } from "../src/types.ts";

function bruteForce(scenario: Scenario): number {
  const graph = new RoadGraph(scenario.graph);
  const lines = scenario.lines;
  const n = lines.length;
  const distCache = new Map<string, number>();
  const dist = (a: string, b: string): number => {
    const key = a + "|" + b;
    if (!distCache.has(key)) distCache.set(key, graph.distance(a, b));
    return distCache.get(key)!;
  };
  // Optimal tour cost for a set of locations via permutation enumeration.
  const tourCache = new Map<string, number>();
  const tourCost = (locSet: string[]): number => {
    const key = [...locSet].sort().join("|");
    if (tourCache.has(key)) return tourCache.get(key)!;
    let best = Infinity;
    const perm = [...locSet];
    const visit = (k: number): void => {
      if (k === perm.length) {
        let cost = dist(scenario.depot, perm[0]);
        for (let i = 0; i + 1 < perm.length; i++) {
          cost += dist(perm[i], perm[i + 1]);
        }
        cost += dist(perm[perm.length - 1], scenario.depot);
        if (cost < best) best = cost;
        return;
      }
      for (let i = k; i < perm.length; i++) {
        const tmp = perm[k]; perm[k] = perm[i]; perm[i] = tmp;
        visit(k + 1);
        const tmp2 = perm[k]; perm[k] = perm[i]; perm[i] = tmp2;
      }
    };
    visit(0);
    tourCache.set(key, best);
    return best;
  };
  // Recursive partition enumeration; block cost = tour + splitCost * orders.
  let best = Infinity;
  const blocks: number[][] = [];
  const search = (i: number): void => {
    if (i === n) {
      let total = 0;
      for (const block of blocks) {
        const locSet = [...new Set(block.map((li) => lines[li].location))];
        const orders = new Set(block.map((li) => lines[li].order));
        total += tourCost(locSet) + scenario.splitCost * orders.size;
      }
      total -= scenario.splitCost * new Set(lines.map((l) => l.order)).size;
      if (total < best) best = total;
      return;
    }
    const qty = lines[i].qty;
    for (const block of blocks) {
      const load = block.reduce((acc, li) => acc + lines[li].qty, 0);
      if (load + qty <= scenario.capacity) {
        block.push(i);
        search(i + 1);
        block.pop();
      }
    }
    if (qty <= scenario.capacity) {
      blocks.push([i]);
      search(i + 1);
      blocks.pop();
    }
  };
  search(0);
  return best;
}

function randomScenario(rng: () => number, id: number): Scenario {
  const locations = ["P", "Q", "R", "S", "T"].slice(0, 2 + Math.floor(rng() * 4));
  const nodes = ["D", ...locations];
  const edges = [];
  // Random tree over nodes to guarantee connectivity, plus extra chords.
  for (let i = 1; i < nodes.length; i++) {
    const to = nodes[Math.floor(rng() * i)];
    edges.push({ from: nodes[i], to, dist: 1 + Math.floor(rng() * 4) });
  }
  if (nodes.length > 3) {
    edges.push({
      from: nodes[1],
      to: nodes[nodes.length - 1],
      dist: 1 + Math.floor(rng() * 3),
    });
  }
  const numLines = 3 + Math.floor(rng() * 6); // 3..8 lines
  const lines = [];
  for (let i = 0; i < numLines; i++) {
    lines.push({
      order: "W" + id + "_" + (1 + Math.floor(rng() * 4)),
      location: locations[Math.floor(rng() * locations.length)],
      qty: 1 + Math.floor(rng() * 3),
    });
  }
  return {
    depot: "D",
    capacity: 4 + Math.floor(rng() * 4),
    splitCost: Math.floor(rng() * 15),
    graph: { nodes, edges },
    lines,
  };
}

// Simple deterministic PRNG (mulberry32).
let seed = 20261004;
const rng = (): number => {
  seed |= 0;
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};

let failures = 0;
for (let id = 0; id < 300; id++) {
  const scenario = randomScenario(rng, id);
  if (scenario.lines.length === 0) continue;
  const expected = bruteForce(scenario);
  const actual = planPicking(scenario).totalCost;
  if (Math.abs(expected - actual) > 1e-9) {
    failures++;
    console.error(
      "MISMATCH #" + id + ": dp=" + actual + " brute=" + expected,
      JSON.stringify(scenario),
    );
  }
}
console.log(failures === 0 ? "OK: all 300 random cases match" : failures + " FAILURES");
process.exit(failures === 0 ? 0 : 1);
