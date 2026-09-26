/**
 * Finite-domain integer constraint solver, built from scratch.
 *
 * Supported constraint kinds (the promised subset, nothing more):
 *  - eq:       a == b                    (var or const on each side)
 *  - diff:     a - b (op) c              (difference constraint, integers)
 *  - sum:      sum(coeff_i * x_i) (op) rhs
 *  - implies:  cond => then              (conditional branch; cond is a
 *                                         literal on one variable)
 *
 * Results are three-valued: 'sat' (with a model), 'unsat', or 'unknown'
 * when the step budget is exhausted. Budget exhaustion is ALWAYS reported
 * distinctly from unsatisfiability.
 */

export type Ref = { var: string } | { const: number };
export const v = (name: string): Ref => ({ var: name });
export const k = (n: number): Ref => ({ const: n });

export type RelOp = '<=' | '>=' | '==';

export interface Literal {
  var: string;
  op: '==' | '!=' | '<' | '<=' | '>' | '>=';
  value: number;
}

export type SimpleConstraint =
  | { kind: 'eq'; a: Ref; b: Ref }
  | { kind: 'diff'; a: string; b: string; op: RelOp; c: number }
  | { kind: 'sum'; terms: { var: string; coeff: number }[]; op: RelOp; rhs: number };

export type Constraint =
  | SimpleConstraint
  | { kind: 'implies'; cond: Literal; then: SimpleConstraint };

export interface VarDecl {
  name: string;
  domain: number[];
}

export interface SolveOptions {
  /** Total propagation+search step budget. Omit for unbounded. */
  budget?: number;
}

export interface SolveResult {
  status: 'sat' | 'unsat' | 'unknown';
  model?: Record<string, number>;
  /** 'budget-exhausted' iff status === 'unknown'. */
  reason?: 'budget-exhausted';
  stats: { nodes: number; steps: number };
}

type Domain = Set<number>;

class Budget {
  left: number;
  constructor(limit?: number) {
    this.left = limit ?? Number.POSITIVE_INFINITY;
  }
  spend(n = 1): boolean {
    if (this.left < n) return false;
    this.left -= n;
    return true;
  }
  get exhausted(): boolean {
    return this.left <= 0;
  }
}

function refDomain(ref: Ref, domains: Map<string, Domain>): Domain {
  return 'const' in ref ? new Set([ref.const]) : domains.get(ref.var)!;
}

function literalHolds(lit: Literal, value: number): boolean {
  switch (lit.op) {
    case '==': return value === lit.value;
    case '!=': return value !== lit.value;
    case '<':  return value <  lit.value;
    case '<=': return value <= lit.value;
    case '>':  return value >  lit.value;
    case '>=': return value >= lit.value;
  }
}

const minOf = (d: Domain): number => Math.min(...d);
const maxOf = (d: Domain): number => Math.max(...d);

function filterDomain(d: Domain, keep: (x: number) => boolean): boolean {
  let changed = false;
  for (const x of d) {
    if (!keep(x)) {
      d.delete(x);
      changed = true;
    }
  }
  return changed;
}

/**
 * One propagation pass of a simple constraint over current domains.
 * Bounds-based: sound (never removes a solution), not necessarily complete.
 * Returns true iff some domain shrank.
 */
function propagateSimple(c: SimpleConstraint, domains: Map<string, Domain>): boolean {
  let changed = false;
  switch (c.kind) {
    case 'eq': {
      const da = refDomain(c.a, domains);
      const db = refDomain(c.b, domains);
      const inter = new Set([...da].filter((x) => db.has(x)));
      for (const ref of [c.a, c.b]) {
        if ('var' in ref) {
          const d = domains.get(ref.var)!;
          if (filterDomain(d, (x) => inter.has(x))) changed = true;
        }
      }
      break;
    }
    case 'diff': {
      const da = domains.get(c.a)!;
      const db = domains.get(c.b)!;
      // a - b <= c  =>  a <= maxB + c  and  b >= minA - c
      // a - b >= c  =>  a >= minB + c  and  b <= maxA - c
      if (c.op === '<=' || c.op === '==') {
        const aMax = maxOf(db) + c.c;
        const bMin = minOf(da) - c.c;
        if (filterDomain(da, (x) => x <= aMax)) changed = true;
        if (filterDomain(db, (x) => x >= bMin)) changed = true;
      }
      if (c.op === '>=' || c.op === '==') {
        const aMin = minOf(db) + c.c;
        const bMax = maxOf(da) - c.c;
        if (filterDomain(da, (x) => x >= aMin)) changed = true;
        if (filterDomain(db, (x) => x <= bMax)) changed = true;
      }
      break;
    }
    case 'sum': {
      const doms = c.terms.map((t) => domains.get(t.var)!);
      const termMin = (i: number): number => {
        const t = c.terms[i];
        return t.coeff >= 0 ? t.coeff * minOf(doms[i]) : t.coeff * maxOf(doms[i]);
      };
      const termMax = (i: number): number => {
        const t = c.terms[i];
        return t.coeff >= 0 ? t.coeff * maxOf(doms[i]) : t.coeff * minOf(doms[i]);
      };
      const totalMin = c.terms.reduce((s, _t, i) => s + termMin(i), 0);
      const totalMax = c.terms.reduce((s, _t, i) => s + termMax(i), 0);
      for (let i = 0; i < c.terms.length; i++) {
        const t = c.terms[i];
        if (t.coeff === 0) continue;
        const restMin = totalMin - termMin(i);
        const restMax = totalMax - termMax(i);
        const d = doms[i];
        if (c.op === '<=' || c.op === '==') {
          const bound = c.rhs - restMin; // coeff*x <= bound
          if (filterDomain(d, (x) => t.coeff * x <= bound)) changed = true;
        }
        if (c.op === '>=' || c.op === '==') {
          const bound = c.rhs - restMax; // coeff*x >= bound
          if (filterDomain(d, (x) => t.coeff * x >= bound)) changed = true;
        }
      }
      break;
    }
  }
  return changed;
}

