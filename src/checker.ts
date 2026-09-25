/**
 * One-operation check of a concrete (possibly partial) assignment.
 *
 * Three-valued outcome:
 *  - rejected:   a hard rule is violated (unknown entity, missing
 *                qualification, out-of-pool assignee, or an SoD collision
 *                with no exception covering the merged subject);
 *  - undecided:  at least one position is unassigned, or every valid reading
 *                needs more exception cost than the budget allows;
 *  - approved:   complete, qualified, in-pool and every collision is paid
 *                for within the budget.
 */
import type {
  Assignment,
  CheckResult,
  ExceptionUse,
  Problem,
  SodGroup,
  Violation,
} from "./model.ts";
import {
  IdentityResolver,
  lookupSubject,
  positionDomain,
} from "./policy.ts";
import { validateProblem } from "./validate.ts";
import { solveAssignment } from "./search.ts";

interface KeyedGroup {
  group: SodGroup;
  allowed: Set<string>;
  cheapest: Map<string, number>;
  exceptionIds: Map<string, string>;
}

export function checkAssignment(problem: Problem, rawAssignment: Assignment): CheckResult {
  const errors = validateProblem(problem);
  if (errors.length > 0) {
    return {
      verdict: "rejected",
      violations: [],
      unassigned: [],
      exceptionsUsed: [],
      exceptionCost: 0,
      reasons: errors.map((e) => `invalid problem: ${e}`),
    };
  }

  const resolver = new IdentityResolver(problem);
  const knownPositions = new Set(problem.positions.map((p) => p.id));
  const violations: Violation[] = [];
  const unassigned: string[] = [];
  const canonical: Assignment = {};
  const rejectedPositions = new Set<string>();

  for (const pid of Object.keys(rawAssignment)) {
    if (!knownPositions.has(pid)) {
      violations.push({ kind: "unknown-position", position: pid });
      rejectedPositions.add(pid);
      continue;
    }
    const raw = rawAssignment[pid] ?? null;
    if (raw === null) {
      unassigned.push(pid);
      canonical[pid] = null;
      continue;
    }
    const subject = lookupSubject(problem, resolver, raw);
    if (!subject) {
      violations.push({ kind: "unknown-subject", position: pid, subject: raw });
      rejectedPositions.add(pid);
      continue;
    }
    const position = problem.positions.find((p) => p.id === pid)!;
    const domain = positionDomain(problem, resolver, pid);
    if (!domain.includes(subject.id)) {
      const inDeclaredPool =
        position.candidates === undefined ||
        position.candidates.some((name) => resolver.resolve(name) === subject.id);
      violations.push({
        kind: inDeclaredPool ? "ineligible" : "outside-candidate-pool",
        position: pid,
        subject: subject.id,
      });
      canonical[pid] = subject.id;
      rejectedPositions.add(pid);
      continue;
    }
    canonical[pid] = subject.id;
  }
  // Positions the problem declares but the assignment map omits are simply
  // unassigned rather than a hard failure.
  for (const pid of problem.positions.map((p) => p.id)) {
    if (!(pid in canonical) && !rejectedPositions.has(pid)) {
      canonical[pid] = null;
      unassigned.push(pid);
    }
  }

  const groups = buildGroups(problem, resolver);
  const exceptionsUsed: ExceptionUse[] = [];
  let unfunded = false;
  for (const { group, allowed, cheapest, exceptionIds } of groups) {
    const holders = new Map<string, string[]>();
    for (const pid of group.positions) {
      const subject = canonical[pid];
      if (!subject) continue;
      const list = holders.get(subject) ?? [];
      list.push(pid);
      holders.set(subject, list);
    }
    for (const [subject, pids] of holders) {
      if (pids.length <= 1) continue;
      if (allowed.has(subject)) {
        exceptionsUsed.push({
          exceptionId: exceptionIds.get(subject)!,
          group: group.id,
          subject,
          cost: cheapest.get(subject)!,
          positions: [...pids],
        });
      } else {
        violations.push({
          kind: "sod-conflict",
          position: pids[0]!,
          subject,
          group: group.id,
          otherPositions: pids.slice(1),
        });
      }
    }
  }
  exceptionsUsed.sort((a, b) => a.exceptionId.localeCompare(b.exceptionId));
  const exceptionCost = exceptionsUsed.reduce((n, u) => n + u.cost, 0);
  const budget = problem.exceptionBudget ?? 0;
  if (exceptionCost > budget) unfunded = true;

  const reasons: string[] = [];
  let verdict: CheckResult["verdict"];
  let extendable: boolean | undefined;
  let witness: Assignment | undefined;

  if (violations.length > 0) {
    verdict = "rejected";
    for (const v of violations) reasons.push(describeViolation(v));
  } else if (unassigned.length > 0) {
    // Partial assignment: decide whether it extends to a complete plan.
    const solved = solveAssignment(problem, canonical, resolver, { fixed: true });
    extendable = solved.status === "satisfied";
    witness = solved.status === "satisfied" && solved.assignment ? solved.assignment : undefined;
    if (extendable) {
      verdict = "undecided";
      reasons.push(`${unassigned.length} position(s) unassigned; a budget-respecting completion exists`);
    } else {
      verdict = "undecided";
      reasons.push(
        solved.status === "undecided"
          ? `${unassigned.length} position(s) unassigned; completion needs more exception budget than ${budget}`
          : `${unassigned.length} position(s) unassigned; partial values cannot be completed (${
              solved.conflictSet ? solved.conflictSet.join(", ") : "conflict"
            })`,
      );
    }
  } else if (unfunded) {
    verdict = "undecided";
    reasons.push(
      `exceptions cost ${exceptionCost} exceed budget ${budget}; budget exhausted, left undecided`,
    );
  } else {
    verdict = "approved";
    reasons.push(
      exceptionCost > 0
        ? `approved with ${exceptionsUsed.length} exception(s), cost ${exceptionCost}/${budget}`
        : "approved without exceptions",
    );
  }

  return {
    verdict,
    violations,
    unassigned,
    exceptionsUsed,
    exceptionCost,
    extendable,
    witness,
    reasons,
  };
}

