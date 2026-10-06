import type { Type, TVar } from "./types.js";
import { showType, typeEquals, varName } from "./types.js";
import type { Subst } from "./subst.js";
import { applySubst, normalizeSubst } from "./subst.js";

export interface Constraint {
  left: Type;
  right: Type;
}

export interface SubstStep {
  variable: TVar;
  type: Type;
}

export interface OccursError {
  kind: "occurs";
  variable: TVar;
  chain: SubstStep[];
  message: string;
}

export interface MismatchError {
  kind: "mismatch";
  left: Type;
  right: Type;
  message: string;
}

export type UnifyError = OccursError | MismatchError;

export type UnifyResult =
  | { ok: true; subst: Subst }
  | { ok: false; error: UnifyError };

function resolveHead(subst: Subst, t: Type): Type {
  const seen = new Set<number>();
  let cur: Type = t;
  while (cur.kind === "var") {
    if (seen.has(cur.id)) return cur;
    seen.add(cur.id);
    const next = subst.get(cur.id);
    if (next === undefined) return cur;
    cur = next;
  }
  return cur;
}

function findPathToVar(subst: Subst, targetId: number, t: Type): SubstStep[] | null {
  if (t.kind === "var") {
    if (t.id === targetId) return [];
    const bound = subst.get(t.id);
    if (bound === undefined) return null;
    const sub = findPathToVar(subst, targetId, bound);
    if (sub === null) return null;
    const step: SubstStep = { variable: t, type: bound };
    const out: SubstStep[] = [step];
    for (const s of sub) out.push(s);
    return out;
  }
  if (t.kind === "fun") {
    const inParam = findPathToVar(subst, targetId, t.param);
    if (inParam !== null) return inParam;
    return findPathToVar(subst, targetId, t.result);
  }
  if (t.kind === "list") return findPathToVar(subst, targetId, t.element);
  return null;
}

function occursError(variable: TVar, chain: SubstStep[]): OccursError {
  const lines: string[] = [];
  lines.push("occurs check failed: cannot construct infinite type for variable " + varName(variable.id));
  lines.push("substitution chain leading to recursion:");
  for (const step of chain) {
    lines.push("  " + varName(step.variable.id) + " := " + showType(step.type));
  }
  const last = chain[chain.length - 1];
  lines.push("  => " + varName(last.variable.id) + " occurs in its own definition");
  return { kind: "occurs", variable, chain, message: lines.join("\n") };
}

function mismatchError(subst: Subst, l: Type, r: Type): MismatchError {
  const dl = applySubst(subst, l);
  const dr = applySubst(subst, r);
  const message = "type mismatch: cannot unify " + showType(dl) + " with " + showType(dr);
  return { kind: "mismatch", left: dl, right: dr, message };
}

function bindVar(subst: Subst, v: TVar, t: Type): UnifyError | null {
  const path = findPathToVar(subst, v.id, t);
  if (path !== null) {
    const chain: SubstStep[] = [];
    const attempted: SubstStep = { variable: v, type: t };
    chain.push(attempted);
    for (const step of path) chain.push(step);
    return occursError(v, chain);
  }
  subst.set(v.id, t);
  return null;
}

export function unify(constraints: Constraint[]): UnifyResult {
  const subst: Subst = new Map<number, Type>();
  const work: Constraint[] = [];
  for (const c of constraints) work.push(c);
  while (work.length > 0) {
    const current = work.pop();
    if (current === undefined) break;
    const l = resolveHead(subst, current.left);
    const r = resolveHead(subst, current.right);
    if (typeEquals(l, r)) continue;
    if (l.kind === "var") {
      const err = bindVar(subst, l, r);
      if (err !== null) return { ok: false, error: err };
      continue;
    }
    if (r.kind === "var") {
      const err = bindVar(subst, r, l);
      if (err !== null) return { ok: false, error: err };
      continue;
    }
    if (l.kind === "fun" && r.kind === "fun") {
      work.push({ left: r.param, right: l.param });
      work.push({ left: r.result, right: l.result });
      continue;
    }
    if (l.kind === "list" && r.kind === "list") {
      work.push({ left: l.element, right: r.element });
      continue;
    }
    return { ok: false, error: mismatchError(subst, l, r) };
  }
  return { ok: true, subst: normalizeSubst(subst) };
}
