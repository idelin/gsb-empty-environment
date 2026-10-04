import { Rational } from "./rational.ts";
import type { Graph } from "./graph.ts";

export type SearchStatus =
  | "optimal"         // provably optimal path returned
  | "unproven"        // feasible path returned, optimality not certified
  | "budget-exceeded" // expansion budget ran out; any returned path is a feasible upper bound
  | "unreachable";    // open list exhausted without ever reaching the goal

export interface AStarOptions<N> {
  graph: Graph<N>;
  start: N;
  goal: N;
  /** Caller-supplied heuristic; must return a non-negative rational. */
  heuristic: (node: N) => Rational | number | string;
  /**
   * Caller asserts the heuristic is admissible (never overestimates the true
   * remaining cost). When false, popping the goal does NOT prove optimality
   * and the result is reported as "unproven".
   */
  admissible: boolean;
  /** Maximum number of node expansions before the search is interrupted. */
  budget?: number;
  /**
   * With an inadmissible heuristic, keep searching after the first goal pop
   * until the open list is empty. The final g(goal) is then provably optimal
   * (label-correcting argument, independent of h), so the status is upgraded
   * to "optimal". Default: false (stop at first goal pop, report "unproven").
   */
  exhaustive?: boolean;
  /**
   * Demonstration/testing only: reproduce the classic bug of accepting the
   * goal the moment it is *generated* instead of when it is *popped*.
   */
  stopOnGoalGeneration?: boolean;
}

export interface AStarResult<N> {
  status: SearchStatus;
  /** Best complete path found (null if none yet). */
  path: N[] | null;
  /** Cost of `path`; a feasible upper bound on the optimum. */
  upperBound: Rational | null;
  /**
   * Certified lower bound on the optimal cost, when one is available
   * (admissible heuristic: min f over the frontier at termination).
   * Null when the heuristic cannot certify a bound.
   */
  lowerBound: Rational | null;
  expansions: number;
}

interface HeapEntry<N> {
  f: Rational;
  g: Rational;
  seq: number;
  node: N;
}

/** Binary min-heap ordered by f, then by insertion order. */
class MinHeap<N> {
  private items: Array<HeapEntry<N>> = [];

  get size(): number {
    return this.items.length;
  }

  peek(): HeapEntry<N> | undefined {
    return this.items[0];
  }

  private static less<N>(a: HeapEntry<N>, b: HeapEntry<N>): boolean {
    const c = a.f.cmp(b.f);
    if (c !== 0) return c < 0;
    return a.seq < b.seq;
  }

  push(entry: HeapEntry<N>): void {
    const items = this.items;
    items.push(entry);
    let i = items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (!MinHeap.less(items[i], items[parent])) break;
      [items[i], items[parent]] = [items[parent], items[i]];
      i = parent;
    }
  }

  pop(): HeapEntry<N> | undefined {
    const items = this.items;
    if (items.length === 0) return undefined;
    const top = items[0];
    const last = items.pop()!;
    if (items.length > 0) {
      items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let smallest = i;
        if (l < items.length && MinHeap.less(items[l], items[smallest])) smallest = l;
        if (r < items.length && MinHeap.less(items[r], items[smallest])) smallest = r;
        if (smallest === i) break;
        [items[i], items[smallest]] = [items[smallest], items[i]];
        i = smallest;
      }
    }
    return top;
  }
}

export function aStar<N>(options: AStarOptions<N>): AStarResult<N> {
  const { graph, start, goal, admissible } = options;
  const budget = options.budget ?? Number.POSITIVE_INFINITY;
  const exhaustive = options.exhaustive ?? false;

  const h = (node: N): Rational => {
    const v = Rational.of(options.heuristic(node));
    if (v.isNegative()) throw new Error("heuristic must be non-negative");
    return v;
  };

  const startKey = graph.key(start);
  const goalKey = graph.key(goal);

  const gScore = new Map<string, Rational>();
  const nodeByKey = new Map<string, N>();
  const parent = new Map<string, string>();
  const heap = new MinHeap<N>();
  let seq = 0;

  gScore.set(startKey, Rational.ZERO);
  nodeByKey.set(startKey, start);
  heap.push({ f: h(start), g: Rational.ZERO, seq: seq++, node: start });

  let bestGoalG: Rational | null = null;
  let expansions = 0;
  let budgetHit = false;
  let provenOptimal = false;

  const recordGoal = (g: Rational): void => {
    if (bestGoalG === null || g.lt(bestGoalG)) bestGoalG = g;
  };

  if (startKey === goalKey) {
    recordGoal(Rational.ZERO);
    provenOptimal = true; // zero is optimal: all edge costs are non-negative
  }

  const relax = (fromKey: string, g: Rational, to: N, cost: Rational): void => {
    const g2 = g.add(cost);
    const toKey = graph.key(to);
    const old = gScore.get(toKey);
    if (old !== undefined && !g2.lt(old)) return; // only strictly better g reopens
    gScore.set(toKey, g2);
    nodeByKey.set(toKey, to);
    parent.set(toKey, fromKey);
    if (toKey === goalKey) {
      // A generated goal is already a complete path: keep it as a feasible
      // upper bound so budget interruption never loses it.
      recordGoal(g2);
      if (options.stopOnGoalGeneration) return;
    }
    heap.push({ f: g2.add(h(to)), g: g2, seq: seq++, node: to });
  };

  while (!provenOptimal && heap.size > 0) {
    if (options.stopOnGoalGeneration && bestGoalG !== null) break;
    const entry = heap.pop()!;
    const key = graph.key(entry.node);
    const current = gScore.get(key);
    // Stale entry: a strictly better g was recorded after this entry was pushed.
    if (current === undefined || entry.g.gt(current)) continue;

    if (expansions >= budget) {
      budgetHit = true;
      heap.push(entry); // put it back so the frontier bound stays valid
      break;
    }
    expansions++;

    if (key === goalKey) {
      recordGoal(entry.g);
      if (admissible) {
        provenOptimal = true; // f(goal) = g(goal) <= f of everything still open
        break;
      }
      if (!exhaustive) break; // feasible upper bound only; optimality unproven
      continue;               // keep going: exhaustion will certify the optimum
    }

    for (const { to, cost } of graph.neighbors(entry.node)) {
      relax(key, entry.g, to, cost);
    }
  }

  // Certified lower bound: with an admissible heuristic some frontier node
  // always lies on an optimal path, so min f over the open list bounds the
  // optimum from below. Without admissibility no certificate exists.
  let lowerBound: Rational | null = null;
  if (admissible) {
    if (provenOptimal && bestGoalG !== null) {
      lowerBound = bestGoalG;
    } else {
      const top = heap.peek();
      if (top) lowerBound = top.f;
    }
  }

  let path: N[] | null = null;
  if (bestGoalG !== null) {
    const chain: N[] = [];
    let k: string | undefined = goalKey;
    while (k !== undefined) {
      chain.push(nodeByKey.get(k)!);
      k = parent.get(k);
    }
    chain.reverse();
    path = chain;
  }

  let status: SearchStatus;
  if (provenOptimal) {
    status = "optimal";
  } else if (budgetHit) {
    status = "budget-exceeded";
  } else if (bestGoalG !== null) {
    // Inadmissible h, stopped at goal pop: feasible but uncertified.
    // If the open list is fully exhausted, g(goal) is provably optimal
    // (every improving relaxation has propagated), independent of h.
    status = heap.size === 0 ? "optimal" : "unproven";
    if (status === "optimal") lowerBound = bestGoalG;
  } else {
    status = "unreachable";
  }

  return { status, path, upperBound: bestGoalG, lowerBound, expansions };
}
