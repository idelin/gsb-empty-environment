/**
 * Syntax repair by token insertion/deletion (unit cost each).
 *
 * The grammar is simulated as a pushdown automaton whose configuration is
 * (input position, symbol stack). Zero-cost moves expand non-terminals and
 * match input tokens; cost-1 moves delete the current input token or insert
 * the terminal the automaton currently expects. This explores exactly the
 * pairs (repaired string, parse) with cost = insertion/deletion distance, so:
 *
 *   Phase 1 (Dijkstra) finds the minimum repair cost C. Because the automaton
 *     only ever inserts terminals it can immediately consume, restricting
 *     insertions to "expected" terminals is complete.
 *   Phase 2 (greedy) rebuilds the lexicographically smallest edit sequence
 *     among all cost-C repairs: at each step it picks the smallest edit (see
 *     edits.ts for the order) that can still be completed within C, using an
 *     exact memoized distance-to-accept oracle.
 *
 * Search budget: phase 1 expands at most `budget` states. If the budget (or
 * the optional `maxCost` cutoff) is exceeded, no repair is returned, only the
 * current bounds: the minimum frontier cost is a valid lower bound on the
 * optimum, and the best relaxed accepting cost (initially n+1: delete
 * everything, insert `0`) is a valid upper bound.
 */

import type { Token, TokenKind } from './tokens.ts';
import { canonicalText } from './tokens.ts';
import type { GrammarSymbol } from './grammar.ts';
import { PRODUCTIONS, START, isNonTerminal, minYieldOf } from './grammar.ts';
import type { Edit } from './edits.ts';
import { applyEdits, editKey } from './edits.ts';

interface State {
  pos: number;
  stack: GrammarSymbol[]; // top of stack is the last element
}

export interface RepairOptions {
  /** Max number of Dijkstra expansions in phase 1. Default: unbounded. */
  budget?: number;
  /** Cost cutoff. Default: n+1 (delete all, insert one integer) — always enough. */
  maxCost?: number;
}

export interface RepairOk {
  status: 'ok';
  cost: number;
  edits: Edit[];
  /** Repaired token stream; original tokens keep their source positions. */
  tokens: Token[];
  expansions: number;
}

export interface RepairExceeded {
  status: 'exceeded';
  reason: 'budget' | 'max-cost';
  lowerBound: number;
  upperBound: number;
  expansions: number;
}

export type RepairResult = RepairOk | RepairExceeded;

function keyOf(s: State): string {
  return s.pos + '|' + s.stack.join(',');
}

function isAccept(s: State, n: number): boolean {
  return s.pos === n && s.stack.length === 0;
}

function stackMinYield(stack: GrammarSymbol[]): number {
  let need = 0;
  for (const sym of stack) need += minYieldOf(sym);
  return need;
}

/**
 * All configurations reachable by zero-cost moves (production expansion and
 * token matching). Pruned by a necessary feasibility bound: the stack must be
 * able to derive its terminals from the remaining input plus the remaining
 * insertion budget. This also keeps the closure finite (nested '(' expansion
 * would otherwise grow the stack forever at zero cost).
 */
function zeroClosure(seeds: State[], cost: number, input: Token[], costLimit: number): State[] {
  const out: State[] = [];
  const seen = new Set<string>();
  const queue: State[] = [...seeds];
  while (queue.length > 0) {
    const s = queue.pop()!;
    const key = keyOf(s);
    if (seen.has(key)) continue;
    seen.add(key);
    if (stackMinYield(s.stack) > (input.length - s.pos) + (costLimit - cost)) continue;
    out.push(s);
    if (s.stack.length === 0) continue;
    const top = s.stack[s.stack.length - 1];
    if (isNonTerminal(top)) {
      for (const rhs of PRODUCTIONS[top]) {
        const ns = s.stack.slice(0, s.stack.length - 1);
        for (let i = rhs.length - 1; i >= 0; i--) ns.push(rhs[i]);
        queue.push({ pos: s.pos, stack: ns });
      }
    } else if (s.pos < input.length && input[s.pos].kind === top) {
      queue.push({ pos: s.pos + 1, stack: s.stack.slice(0, s.stack.length - 1) });
    }
  }
  return out;
}

