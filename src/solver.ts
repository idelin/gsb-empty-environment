/**
 * Satisfiability over finite integer domains.
 *
 * Constraints (all variables range over finite integer intervals):
 *   eqConst  x = k
 *   eqVar    x = y
 *   diff     x - y <= c          (difference constraint)
 *   sum      sum(xs) <=/>=/== k  (list sum)
 *   when     cond => then        (conditional branch; cond is a comparison)
 *
 * The solver combines bounds propagation with backtracking search and
 * returns one of three clearly separated outcomes:
 *   sat              — with a model (witness), re-checked by direct evaluation
 *   unsat            — with a subset-minimal conflict (constraint ids)
 *   budget-exceeded  — neither proven; explicitly distinct from unsat
 */

export interface SolverVariable {
  readonly name: string;
  readonly lo: number;
  readonly hi: number;
}

export type ComparisonOp = "le" | "lt" | "ge" | "gt" | "eq" | "ne";

export interface SolverCondition {
  readonly op: ComparisonOp;
  readonly a: string;
  readonly b: string | number;
}

export type SimpleConstraint =
  | { readonly kind: "eqConst"; readonly id: string; readonly a: string; readonly value: number }
  | { readonly kind: "eqVar"; readonly id: string; readonly a: string; readonly b: string }
  | { readonly kind: "diff"; readonly id: string; readonly a: string; readonly b: string; readonly c: number }
  | {
      readonly kind: "sum";
      readonly id: string;
      readonly vars: readonly string[];
      readonly op: "le" | "ge" | "eq";
      readonly value: number;
    };

export type SolverConstraint =
  | SimpleConstraint
  | { readonly kind: "when"; readonly id: string; readonly cond: SolverCondition; readonly then: SimpleConstraint };

export type SolveResult =
  | { readonly status: "sat"; readonly model: Record<string, number>; readonly nodes: number }
  | { readonly status: "unsat"; readonly conflict: readonly string[]; readonly nodes: number }
  | { readonly status: "budget-exceeded"; readonly nodes: number };

export interface SolveOptions {
  /** Max search nodes before giving up (default 100_000). */
  readonly maxNodes?: number;
  /** Max propagation steps before giving up (default 5_000_000). */
  readonly maxPropSteps?: number;
  /** Compute a subset-minimal conflict on unsat (default true). */
  readonly minimizeConflict?: boolean;
}

interface Dom {
  lo: number;
  hi: number;
}

type Domains = Map<string, Dom>;

interface Budget {
  nodes: number;
  readonly maxNodes: number;
  steps: number;
  readonly maxSteps: number;
}

class BudgetExceeded extends Error {}

const DEFAULT_MAX_NODES = 100_000;
const DEFAULT_MAX_PROP_STEPS = 5_000_000;

export function solve(
  variables: readonly SolverVariable[],
  constraints: readonly SolverConstraint[],
  options: SolveOptions = {},
): SolveResult {
  checkReferences(variables, constraints);
  const maxNodes = options.maxNodes ?? DEFAULT_MAX_NODES;
  const maxPropSteps = options.maxPropSteps ?? DEFAULT_MAX_PROP_STEPS;
  const budget: Budget = { nodes: 0, maxNodes, steps: 0, maxSteps: maxPropSteps };
  let found: Domains | null;
  try {
    found = search(initialDomains(variables), constraints, budget);
  } catch (error) {
    if (error instanceof BudgetExceeded) return { status: "budget-exceeded", nodes: budget.nodes };
    throw error;
  }
  if (found !== null) {
    const model: Record<string, number> = {};
    for (const [name, dom] of found) model[name] = dom.lo;
    if (!evaluateModel(constraints, model)) {
      throw new Error("internal error: solver model fails direct evaluation");
    }
    return { status: "sat", model, nodes: budget.nodes };
  }
  const conflict =
    options.minimizeConflict === false
      ? constraints.map((c) => c.id)
      : minimizeConflict(variables, constraints, maxNodes, maxPropSteps);
  return { status: "unsat", conflict, nodes: budget.nodes };
}

/** Direct evaluation of constraints against a full assignment (no propagation). */
export function evaluateModel(
  constraints: readonly SolverConstraint[],
  model: Readonly<Record<string, number>>,
): boolean {
  return constraints.every((c) => evalConstraint(c, model));
}

function evalConstraint(c: SolverConstraint, model: Readonly<Record<string, number>>): boolean {
  if (c.kind === "when") {
    return evalConditionOnModel(c.cond, model) ? evalSimple(c.then, model) : true;
  }
  return evalSimple(c, model);
}

