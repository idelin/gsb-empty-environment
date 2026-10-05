/**
 * Constrained topological sorter.
 *
 * Given precedence edges and fixed-position pins (0-based, at most one pin
 * per node), returns the lexicographically smallest permutation of 0..n-1
 * that respects both, or verifiable evidence that no such permutation
 * exists, or 'unknown' when the search budget is exhausted.
 *
 * A successful plain topological sort only proves the precedence relation
 * is acyclic; it says nothing about pin feasibility. This solver therefore
 * never treats a cycle-free graph as sufficient evidence for a solution:
 * feasibility is decided by structural pin checks plus a bounded,
 * budget-limited search.
 */

export interface SearchStats {
  /** Candidate placements attempted by the search. */
  explored: number;
  /** Maximum placements allowed before the search gives up. */
  budget: number;
}

export type Evidence =
  | { kind: 'duplicate-pin-position'; position: number; nodes: number[] }
  | { kind: 'pin-out-of-range'; node: number; position: number; n: number }
  | { kind: 'cycle'; cycle: number[] }
  | { kind: 'ancestors-exceed-slot'; node: number; position: number; ancestors: number[] }
  | { kind: 'descendants-exceed-slot'; node: number; position: number; descendants: number[] }
  | {
      kind: 'pinned-order-contradiction';
      ancestor: number;
      ancestorPos: number;
      descendant: number;
      descendantPos: number;
      chain: number[];
    }
  | { kind: 'exhaustive-search'; explored: number };

export type SolveResult =
  | { status: 'ok'; order: number[]; stats: SearchStats }
  | { status: 'infeasible'; evidence: Evidence; stats: SearchStats }
  | { status: 'unknown'; reason: 'budget-exhausted'; stats: SearchStats };

export type Edge = readonly [number, number];

export interface Graph {
  succ: number[][];
  pred: number[][];
}

export function buildGraph(n: number, edges: ReadonlyArray<Edge>): Graph {
  const succ: number[][] = [];
  const pred: number[][] = [];
  for (let i = 0; i < n; i++) {
    succ.push([]);
    pred.push([]);
  }
  const seen: Set<number> = new Set();
  for (const [a, b] of edges) {
    if (!Number.isInteger(a) || !Number.isInteger(b) || a < 0 || b < 0 || a >= n || b >= n) {
      throw new RangeError('edge endpoint out of range: [' + a + ', ' + b + '] for n=' + n);
    }
    const key = a * n + b;
    if (seen.has(key)) continue;
    seen.add(key);
    succ[a].push(b);
    pred[b].push(a);
  }
  return { succ, pred };
}

/**
 * Returns one cycle as a node list c with edges c[i] -> c[i+1] and
 * c[last] -> c[0], or null when the graph is acyclic.
 */
export function findCycle(n: number, succ: number[][]): number[] | null {
  const state: number[] = new Array(n).fill(0); // 0=unvisited 1=on-stack 2=done
  const stack: number[] = [];
  const iterIndex: number[] = new Array(n).fill(0);
  for (let start = 0; start < n; start++) {
    if (state[start] !== 0) continue;
    state[start] = 1;
    stack.push(start);
    while (stack.length > 0) {
      const v = stack[stack.length - 1];
      if (iterIndex[v] < succ[v].length) {
        const w = succ[v][iterIndex[v]];
        iterIndex[v]++;
        if (state[w] === 0) {
          state[w] = 1;
          stack.push(w);
        } else if (state[w] === 1) {
          return stack.slice(stack.indexOf(w));
        }
      } else {
        state[v] = 2;
        stack.pop();
      }
    }
  }
  return null;
}

/** reach[u][v] is true iff v is reachable from u via one or more edges. */
export function computeReachability(n: number, succ: number[][]): boolean[][] {
  const reach: boolean[][] = [];
  for (let i = 0; i < n; i++) {
    reach.push(new Array(n).fill(false));
  }
  for (let s = 0; s < n; s++) {
    const stack: number[] = succ[s].slice();
    while (stack.length > 0) {
      const v = stack[stack.length - 1];
      stack.length -= 1;
      if (reach[s][v]) continue;
      reach[s][v] = true;
      for (const w of succ[v]) {
        if (!reach[s][w]) stack.push(w);
      }
    }
  }
  return reach;
}
/** BFS shortest directed path from `from` to `to` (must exist). */
export function witnessChain(succ: number[][], from: number, to: number): number[] {
  if (from === to) return [from];
  const prev: Map<number, number> = new Map();
  const seen: Set<number> = new Set();
  seen.add(from);
  const queue: number[] = [from];
  let head = 0;
  while (head < queue.length) {
    const v = queue[head];
    head++;
    for (const w of succ[v]) {
      if (seen.has(w)) continue;
      seen.add(w);
      prev.set(w, v);
      if (w === to) {
        const chain: number[] = [to];
        let cur = to;
        while (cur !== from) {
          const p = prev.get(cur);
          if (p === undefined) throw new Error('broken witness chain');
          chain.push(p);
          cur = p;
        }
        chain.reverse();
        return chain;
      }
      queue.push(w);
    }
  }
  throw new Error('no path from ' + from + ' to ' + to);
}

const BUDGET_EXHAUSTED = { marker: 'budget-exhausted' };

/**
 * Solves the constrained topological ordering problem.
 *
 * @param n      number of nodes, labeled 0..n-1
 * @param edges  precedence pairs [before, after]
 * @param pins   map node -> fixed 0-based position (at most one per node)
 * @param budget max candidate placements the search may attempt
 */
