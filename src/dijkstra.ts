import { Rational } from "./rational.ts";
import type { Graph } from "./graph.ts";

export interface DijkstraResult<N> {
  cost: Rational | null; // null when the goal is unreachable
  path: N[] | null;
  expansions: number;
}

/**
 * Reference Dijkstra (label-setting with decrease-key via lazy deletion).
 * Used as ground truth to cross-check the A* implementation.
 */
export function dijkstra<N>(
  graph: Graph<N>,
  start: N,
  goal: N,
): DijkstraResult<N> {
  const goalKey = graph.key(goal);
  const dist = new Map<string, Rational>();
  const nodeByKey = new Map<string, N>();
  const parent = new Map<string, string>();
  // Simple array-based priority queue; correctness matters, not speed.
  const queue: Array<{ g: Rational; key: string; node: N }> = [];

  const startKey = graph.key(start);
  dist.set(startKey, Rational.ZERO);
  nodeByKey.set(startKey, start);
  queue.push({ g: Rational.ZERO, key: startKey, node: start });

  let expansions = 0;

  while (queue.length > 0) {
    let best = 0;
    for (let i = 1; i < queue.length; i++) {
      if (queue[i].g.lt(queue[best].g)) best = i;
    }
    const { g, key, node } = queue.splice(best, 1)[0];
    const known = dist.get(key);
    if (known === undefined || g.gt(known)) continue; // stale
    expansions++;
    if (key === goalKey) {
      const chain: N[] = [];
      let k: string | undefined = key;
      while (k !== undefined) {
        chain.push(nodeByKey.get(k)!);
        k = parent.get(k);
      }
      chain.reverse();
      return { cost: g, path: chain, expansions };
    }
    for (const { to, cost } of graph.neighbors(node)) {
      const g2 = g.add(cost);
      const toKey = graph.key(to);
      const old = dist.get(toKey);
      if (old !== undefined && !g2.lt(old)) continue;
      dist.set(toKey, g2);
      nodeByKey.set(toKey, to);
      parent.set(toKey, key);
      queue.push({ g: g2, key: toKey, node: to });
    }
  }
  return { cost: null, path: null, expansions };
}