/**
 * Admissible heuristic: the stack must still derive `minYield` terminals, of
 * which at most `remaining input` can come from free matches — the rest must
 * be inserted at cost 1 each.
 */
function heuristic(s: State, n: number): number {
  return Math.max(0, stackMinYield(s.stack) - (n - s.pos));
}

interface HeapEntry {
  prio: number; // cost + heuristic
  cost: number;
  seq: number;
  state: State;
}

class MinHeap {
  private entries: HeapEntry[] = [];
  private counter = 0;

  get size(): number {
    return this.entries.length;
  }

  peekPrio(): number {
    return this.entries[0].prio;
  }

  push(prio: number, cost: number, state: State): void {
    this.entries.push({ prio, cost, seq: this.counter++, state });
    let i = this.entries.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (this.less(this.entries[i], this.entries[p])) {
        [this.entries[i], this.entries[p]] = [this.entries[p], this.entries[i]];
        i = p;
      } else break;
    }
  }

  pop(): HeapEntry {
    const top = this.entries[0];
    const last = this.entries.pop()!;
    if (this.entries.length > 0) {
      this.entries[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = 2 * i + 2;
        let m = i;
        if (l < this.entries.length && this.less(this.entries[l], this.entries[m])) m = l;
        if (r < this.entries.length && this.less(this.entries[r], this.entries[m])) m = r;
        if (m === i) break;
        [this.entries[i], this.entries[m]] = [this.entries[m], this.entries[i]];
        i = m;
      }
    }
    return top;
  }

  private less(a: HeapEntry, b: HeapEntry): boolean {
    return (
      a.prio < b.prio ||
      (a.prio === b.prio && (a.cost < b.cost || (a.cost === b.cost && a.seq < b.seq)))
    );
  }
}

interface CostSearch {
  cost: number | null;
  lowerBound: number;
  upperBound: number;
  expansions: number;
  hitBudget: boolean;
}

function findMinCost(input: Token[], maxCost: number, budget: number): CostSearch {
  const n = input.length;
  let upperBound = n + 1; // delete everything, insert a single integer
  const dist = new Map<string, number>();
  const heap = new MinHeap();
  let expansions = 0;

  const relax = (state: State, cost: number): void => {
    if (cost > maxCost) return;
    for (const s of zeroClosure([state], cost, input, maxCost)) {
      const key = keyOf(s);
      const prev = dist.get(key);
      if (prev !== undefined && prev <= cost) continue;
      dist.set(key, cost);
      if (isAccept(s, n) && cost < upperBound) upperBound = cost;
      heap.push(cost + heuristic(s, n), cost, s);
    }
  };

  relax({ pos: 0, stack: [START] }, 0);

  while (heap.size > 0) {
    if (expansions >= budget) {
      // Every path to an accepting state passes through the frontier, and the
      // heuristic is admissible, so the minimum frontier priority is a valid
      // lower bound on the optimum.
      return { cost: null, lowerBound: heap.peekPrio(), upperBound, expansions, hitBudget: true };
    }
    const entry = heap.pop();
    const s = entry.state;
    if (entry.cost > dist.get(keyOf(s))!) continue; // stale heap entry
    expansions++;
    if (isAccept(s, n)) {
      return { cost: entry.cost, lowerBound: entry.cost, upperBound: entry.cost, expansions, hitBudget: false };
    }
    if (s.pos < n) relax({ pos: s.pos + 1, stack: s.stack }, entry.cost + 1); // delete
    if (s.stack.length > 0) {
      const top = s.stack[s.stack.length - 1];
      if (!isNonTerminal(top)) {
        relax({ pos: s.pos, stack: s.stack.slice(0, s.stack.length - 1) }, entry.cost + 1); // insert
      }
    }
  }
  // Frontier exhausted under the cost cutoff: the optimum exceeds maxCost.
  return { cost: null, lowerBound: maxCost + 1, upperBound, expansions, hitBudget: false };
}