export function solve(
  n: number,
  edges: ReadonlyArray<Edge>,
  pins: ReadonlyMap<number, number>,
  budget: number = 1000000
): SolveResult {
  if (!Number.isInteger(n) || n < 0) throw new RangeError('n must be a non-negative integer');
  if (!Number.isInteger(budget) || budget < 0) {
    throw new RangeError('budget must be a non-negative integer');
  }
  const stats: SearchStats = { explored: 0, budget };

  const graph = buildGraph(n, edges);
  const succ = graph.succ;
  const pred = graph.pred;

  // ---- structural, independently verifiable infeasibility checks ----

  for (const [v, p] of pins) {
    if (!Number.isInteger(v) || v < 0 || v >= n) {
      throw new RangeError('pinned node out of range: ' + v);
    }
    if (!Number.isInteger(p) || p < 0 || p >= n) {
      return {
        status: 'infeasible',
        evidence: { kind: 'pin-out-of-range', node: v, position: p, n },
        stats,
      };
    }
  }

  const nodesAtPos: Map<number, number[]> = new Map();
  for (const [v, p] of pins) {
    const list = nodesAtPos.get(p);
    if (list === undefined) nodesAtPos.set(p, [v]);
    else list.push(v);
  }
  for (const [p, nodes] of nodesAtPos) {
    if (nodes.length > 1) {
      nodes.sort((a, b) => a - b);
      return {
        status: 'infeasible',
        evidence: { kind: 'duplicate-pin-position', position: p, nodes },
        stats,
      };
    }
  }

  const cycle = findCycle(n, succ);
  if (cycle !== null) {
    return { status: 'infeasible', evidence: { kind: 'cycle', cycle }, stats };
  }

  const reach = computeReachability(n, succ);
  const ancestors: number[][] = [];
  const descendants: number[][] = [];
  for (let v = 0; v < n; v++) {
    const anc: number[] = [];
    const desc: number[] = [];
    for (let u = 0; u < n; u++) {
      if (reach[u][v]) anc.push(u);
      if (reach[v][u]) desc.push(u);
    }
    ancestors.push(anc);
    descendants.push(desc);
  }

  const pinnedNodes: number[] = [];
  for (const v of pins.keys()) pinnedNodes.push(v);
  pinnedNodes.sort((a, b) => a - b);

  for (const v of pinnedNodes) {
    const p = pins.get(v);
    if (p === undefined) continue;
    if (ancestors[v].length > p) {
      return {
        status: 'infeasible',
        evidence: { kind: 'ancestors-exceed-slot', node: v, position: p, ancestors: ancestors[v] },
        stats,
      };
    }
    if (descendants[v].length > n - 1 - p) {
      return {
        status: 'infeasible',
        evidence: {
          kind: 'descendants-exceed-slot',
          node: v,
          position: p,
          descendants: descendants[v],
        },
        stats,
      };
    }
  }

  for (const u of pinnedNodes) {
    for (const v of pinnedNodes) {
      if (u === v || !reach[u][v]) continue;
      const pu = pins.get(u);
      const pv = pins.get(v);
      if (pu === undefined || pv === undefined) continue;
      if (pu >= pv) {
        return {
          status: 'infeasible',
          evidence: {
            kind: 'pinned-order-contradiction',
            ancestor: u,
            ancestorPos: pu,
            descendant: v,
            descendantPos: pv,
            chain: witnessChain(succ, u, v),
          },
          stats,
        };
      }
    }
  }
  // ---- bounded lexicographic search ----
  //
  // Depth-first search over positions 0..n-1 trying candidates in ascending
  // node order, so the first complete assignment found is the
  // lexicographically smallest valid permutation. A pinned node may only be
  // placed at its own position; unpinned positions never host pinned nodes.

  const pinPosOf: number[] = new Array(n).fill(-1);
  const nodeAtPos: number[] = new Array(n).fill(-1);
  for (const [v, p] of pins) {
    pinPosOf[v] = p;
    nodeAtPos[p] = v;
  }
  const remainingPreds: number[] = pred.map((list) => list.length);
  const placed: boolean[] = new Array(n).fill(false);
  const order: number[] = new Array(n).fill(-1);

  function forwardCheck(nextPos: number): boolean {
    for (const v of pinnedNodes) {
      if (placed[v]) continue;
      const p = pinPosOf[v];
      if (p < nextPos) return false; // pinned node missed its slot
      let need = 0;
      for (const u of ancestors[v]) {
        if (!placed[u]) need++;
      }
      if (need > p - nextPos) return false; // ancestors no longer fit before p
    }
    return true;
  }

  function place(v: number, pos: number): boolean {
    stats.explored++;
    if (stats.explored > stats.budget) throw BUDGET_EXHAUSTED;
    placed[v] = true;
    order[pos] = v;
    for (const w of succ[v]) remainingPreds[w]--;
    if (dfs(pos + 1)) return true;
    placed[v] = false;
    order[pos] = -1;
    for (const w of succ[v]) remainingPreds[w]++;
    return false;
  }

  function dfs(pos: number): boolean {
    if (pos === n) return true;
    if (!forwardCheck(pos)) return false;
    const pinnedHere = nodeAtPos[pos];
    if (pinnedHere !== -1) {
      if (placed[pinnedHere] || remainingPreds[pinnedHere] > 0) return false;
      return place(pinnedHere, pos);
    }
    for (let v = 0; v < n; v++) {
      if (placed[v] || remainingPreds[v] > 0 || pinPosOf[v] !== -1) continue;
      if (place(v, pos)) return true;
    }
    return false;
  }

  try {
    if (dfs(0)) {
      return { status: 'ok', order: order.slice(), stats };
    }
    return {
      status: 'infeasible',
      evidence: { kind: 'exhaustive-search', explored: stats.explored },
      stats,
    };
  } catch (err) {
    if (err === BUDGET_EXHAUSTED) {
      return { status: 'unknown', reason: 'budget-exhausted', stats };
    }
    throw err;
  }
}
