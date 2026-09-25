import { canonicalize, type JsonValue } from "./json.ts";
import type { Condition, Constraint } from "./spec.ts";

/** Tri-state under a partial assignment. */
export type Tri = "true" | "false" | "unknown";

export type Assignment = ReadonlyMap<string, JsonValue>;

export function evalCondition(cond: Condition, assignment: Assignment): Tri {
  if ("param" in cond) {
    if (!assignment.has(cond.param)) return "unknown";
    const value = assignment.get(cond.param)!;
    if ("equals" in cond) {
      return canonicalize(value) === canonicalize(cond.equals) ? "true" : "false";
    }
    const hit = cond.in.some((v) => canonicalize(v) === canonicalize(value));
    return hit ? "true" : "false";
  }
  if ("not" in cond) {
    const inner = evalCondition(cond.not, assignment);
    return inner === "unknown" ? "unknown" : inner === "true" ? "false" : "true";
  }
  if ("all" in cond) {
    let unknown = false;
    for (const sub of cond.all) {
      const r = evalCondition(sub, assignment);
      if (r === "false") return "false";
      if (r === "unknown") unknown = true;
    }
    return unknown ? "unknown" : "true";
  }
  // any
  let unknown = false;
  for (const sub of cond.any) {
    const r = evalCondition(sub, assignment);
    if (r === "true") return "true";
    if (r === "unknown") unknown = true;
  }
  return unknown ? "unknown" : "false";
}

/** True only when the constraint is definitively violated. */
export function isViolated(constraint: Constraint, assignment: Assignment): boolean {
  if (constraint.kind === "implies") {
    return (
      evalCondition(constraint.if, assignment) === "true" &&
      evalCondition(constraint.then, assignment) === "false"
    );
  }
  return constraint.conditions.every(
    (cond) => evalCondition(cond, assignment) === "true",
  );
}

/**
 * One-step forward checking: with `assignment` fixed, compute for each
 * still-unassigned axis the values that do not immediately violate a
 * constraint. Returns null when some remaining domain becomes empty
 * (the whole subtree can be skipped).
 */
export function propagate(
  constraints: readonly Constraint[],
  assignment: Assignment,
  remaining: ReadonlyArray<{ name: string; domain: readonly JsonValue[] }>,
): Array<readonly JsonValue[]> | null {
  if (constraints.length === 0) {
    return remaining.map((axis) => axis.domain);
  }
  const pruned: Array<readonly JsonValue[]> = [];
  for (const axis of remaining) {
    const survivors: JsonValue[] = [];
    for (const value of axis.domain) {
      const trial = new Map(assignment);
      trial.set(axis.name, value);
      if (!constraints.some((c) => isViolated(c, trial))) {
        survivors.push(value);
      }
    }
    if (survivors.length === 0) return null;
    pruned.push(survivors);
  }
  return pruned;
}
