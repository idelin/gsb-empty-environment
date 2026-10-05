import type { CostModel, Op } from './types.ts';
import type { TreeIndex } from './tree.ts';
import { pairable, UNMATCHED, UNDECIDED } from './match.ts';
import type { Matching } from './match.ts';
import { generateScript } from './script.ts';

/**
 * Anytime branch-and-bound over matchings.
 *
 * Target nodes are decided in BFS order: each is either bound to a so-far
 * unused pairable source node or marked UNMATCHED (inserted). Id-forced
 * pairs have a single candidate. The priority queue is ordered by the
 * admissible lower bound f = g + h:
 *
 *   g  exact decided costs: update cost per pair, and exactly one insert
 *      charge per UNMATCHED target node (the generator inserts node by
 *      node, so each unmatched target node costs `insert` precisely)
 *   h  per undecided target node, the cheaper of its best possible update
 *      cost and one insert charge; move and delete costs are ignored (they
 *      only add), so h never overestimates
 *
 * Leaves are scored exactly by generating the real edit script. The search
 * is deterministic: fixed candidate order, (f, -depth, sequence) heap order,
 * and the incumbent is replaced only on strictly lower cost.
 */

export interface SearchResult {
  ops: Op[];
  cost: number;
  matching: Matching;
  optimal: boolean;
  expanded: number;
}

interface State {
  f: number;
  g: number;
  ti: number;
  pairs: Int16Array; // tgtIdx -> srcIdx | UNDECIDED | UNMATCHED
  used: Uint8Array; // srcIdx -> 0/1
  seq: number;
}

export function search(
  src: TreeIndex,
  dst: TreeIndex,
  forced: number[],
  costs: CostModel,
  budget: number,
  incumbent: { ops: Op[]; cost: number; matching: Matching },
): SearchResult {
  const nt = dst.nodes.length;
  const ns = src.nodes.length;

  // Per-target candidate source lists and best achievable update cost.
  const candidates: number[][] = new Array(nt);
  const minUpd = new Array<number>(nt).fill(Number.POSITIVE_INFINITY);
  for (let t = 0; t < nt; t++) {
    if (forced[t] >= 0) {
      candidates[t] = [forced[t]];
      minUpd[t] = updCost(src, dst, forced[t], t, costs);
      continue;
    }
    const list: number[] = [];
    for (let s = 0; s < ns; s++) {
      if (pairable(src.nodes[s], dst.nodes[t])) {
        list.push(s);
        const c = updCost(src, dst, s, t, costs);
        if (c < minUpd[t]) minUpd[t] = c;
      }
    }
    candidates[t] = list;
  }

  const order = dst.bfs;
  // h[t] for a single undecided target node; summed over the suffix.
  const hUnit = new Array<number>(nt);
  for (let t = 0; t < nt; t++) {
    hUnit[t] = forced[t] >= 0 ? minUpd[t] : Math.min(minUpd[t], costs.insert);
  }
  const hSuffix = new Array<number>(nt + 1).fill(0);
  for (let i = order.length - 1; i >= 0; i--) {
    hSuffix[i] = hSuffix[i + 1] + hUnit[order[i]];
  }
  const heuristic = (fromTi: number): number => hSuffix[fromTi];

  let best = incumbent;
  let expanded = 0;
  let seqCounter = 0;

  const heap: State[] = [];
  const less = (a: State, b: State): boolean => {
    if (a.f !== b.f) return a.f < b.f;
    if (a.ti !== b.ti) return a.ti > b.ti; // prefer deeper states
    return a.seq < b.seq;
  };
  const push = (st: State): void => {
    heap.push(st);
    let i = heap.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (less(heap[i], heap[p])) {
        const tmp = heap[i];
        heap[i] = heap[p];
        heap[p] = tmp;
        i = p;
      } else break;
    }
  };
  const pop = (): State => {
    const top = heap[0];
    const last = heap.pop() as State;
    if (heap.length > 0) {
      heap[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        if (l < heap.length && less(heap[l], heap[m])) m = l;
        if (r < heap.length && less(heap[r], heap[m])) m = r;
        if (m === i) break;
        const tmp = heap[i];
        heap[i] = heap[m];
        heap[m] = tmp;
        i = m;
      }
    }
    return top;
  };

  const initial: State = {
    f: 0,
    g: 0,
    ti: 0,
    pairs: new Int16Array(nt).fill(UNDECIDED),
    used: new Uint8Array(ns),
    seq: seqCounter++,
  };
  initial.f = heuristic(0);
  push(initial);

  while (heap.length > 0) {
    if (expanded >= budget) {
      return { ops: best.ops, cost: best.cost, matching: best.matching, optimal: false, expanded };
    }
    const st = pop();
    if (st.f >= best.cost) continue; // cannot improve the incumbent
    expanded++;
    if (st.ti === order.length) {
      const matching: Matching = new Map();
      for (let t = 0; t < nt; t++) {
        if (st.pairs[t] >= 0) matching.set(st.pairs[t], t);
      }
      const gen = generateScript(matching, src, dst, forced, costs);
      if (gen.cost < best.cost) {
        best = { ops: gen.ops, cost: gen.cost, matching: gen.matching };
      }
      continue;
    }
    const t = order[st.ti];
    const cands = candidates[t];
    // Id-forced nodes (and the root) must take their forced partner: no
    // insert branch. If the forced partner is already used the branch dies.
    const allowInsert = forced[t] < 0;
    const options = cands.length + (allowInsert ? 1 : 0);
    // Candidate source partners first (usually cheaper), UNMATCHED last.
    for (let ci = 0; ci < options; ci++) {
      const isInsert = ci === cands.length;
      let s = UNMATCHED;
      if (!isInsert) {
        s = cands[ci];
        if (st.used[s] === 1) continue;
      }
      const pairs = new Int16Array(st.pairs);
      pairs[t] = s;
      const used = new Uint8Array(st.used);
      let g = st.g;
      if (isInsert) {
        g += costs.insert;
      } else {
        used[s] = 1;
        g += updCost(src, dst, s, t, costs);
      }
      const h = heuristic(st.ti + 1);
      push({ f: g + h, g, ti: st.ti + 1, pairs, used, seq: seqCounter++ });
    }
  }
  return { ops: best.ops, cost: best.cost, matching: best.matching, optimal: true, expanded };
}

function updCost(
  src: TreeIndex,
  dst: TreeIndex,
  s: number,
  t: number,
  costs: CostModel,
): number {
  const a = src.nodes[s];
  const b = dst.nodes[t];
  return a.type === b.type && a.value === b.value && a.id === b.id ? 0 : costs.update;
}
