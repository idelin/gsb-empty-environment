/**
 * Independent verifier.
 *
 * Nothing here trusts a checker/solver verdict: it rebuilds the constraint
 * interpretation from the raw Problem and re-derives whether the reported
 * assignment and exception usage are actually valid. This is deliberately a
 * second, compact implementation of the semantics rather than a call back
 * into checker/search.
 */
import type { Assignment, CheckResult, Problem, SolveResult } from "./model.ts";
import { evalCondition, IdentityResolver } from "./policy.ts";
import { validateProblem } from "./validate.ts";

export interface Verification {
  ok: boolean;
  errors: string[];
}

export function verifyCheck(problem: Problem, assignment: Assignment, result: CheckResult): Verification {
  const errors: string[] = [];
  const push = (msg: string) => errors.push(msg);

  for (const err of validateProblem(problem)) push(`invalid problem: ${err}`);

  const resolver = new IdentityResolver(problem);
  const subjectById = new Map(problem.subjects.map((s) => [s.id, s]));
  const positionsById = new Map(problem.positions.map((p) => [p.id, p]));
  const budget = problem.exceptionBudget ?? 0;

  const resolved = new Map<string, string | null>();
  const hardViolations: string[] = [];

  for (const position of problem.positions) {
    const raw = assignment[position.id] ?? null;
    if (raw === null) {
      resolved.set(position.id, null);
      continue;
    }
    const canonical = resolver.resolve(raw);
    if (canonical === null || !subjectById.has(canonical)) {
      hardViolations.push(`${position.id}: unknown subject ${raw}`);
      resolved.set(position.id, null);
      continue;
    }
    const subject = subjectById.get(canonical)!;
    if (!evalCondition(position.required, subject.attrs ?? {})) {
      hardViolations.push(`${position.id}: ${canonical} unqualified`);
    }
    if (position.candidates) {
      const poolCanon = position.candidates
        .map((name) => resolver.resolve(name))
        .filter((x): x is string => x !== null);
      if (!poolCanon.includes(canonical)) {
        hardViolations.push(`${position.id}: ${canonical} outside candidate pool`);
      }
    }
    resolved.set(position.id, canonical);
  }

  const payableCollisions: Array<{ group: string; subject: string; positions: string[]; cost: number; id: string }> = [];
  for (const group of problem.sodGroups ?? []) {
    const who = new Map<string, string[]>();
    for (const pid of group.positions) {
      const sid = resolved.get(pid) ?? null;
      if (!sid) continue;
      who.set(sid, [...(who.get(sid) ?? []), pid]);
    }
    for (const [sid, pids] of who) {
      if (pids.length < 2) continue;
      const matching = (problem.exceptions ?? []).filter((e) => {
        return e.group === group.id && resolver.resolve(e.subject) === sid;
      });
      if (matching.length === 0) {
        hardViolations.push(`group ${group.id}: ${sid} holds ${pids.join(", ")} without exception`);
      } else {
        const cheapest = matching.reduce((a, b) => (a.cost ?? 0) <= (b.cost ?? 0) ? a : b);
        payableCollisions.push({
          group: group.id,
          subject: sid,
          positions: [...pids],
          cost: cheapest.cost ?? 0,
          id: cheapest.id,
        });
      }
    }
  }

  const complete = problem.positions.every((p) => resolved.get(p.id) !== null);
  const exceptionCost = payableCollisions.reduce((n, c) => n + c.cost, 0);

  // Re-derive expected verdict.
  let expected: CheckResult["verdict"];
  if (hardViolations.length > 0) {
    expected = "rejected";
  } else if (!complete || exceptionCost > budget) {
    expected = "undecided";
  } else {
    expected = "approved";
  }

  if (result.verdict !== expected) {
    push(`verdict mismatch: reported ${result.verdict}, expected ${expected}`);
  }
  if (result.exceptionCost !== exceptionCost) {
    push(`exception cost mismatch: reported ${result.exceptionCost}, recomputed ${exceptionCost}`);
  }
  const unassignedRecomputed = problem.positions
    .filter((p) => resolved.get(p.id) === null)
    .map((p) => p.id)
    .filter((pid) => !hardViolations.some((v) => v.startsWith(`${pid}:`)));
  if (JSON.stringify([...result.unassigned].sort()) !== JSON.stringify([...unassignedRecomputed].sort())) {
    push("unassigned list mismatch");
  }
  if (result.exceptionsUsed.length !== payableCollisions.length) {
    push("exceptionsUsed length mismatch");
  }
  for (const collision of payableCollisions) {
    const use = result.exceptionsUsed.find(
      (u) => u.group === collision.group && u.subject === collision.subject,
    );
    if (!use) {
      push(`missing exception use for ${collision.subject} in ${collision.group}`);
    } else if (use.cost !== collision.cost || use.exceptionId !== collision.id) {
      push(`exception use mismatch for ${collision.subject} in ${collision.group}`);
    }
  }
  // Witness must be a real completion within budget when claimed.
  if (result.extendable && result.witness) {
    const witnessCheck = verifyAssignmentStandalone(problem, result.witness, resolver, subjectById, positionsById);
    if (!witnessCheck.ok) push(`witness invalid: ${witnessCheck.errors.join("; ")}`);
  }
  if (result.extendable === true && !result.witness) {
    push("extendable=true but no witness assignment given");
  }
  return { ok: errors.length === 0, errors };
}