/** Exact distance-to-accept oracle with memoization, used by the greedy phase. */
function makeDistOracle(input: Token[]): (state: State, maxCost: number) => number {
  const n = input.length;
  const exact = new Map<string, number>();
  const capped = new Set<string>(); // `${maxCost}|${key}` known to exceed maxCost

  return function distToAccept(state: State, maxCost: number): number {
    const key = keyOf(state);
    const hit = exact.get(key);
    if (hit !== undefined && hit <= maxCost) return hit;
    const capKey = maxCost + '|' + key;
    if (capped.has(capKey)) return Infinity;

    const dist = new Map<string, number>();
    const heap = new MinHeap();
    const relax = (st: State, cost: number): void => {
      if (cost > maxCost) return;
      for (const s of zeroClosure([st], cost, input, maxCost)) {
        const k = keyOf(s);
        const prev = dist.get(k);
        if (prev !== undefined && prev <= cost) continue;
        dist.set(k, cost);
        heap.push(cost + heuristic(s, n), cost, s);
      }
    };
    relax(state, 0);
    while (heap.size > 0) {
      const entry = heap.pop();
      const s = entry.state;
      if (entry.cost > dist.get(keyOf(s))!) continue;
      if (isAccept(s, n)) {
        exact.set(key, entry.cost);
        return entry.cost;
      }
      if (s.pos < n) relax({ pos: s.pos + 1, stack: s.stack }, entry.cost + 1);
      if (s.stack.length > 0) {
        const top = s.stack[s.stack.length - 1];
        if (!isNonTerminal(top)) {
          relax({ pos: s.pos, stack: s.stack.slice(0, s.stack.length - 1) }, entry.cost + 1);
        }
      }
    }
    capped.add(capKey);
    return Infinity;
  };
}

/**
 * Rebuild the lexicographically smallest cost-C edit sequence. Invariant:
 * `current` holds every automaton configuration reachable by some repair
 * whose edit prefix equals `edits`, at cost `spent`.
 */
function greedyLexMin(input: Token[], cost: number): Edit[] {
  const n = input.length;
  const distToAccept = makeDistOracle(input);
  const edits: Edit[] = [];
  let current = zeroClosure([{ pos: 0, stack: [START] }], 0, input, cost);
  let spent = 0;

  while (!current.some((s) => isAccept(s, n))) {
    const byKey = new Map<string, { edit: Edit; next: State[] }>();
    const add = (edit: Edit, next: State): void => {
      const k = editKey(edit);
      const slot = byKey.get(k);
      if (slot) slot.next.push(next);
      else byKey.set(k, { edit, next: [next] });
    };
    for (const s of current) {
      if (s.pos < n) {
        add({ type: 'delete', index: s.pos, kind: input[s.pos].kind }, { pos: s.pos + 1, stack: s.stack });
      }
      if (s.stack.length > 0) {
        const top = s.stack[s.stack.length - 1];
        if (!isNonTerminal(top)) {
          add({ type: 'insert', index: s.pos, kind: top }, { pos: s.pos, stack: s.stack.slice(0, s.stack.length - 1) });
        }
      }
    }
    let chosen: { edit: Edit; states: State[] } | null = null;
    for (const k of [...byKey.keys()].sort()) {
      const cand = byKey.get(k)!;
      const feasible = cand.next.filter(
        (ns) => spent + 1 + distToAccept(ns, cost - spent - 1) <= cost,
      );
      if (feasible.length > 0) {
        chosen = { edit: cand.edit, states: feasible };
        break;
      }
    }
    if (chosen === null) throw new Error('repair: no feasible edit (internal inconsistency)');
    edits.push(chosen.edit);
    spent += 1;
    current = zeroClosure(chosen.states, spent, input, cost);
  }
  if (spent !== cost) throw new Error('repair: greedy cost mismatch (internal inconsistency)');
  return edits;
}

export function repair(input: Token[], options: RepairOptions = {}): RepairResult {
  const budget = options.budget ?? Number.MAX_SAFE_INTEGER;
  const maxCost = options.maxCost ?? input.length + 1;
  const search = findMinCost(input, maxCost, budget);
  if (search.cost === null) {
    return {
      status: 'exceeded',
      reason: search.hitBudget ? 'budget' : 'max-cost',
      lowerBound: search.lowerBound,
      upperBound: search.upperBound,
      expansions: search.expansions,
    };
  }
  const edits = greedyLexMin(input, search.cost);
  return {
    status: 'ok',
    cost: search.cost,
    edits,
    tokens: applyEdits(input, edits),
    expansions: search.expansions,
  };
}

export { canonicalText };
export type { TokenKind };
