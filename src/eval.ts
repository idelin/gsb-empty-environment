// Abstract three-valued evaluation of conditions over cells. Leaf predicates
// are evaluated exactly on boxes (intervals / value sets); boolean
// combinators use Kleene strong logic. Results are memoized on the shared
// (hash-consed) condition nodes, and every computation is budgeted.

import type { Cond, Tri } from './conditions.ts';
import { compSize, type Cell, type Comp } from './domain.ts';

export class BudgetExceeded extends Error {
  constructor() { super('evaluation budget exceeded'); }
}

export interface Budget { remaining: number; }

export type OutcomeCache = Map<string, ReadonlySet<Tri>>;

function spend(budget: Budget): void {
  budget.remaining -= 1;
  if (budget.remaining < 0) throw new BudgetExceeded();
}

type CmpLikeOp = '<' | '<=' | '==' | '!=' | '>=' | '>';

/** Exact outcome set of `x <op> c` for x ranging over [lo, hi]. */
function rangeOutcomes(op: CmpLikeOp, c: number, lo: number, hi: number, out: Set<Tri>): void {
  let allSat: boolean;
  let noneSat: boolean;
  switch (op) {
    case '<': allSat = hi < c; noneSat = lo >= c; break;
    case '<=': allSat = hi <= c; noneSat = lo > c; break;
    case '>': allSat = lo > c; noneSat = hi <= c; break;
    case '>=': allSat = lo >= c; noneSat = hi < c; break;
    case '==': allSat = lo === hi && lo === c; noneSat = c < lo || c > hi; break;
    case '!=': allSat = c < lo || c > hi; noneSat = lo === hi && lo === c; break;
  }
  if (allSat) out.add('T');
  else if (noneSat) out.add('F');
  else { out.add('T'); out.add('F'); }
}

function leafOutcomes(cond: Cond, cell: Cell): Set<Tri> {
  const out = new Set<Tri>();
  switch (cond.kind) {
    case 'const':
      out.add(cond.value ? 'T' : 'F');
      return out;
    case 'cmp': {
      const comp = cell[cond.var] as Comp & { kind: 'int' };
      if (comp.lo <= comp.hi) rangeOutcomes(cond.op, cond.value, comp.lo, comp.hi, out);
      if (comp.unk) out.add('U');
      return out;
    }
    case 'diff': {
      const l = cell[cond.left] as Comp & { kind: 'int' };
      const r = cell[cond.right] as Comp & { kind: 'int' };
      if (l.lo <= l.hi && r.lo <= r.hi) {
        // x - y attains every integer in [loL - hiR, hiL - loR] on a box.
        rangeOutcomes(cond.op, cond.c, l.lo - r.hi, l.hi - r.lo, out);
      }
      if (l.unk || r.unk) out.add('U');
      return out;
    }
    case 'in': {
      const comp = cell[cond.var];
      const values = comp.kind === 'enum'
        ? comp.values
        : rangeList((comp as Comp & { kind: 'int' }).lo, (comp as Comp & { kind: 'int' }).hi);
      let anyIn = false;
      let anyOut = false;
      for (const v of values) {
        if (cond.values.includes(v)) anyIn = true; else anyOut = true;
        if (anyIn && anyOut) break;
      }
      if (anyIn) out.add('T');
      if (anyOut) out.add('F');
      if (comp.unk) out.add('U');
      return out;
    }
    default:
      throw new Error(`not a leaf: ${cond.kind}`);
  }
}

function rangeList(lo: number, hi: number): number[] {
  const out: number[] = [];
  for (let i = lo; i <= hi; i++) out.push(i);
  return out;
}

const NEGATE: Readonly<Record<Tri, Tri>> = { T: 'F', F: 'T', U: 'U' };

function combineAnd(a: ReadonlySet<Tri>, b: ReadonlySet<Tri>): Set<Tri> {
  const out = new Set<Tri>();
  for (const x of a) for (const y of b) {
    out.add(x === 'F' || y === 'F' ? 'F' : x === 'U' || y === 'U' ? 'U' : 'T');
  }
  return out;
}

function combineOr(a: ReadonlySet<Tri>, b: ReadonlySet<Tri>): Set<Tri> {
  const out = new Set<Tri>();
  for (const x of a) for (const y of b) {
    out.add(x === 'T' || y === 'T' ? 'T' : x === 'U' || y === 'U' ? 'U' : 'F');
  }
  return out;
}

/**
 * Possible Kleene outcomes of `cond` over `cell`. Exact for leaves; a sound
 * over-approximation for boolean combinations (correlations between sibling
 * subconditions are refined away by cell splitting in the caller).
 */
export function outcomes(
  cond: Cond,
  cell: Cell,
  cellKey: string,
  cache: OutcomeCache,
  budget: Budget,
): ReadonlySet<Tri> {
  const key = `${cond.id}|${cellKey}`;
  const hit = cache.get(key);
  if (hit !== undefined) return hit;
  spend(budget);

  let result: ReadonlySet<Tri>;
  switch (cond.kind) {
    case 'const':
    case 'cmp':
    case 'diff':
    case 'in':
      result = leafOutcomes(cond, cell);
      break;
    case 'not': {
      const inner = outcomes(cond.a, cell, cellKey, cache, budget);
      result = new Set([...inner].map((t) => NEGATE[t]));
      break;
    }
    case 'and': {
      let acc: ReadonlySet<Tri> = new Set<Tri>(['T']);
      for (const arg of cond.args) acc = combineAnd(acc, outcomes(arg, cell, cellKey, cache, budget));
      result = acc;
      break;
    }
    case 'or': {
      let acc: ReadonlySet<Tri> = new Set<Tri>(['F']);
      for (const arg of cond.args) acc = combineOr(acc, outcomes(arg, cell, cellKey, cache, budget));
      result = acc;
      break;
    }
  }
  cache.set(key, result);
  return result;
}

export function isEmptyCell(cell: Cell, order: readonly string[]): boolean {
  return order.some((v) => compSize(cell[v]) === 0);
}
