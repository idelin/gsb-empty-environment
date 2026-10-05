/**
 * Path feasibility over finite boolean conditions.
 *
 * Edge guards are conjunctions of `IDENT == LIT` / `IDENT != LIT`.
 * Every guarded variable has a finite domain: the set of literals it is
 * compared against anywhere in the graph. A branch is infeasible exactly
 * when the allowed-value set of some variable becomes empty, which lets
 * the analyzer exclude mutually contradictory branches.
 */

export type LitKey = string; // canonical: "b:true" | "n:42" | "s:hello"

export interface Clause {
  variable: string;
  op: "==" | "!=";
  literal: LitKey;
}

export function parseLiteral(text: string): LitKey {
  const t = text.trim();
  if (t === "true" || t === "false") return `b:${t}`;
  if (/^-?\d+(\.\d+)?$/.test(t)) return `n:${Number(t)}`;
  const dq = t.match(/^"(.*)"$/) ?? t.match(/^'(.*)'$/);
  if (dq) return `s:${dq[1]}`;
  throw new Error(`invalid literal: ${text}`);
}

export function formatLiteral(key: LitKey): string {
  const [kind, rest] = [key.slice(0, 1), key.slice(2)];
  if (kind === "b" || kind === "n") return rest;
  return JSON.stringify(rest);
}

const CLAUSE_RE = /^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(==|!=)\s*(true|false|-?\d+(?:\.\d+)?|"[^"]*"|'[^']*')\s*$/;

export function parseCondition(cond: string): Clause[] {
  const parts = cond.split("&&");
  const clauses: Clause[] = [];
  for (const part of parts) {
    const m = part.match(CLAUSE_RE);
    if (!m || m[1] === undefined || m[2] === undefined || m[3] === undefined) {
      throw new Error(`invalid condition clause: "${part.trim()}" (expected IDENT ==|!= LITERAL)`);
    }
    clauses.push({ variable: m[1], op: m[2] as "==" | "!=", literal: parseLiteral(m[3]) });
  }
  return clauses;
}

/** var -> finite domain (set of allowed literals at the start). */
export type Domains = Map<string, Set<LitKey>>;

export function collectDomains(conds: (string | undefined)[]): Domains {
  const domains: Domains = new Map();
  for (const cond of conds) {
    if (cond === undefined) continue;
    for (const clause of parseCondition(cond)) {
      let set = domains.get(clause.variable);
      if (!set) {
        set = new Set();
        domains.set(clause.variable, set);
      }
      set.add(clause.literal);
    }
  }
  return domains;
}

/** var -> currently allowed literals. Absent key means "full domain". */
export type CondState = Map<string, Set<LitKey>>;

export function cloneCondState(s: CondState): CondState {
  const out: CondState = new Map();
  for (const [k, v] of s) out.set(k, new Set(v));
  return out;
}

/**
 * Apply a guard; returns the refined state, or null if the guard
 * contradicts previously taken branches (infeasible path).
 */
export function refine(state: CondState, domains: Domains, cond: string | undefined): CondState | null {
  if (cond === undefined) return state;
  let next: CondState | null = null; // lazy copy
  for (const clause of parseCondition(cond)) {
    const cur = (next ?? state).get(clause.variable) ?? new Set(domains.get(clause.variable) ?? []);
    let refined: Set<LitKey>;
    if (clause.op === "==") {
      refined = cur.has(clause.literal) ? new Set([clause.literal]) : new Set();
    } else {
      refined = new Set(cur);
      refined.delete(clause.literal);
    }
    if (refined.size === 0) return null; // contradictory branches
    if (next === null) next = cloneCondState(state);
    next.set(clause.variable, refined);
  }
  return next ?? state;
}

/** Forget everything known about a variable (e.g. after it is assigned). */
export function killVariable(state: CondState, variable: string): CondState {
  if (!state.has(variable)) return state;
  const next = cloneCondState(state);
  next.delete(variable);
  return next;
}

export function condStateKey(state: CondState): string {
  const parts: string[] = [];
  const vars = [...state.keys()].sort();
  for (const v of vars) {
    const lits = [...(state.get(v) as Set<LitKey>)].sort();
    parts.push(`${v}={${lits.join(",")}}`);
  }
  return parts.join(";");
}