export function verifySolve(problem: Problem, previous: Assignment | undefined, result: SolveResult): Verification {
  const errors: string[] = [];
  const push = (msg: string) => errors.push(msg);
  const resolver = new IdentityResolver(problem);
  const subjectById = new Map(problem.subjects.map((s) => [s.id, s]));
  const positionsById = new Map(problem.positions.map((p) => [p.id, p]));
  const budget = problem.exceptionBudget ?? 0;

  if (result.status === "satisfied") {
    if (!result.assignment) return { ok: false, errors: ["satisfied but assignment is null"] };
    const v = verifyAssignmentStandalone(problem, result.assignment, resolver, subjectById, positionsById);
    if (!v.ok) push(`assignment invalid: ${v.errors.join("; ")}`);
    if (result.exceptionCost > budget) push("satisfied plan exceeds exception budget");

    // Changes counted against canonicalised previous assignment.
    if (previous) {
      let changes = 0;
      for (const position of problem.positions) {
        const before = previous[position.id] ?? null;
        const beforeCanon = before === null ? null : resolver.resolve(before);
        const after = result.assignment![position.id] ?? null;
        if (beforeCanon !== null && beforeCanon !== after) changes++;
      }
      if (changes !== result.changesFromPrevious) {
        push(`changes mismatch: reported ${result.changesFromPrevious}, recomputed ${changes}`);
      }
    } else if (result.changesFromPrevious !== 0) {
      push("changes must be zero when no previous assignment exists");
    }

    // Claimed costs must match the tables.
    const table = problem.costs ?? {};
    let cost = 0;
    for (const [pid, sid] of Object.entries(result.assignment)) {
      if (sid) cost += table[pid]?.[sid] ?? 0;
    }
    if (cost !== result.assignmentCost) push(`assignment cost mismatch: ${result.assignmentCost} vs ${cost}`);
  }

  if (result.status === "infeasible") {
    if (result.assignment) push("infeasible result must not carry an assignment");
    if (!result.conflictSet || result.conflictSet.length === 0) {
      push("infeasible result must name a non-empty minimum conflict set");
    } else {
      for (const pid of result.conflictSet) {
        if (!positionsById.has(pid)) push(`conflict set names unknown position ${pid}`);
      }
      // The reported set itself must be infeasible under hard constraints.
      if (!subsetIsHardInfeasible(problem, result.conflictSet, resolver, result.assignment)) {
        push("reported conflict set is actually feasible");
      }
    }
  }

  if (result.status === "undecided" && result.assignment) {
    push("undecided must not report an assignment as resolved");
  }
  return { ok: errors.length === 0, errors };
}

interface StandaloneContext {
  resolve: (n: string) => string | null;
  subjectById: Map<string, { id: string; attrs?: Record<string, string | number | boolean> }>;
  positionsById: Map<string, { id: string; required: unknown; candidates?: string[] }>;
}

