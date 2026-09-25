import type { Problem } from "./model.ts";

function isNonNegInt(x: unknown): boolean {
  return typeof x === "number" && Number.isInteger(x) && x >= 0;
}

/** Structural validation. Returns human-readable error messages (empty = ok). */
export function validateProblem(problem: Problem): string[] {
  const errors: string[] = [];
  const subjectIds = new Set<string>();
  for (const s of problem.subjects) {
    if (subjectIds.has(s.id)) errors.push(`duplicate subject id: ${s.id}`);
    subjectIds.add(s.id);
  }

  for (const [alias, target] of Object.entries(problem.aliases ?? {})) {
    if (subjectIds.has(alias)) errors.push(`alias ${alias} shadows a canonical subject id`);
    if (target === alias) errors.push(`alias ${alias} maps to itself`);
  }

  const positionIds = new Set<string>();
  for (const p of problem.positions) {
    if (positionIds.has(p.id)) errors.push(`duplicate position id: ${p.id}`);
    positionIds.add(p.id);
  }

  for (const group of problem.sodGroups ?? []) {
    for (const pid of group.positions) {
      if (!positionIds.has(pid)) errors.push(`group ${group.id} references unknown position ${pid}`);
    }
  }

  const groupIds = new Set((problem.sodGroups ?? []).map((g) => g.id));
  for (const ex of problem.exceptions ?? []) {
    if (!groupIds.has(ex.group)) errors.push(`exception ${ex.id} references unknown group ${ex.group}`);
    if (ex.cost !== undefined && !isNonNegInt(ex.cost)) {
      errors.push(`exception ${ex.id} cost must be a non-negative integer`);
    }
  }

  if (problem.exceptionBudget !== undefined && !isNonNegInt(problem.exceptionBudget)) {
    errors.push("exceptionBudget must be a non-negative integer");
  }
  for (const [pid, row] of Object.entries(problem.costs ?? {})) {
    for (const [sid, c] of Object.entries(row)) {
      if (!isNonNegInt(c)) errors.push(`cost[${pid}][${sid}] must be a non-negative integer`);
    }
  }
  return errors;
}