function evalSimple(c: SimpleConstraint, model: Readonly<Record<string, number>>): boolean {
  switch (c.kind) {
    case "eqConst":
      return model[c.a] === c.value;
    case "eqVar":
      return model[c.a] === model[c.b];
    case "diff":
      return model[c.a] - model[c.b] <= c.c;
    case "sum": {
      const total = c.vars.reduce((acc, name) => acc + model[name], 0);
      if (c.op === "le") return total <= c.value;
      if (c.op === "ge") return total >= c.value;
      return total === c.value;
    }
  }
}

function evalConditionOnModel(cond: SolverCondition, model: Readonly<Record<string, number>>): boolean {
  const a = model[cond.a];
  const b = typeof cond.b === "number" ? cond.b : model[cond.b];
  switch (cond.op) {
    case "le":
      return a <= b;
    case "lt":
      return a < b;
    case "ge":
      return a >= b;
    case "gt":
      return a > b;
    case "eq":
      return a === b;
    case "ne":
      return a !== b;
  }
}

function initialDomains(variables: readonly SolverVariable[]): Domains {
  const domains: Domains = new Map();
  for (const v of variables) domains.set(v.name, { lo: v.lo, hi: v.hi });
  return domains;
}

function cloneDomains(domains: Domains): Domains {
  const copy: Domains = new Map();
  for (const [name, dom] of domains) copy.set(name, { lo: dom.lo, hi: dom.hi });
  return copy;
}

function search(domains: Domains, constraints: readonly SolverConstraint[], budget: Budget): Domains | null {
  budget.nodes++;
  if (budget.nodes > budget.maxNodes) throw new BudgetExceeded();
  if (!propagate(domains, constraints, budget)) return null;
  let pick: string | null = null;
  let pickSize = Number.POSITIVE_INFINITY;
  for (const [name, dom] of domains) {
    const size = dom.hi - dom.lo;
    if (size > 0 && size < pickSize) {
      pick = name;
      pickSize = size;
    }
  }
  if (pick === null) return domains;
  const dom = domains.get(pick)!;
  for (let value = dom.lo; value <= dom.hi; value++) {
    const child = cloneDomains(domains);
    const childDom = child.get(pick)!;
    childDom.lo = value;
    childDom.hi = value;
    const result = search(child, constraints, budget);
    if (result !== null) return result;
  }
  return null;
}

type Status = "fail" | "tightened" | "none";

function merge(a: Status, b: Status): Status {
  if (a === "fail" || b === "fail") return "fail";
  if (a === "tightened" || b === "tightened") return "tightened";
  return "none";
}

function tightenLo(dom: Dom, lo: number): Status {
  if (lo > dom.hi) return "fail";
  if (lo > dom.lo) {
    dom.lo = lo;
    return "tightened";
  }
  return "none";
}

function tightenHi(dom: Dom, hi: number): Status {
  if (hi < dom.lo) return "fail";
  if (hi < dom.hi) {
    dom.hi = hi;
    return "tightened";
  }
  return "none";
}

function propagate(domains: Domains, constraints: readonly SolverConstraint[], budget: Budget): boolean {
  for (const dom of domains.values()) {
    if (dom.lo > dom.hi) return false;
  }
  let changed = true;
  while (changed) {
    changed = false;
    for (const c of constraints) {
      budget.steps++;
      if (budget.steps > budget.maxSteps) throw new BudgetExceeded();
      const status = applyConstraint(domains, c);
      if (status === "fail") return false;
      if (status === "tightened") changed = true;
    }
  }
  return true;
}

function applyConstraint(domains: Domains, c: SolverConstraint): Status {
  if (c.kind === "when") {
    const status = conditionStatus(c.cond, domains);
    // A false or undecided condition imposes nothing; search will decide it.
    if (status !== "true") return "none";
    return applySimple(domains, c.then);
  }
  return applySimple(domains, c);
}