function verifyAssignmentStandalone(
  problem: Problem,
  assignment: Assignment,
  resolver: IdentityResolver,
  subjectById: Map<string, (typeof problem.subjects)[number]>,
  positionsById: Map<string, (typeof problem.positions)[number]>,
): Verification {
  const ctx: StandaloneContext = {
    resolve: (n) => resolver.resolve(n),
    subjectById,
    positionsById,
  };
  void ctx;
  const errors: string[] = [];
  const resolved = new Map<string, string>();
  for (const position of problem.positions) {
    const raw = assignment[position.id];
    if (raw === undefined || raw === null) {
      errors.push(`position ${position.id} unassigned`);
      continue;
    }
    const sid = resolver.resolve(raw);
    if (!sid || !subjectById.has(sid)) {
      errors.push(`position ${position.id}: unknown subject ${raw}`);
      continue;
    }
    const subject = subjectById.get(sid)!;
    if (!evalCondition(position.required, subject.attrs ?? {})) {
      errors.push(`position ${position.id}: ${sid} unqualified`);
    }
    if (position.candidates) {
      const pool = position.candidates
        .map((n) => resolver.resolve(n))
        .filter((x): x is string => x !== null);
      if (!pool.includes(sid)) errors.push(`position ${position.id}: ${sid} outside pool`);
    }
    resolved.set(position.id, sid);
  }
  const budget = problem.exceptionBudget ?? 0;
  let cost = 0;
  for (const group of problem.sodGroups ?? []) {
    const who = new Map<string, string[]>();
    for (const pid of group.positions) {
      const sid = resolved.get(pid);
      if (sid) who.set(sid, [...(who.get(sid) ?? []), pid]);
    }
    for (const [sid, pids] of who) {
      if (pids.length < 2) continue;
      const matching = (problem.exceptions ?? []).filter(
        (e) => e.group === group.id && resolver.resolve(e.subject) === sid,
      );
      if (matching.length === 0) {
        errors.push(`group ${group.id}: ${sid} collision without exception`);
      } else {
        cost += matching.reduce((min, e) => Math.min(min, e.cost ?? 0), Infinity);
      }
    }
  }
  if (cost > budget) errors.push(`exception cost ${cost} exceeds budget ${budget}`);
  return { ok: errors.length === 0, errors };
}

/**
 * Hard-infeasibility of a subset, computed independently with a tiny
 * backtracking search (exceptions available, budgets ignored).
 */
function subsetIsHardInfeasible(
  problem: Problem,
  subset: string[],
  resolver: IdentityResolver,
  fixed: Assignment | null,
): boolean {
  const inSet = new Set(subset);
  const current = new Map<string, string>();
  for (const pid of subset) {
    const raw = fixed?.[pid];
    if (raw) {
      const sid = resolver.resolve(raw);
      if (!sid) return true;
      current.set(pid, sid);
    }
  }
  const open = subset.filter((pid) => !current.has(pid));

  const domainOf = (pid: string): string[] => {
    const position = problem.positions.find((p) => p.id === pid)!;
    const rawPool = position.candidates ?? problem.subjects.map((s) => s.id);
    const out: string[] = [];
    for (const name of rawPool) {
      const sid = resolver.resolve(name);
      if (!sid) continue;
      const subject = problem.subjects.find((s) => s.id === sid);
      if (!subject || !evalCondition(position.required, subject.attrs ?? {})) continue;
      if (!out.includes(sid)) out.push(sid);
    }
    return out;
  };
  const exceptionSubjects = (groupId: string) =>
    new Set(
      (problem.exceptions ?? [])
        .filter((e) => e.group === groupId)
        .map((e) => resolver.resolve(e.subject))
        .filter((x): x is string => x !== null),
    );

  const go = (remaining: string[]): boolean => {
    if (remaining.length === 0) return true;
    let pick = remaining[0]!;
    let pickDomain = domainOf(pick);
    for (const pid of remaining.slice(1)) {
      const d = domainOf(pid);
      if (d.length < pickDomain.length) {
        pick = pid;
        pickDomain = d;
      }
    }
    const rest = remaining.filter((p) => p !== pick);
    for (const sid of pickDomain) {
      let ok = true;
      for (const group of problem.sodGroups ?? []) {
        if (!group.positions.includes(pick)) continue;
        if (!group.positions.some((q) => inSet.has(q))) continue;
        const clash = group.positions.filter((q) => q !== pick && inSet.has(q) && current.get(q) === sid).length;
        if (clash > 0 && !exceptionSubjects(group.id).has(sid)) ok = false;
      }
      if (!ok) continue;
      current.set(pick, sid);
      if (go(rest)) {
        current.delete(pick);
        return true;
      }
      current.delete(pick);
    }
    return false;
  };

  return !go(open);
}