/** Evaluate a simple constraint on a full assignment. */
export function checkSimple(c: SimpleConstraint, asg: Record<string, number>): boolean {
  const val = (r: Ref): number => ('const' in r ? r.const : asg[r.var]);
  switch (c.kind) {
    case 'eq':
      return val(c.a) === val(c.b);
    case 'diff': {
      const d = asg[c.a] - asg[c.b];
      return c.op === '<=' ? d <= c.c : c.op === '>=' ? d >= c.c : d === c.c;
    }
    case 'sum': {
      const total = c.terms.reduce((s, t) => s + t.coeff * asg[t.var], 0);
      return c.op === '<=' ? total <= c.rhs : c.op === '>=' ? total >= c.rhs : total === c.rhs;
    }
  }
}

/** Evaluate any constraint on a full assignment (implication-aware). */
export function checkConstraint(c: Constraint, asg: Record<string, number>): boolean {
  if (c.kind === 'implies') {
    return literalHolds(c.cond, asg[c.cond.var]) ? checkSimple(c.then, asg) : true;
  }
  return checkSimple(c, asg);
}

export function checkAll(constraints: Constraint[], asg: Record<string, number>): boolean {
  return constraints.every((c) => checkConstraint(c, asg));
}

/**
 * Propagate to fixpoint. Returns 'ok' | 'conflict' | 'budget'.
 * An implication fires only when its condition is entailed by the current
 * domain; a refuted condition makes it vacuous (no pruning, no error).
 */
function propagate(
  constraints: Constraint[],
  domains: Map<string, Domain>,
  budget: Budget,
): 'ok' | 'conflict' | 'budget' {
  const wiped = (): boolean => [...domains.values()].some((d) => d.size === 0);
  let dirty = true;
  while (dirty) {
    dirty = false;
    for (const c of constraints) {
      if (!budget.spend()) return 'budget';
      if (c.kind === 'implies') {
        const d = domains.get(c.cond.var)!;
        const allFalse = [...d].every((x) => !literalHolds(c.cond, x));
        if (allFalse) continue; // vacuous
        const allTrue = [...d].every((x) => literalHolds(c.cond, x));
        if (!allTrue) continue; // undecided: no propagation yet
        if (propagateSimple(c.then, domains)) dirty = true;
      } else {
        if (propagateSimple(c, domains)) dirty = true;
      }
      if (wiped()) return 'conflict';
    }
  }
  return 'ok';
}

function cloneDomains(domains: Map<string, Domain>): Map<string, Domain> {
  return new Map([...domains].map(([name, d]) => [name, new Set(d)]));
}

export function solve(vars: VarDecl[], constraints: Constraint[], opts: SolveOptions = {}): SolveResult {
  const budget = new Budget(opts.budget);
  const stats = { nodes: 0, steps: 0 };
  const root = new Map<string, Domain>(vars.map((decl) => [decl.name, new Set(decl.domain)]));
  const unknown: SolveResult = { status: 'unknown', reason: 'budget-exhausted', stats };

  const search = (domains: Map<string, Domain>): SolveResult | null => {
    stats.steps++;
    const p = propagate(constraints, domains, budget);
    if (p === 'conflict') return null;
    if (p === 'budget') return unknown;

    const undecided = vars.filter((decl) => domains.get(decl.name)!.size > 1);
    if (undecided.length === 0) {
      const model: Record<string, number> = {};
      for (const decl of vars) model[decl.name] = [...domains.get(decl.name)!][0];
      // Bounds propagation is sound but incomplete: fully verify the leaf.
      return checkAll(constraints, model) ? { status: 'sat', model, stats } : null;
    }

    // Smallest-domain-first branching.
    undecided.sort((a, b) => domains.get(a.name)!.size - domains.get(b.name)!.size);
    const pick = undecided[0].name;
    for (const value of [...domains.get(pick)!].sort((a, b) => a - b)) {
      if (!budget.spend()) return unknown;
      stats.nodes++;
      const next = cloneDomains(domains);
      next.set(pick, new Set([value]));
      const r = search(next);
      if (r === null) continue; // branch conflict: try next value
      return r; // sat or unknown (budget is global: stop here either way)
    }
    return null; // every branch conflicted
  };

  const result = search(root);
  return result ?? { status: 'unsat', stats };
}