function applySimple(domains: Domains, c: SimpleConstraint): Status {
  switch (c.kind) {
    case "eqConst": {
      const dom = domains.get(c.a)!;
      return merge(tightenLo(dom, c.value), tightenHi(dom, c.value));
    }
    case "eqVar": {
      const x = domains.get(c.a)!;
      const y = domains.get(c.b)!;
      const lo = Math.max(x.lo, y.lo);
      const hi = Math.min(x.hi, y.hi);
      return merge(tightenLo(x, lo), merge(tightenHi(x, hi), merge(tightenLo(y, lo), tightenHi(y, hi))));
    }
    case "diff": {
      // x - y <= c  ⇒  x <= y + c  and  y >= x - c
      const x = domains.get(c.a)!;
      const y = domains.get(c.b)!;
      return merge(tightenHi(x, y.hi + c.c), tightenLo(y, x.lo - c.c));
    }
    case "sum": {
      const doms = c.vars.map((name) => domains.get(name)!);
      const sumLo = doms.reduce((acc, dom) => acc + dom.lo, 0);
      const sumHi = doms.reduce((acc, dom) => acc + dom.hi, 0);
      let status: Status = "none";
      if (c.op === "le" || c.op === "eq") {
        if (sumLo > c.value) return "fail";
        for (let i = 0; i < doms.length; i++) {
          status = merge(status, tightenHi(doms[i]!, c.value - (sumLo - doms[i]!.lo)));
        }
      }
      if (c.op === "ge" || c.op === "eq") {
        if (sumHi < c.value) return "fail";
        for (let i = 0; i < doms.length; i++) {
          status = merge(status, tightenLo(doms[i]!, c.value - (sumHi - doms[i]!.hi)));
        }
      }
      return status;
    }
  }
}

function conditionStatus(cond: SolverCondition, domains: Domains): "true" | "false" | "unknown" {
  const a = domains.get(cond.a)!;
  const b: Dom = typeof cond.b === "number" ? { lo: cond.b, hi: cond.b } : domains.get(cond.b)!;
  switch (cond.op) {
    case "le":
      if (a.hi <= b.lo) return "true";
      if (a.lo > b.hi) return "false";
      return "unknown";
    case "lt":
      if (a.hi < b.lo) return "true";
      if (a.lo >= b.hi) return "false";
      return "unknown";
    case "ge":
      if (a.lo >= b.hi) return "true";
      if (a.hi < b.lo) return "false";
      return "unknown";
    case "gt":
      if (a.lo > b.hi) return "true";
      if (a.hi <= b.lo) return "false";
      return "unknown";
    case "eq":
      if (a.lo === a.hi && b.lo === b.hi) return a.lo === b.lo ? "true" : "false";
      if (a.hi < b.lo || b.hi < a.lo) return "false";
      return "unknown";
    case "ne":
      if (a.hi < b.lo || b.hi < a.lo) return "true";
      if (a.lo === a.hi && b.lo === b.hi && a.lo === b.lo) return "false";
      return "unknown";
  }
}

/**
 * Subset-minimal conflict via deletion: repeatedly drop any constraint
 * whose removal keeps the set unsatisfiable. A trial that exhausts its
 * budget keeps the constraint (conservative). The result is 1-minimal:
 * removing any single remaining constraint makes it satisfiable.
 */
function minimizeConflict(
  variables: readonly SolverVariable[],
  constraints: readonly SolverConstraint[],
  maxNodes: number,
  maxPropSteps: number,
): string[] {
  let core = [...constraints];
  for (const candidate of [...core]) {
    const trial = core.filter((c) => c !== candidate);
    const budget: Budget = { nodes: 0, maxNodes, steps: 0, maxSteps: maxPropSteps };
    let result: Domains | null;
    try {
      result = search(initialDomains(variables), trial, budget);
    } catch (error) {
      if (error instanceof BudgetExceeded) continue;
      throw error;
    }
    if (result === null) core = trial;
  }
  return core.map((c) => c.id);
}

function constraintVars(c: SolverConstraint): string[] {
  switch (c.kind) {
    case "eqConst":
      return [c.a];
    case "eqVar":
    case "diff":
      return [c.a, c.b];
    case "sum":
      return [...c.vars];
    case "when": {
      const condVars = typeof c.cond.b === "string" ? [c.cond.a, c.cond.b] : [c.cond.a];
      return [...condVars, ...constraintVars(c.then)];
    }
  }
}

function checkReferences(variables: readonly SolverVariable[], constraints: readonly SolverConstraint[]): void {
  const names = new Set(variables.map((v) => v.name));
  if (names.size !== variables.length) throw new Error("duplicate variable names");
  const ids = new Set<string>();
  for (const c of constraints) {
    if (ids.has(c.id)) throw new Error(`duplicate constraint id "${c.id}"`);
    ids.add(c.id);
    for (const name of constraintVars(c)) {
      if (!names.has(name)) {
        throw new Error(`constraint "${c.id}" references unknown variable "${name}"`);
      }
    }
  }
}