function describeViolation(v: Violation): string {
  switch (v.kind) {
    case "unknown-position":
      return `unknown position ${v.position}`;
    case "unknown-subject":
      return `position ${v.position}: subject ${v.subject ?? "?"} is not a declared entity or alias`;
    case "ineligible":
      return `position ${v.position}: subject ${v.subject ?? "?"} does not meet the qualification`;
    case "outside-candidate-pool":
      return `position ${v.position}: subject ${v.subject ?? "?"} is not in the candidate pool`;
    case "sod-conflict":
      return `subject ${v.subject ?? "?"} holds ${[v.position, ...(v.otherPositions ?? [])].join(
        ", ",
      )} in group ${v.group ?? "?"} without an exception`;
  }
}

function buildGroups(problem: Problem, resolver: IdentityResolver): KeyedGroup[] {
  const out: KeyedGroup[] = [];
  for (const group of problem.sodGroups ?? []) {
    const allowed = new Set<string>();
    const cheapest = new Map<string, number>();
    const exceptionIds = new Map<string, string>();
    for (const ex of problem.exceptions ?? []) {
      if (ex.group !== group.id) continue;
      const canonical = resolver.resolve(ex.subject);
      if (canonical === null) continue;
      allowed.add(canonical);
      const cost = ex.cost ?? 0;
      const known = cheapest.get(canonical);
      if (known === undefined || cost < known) {
        cheapest.set(canonical, cost);
        exceptionIds.set(canonical, ex.id);
      }
    }
    out.push({ group, allowed, cheapest, exceptionIds });
  }
  return out;
}
