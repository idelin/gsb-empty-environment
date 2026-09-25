import { evaluatePolicy } from './policy.ts';
import type { Problem, Subject, VerifyResult } from './types.ts';

/**
 * Independent verifier. It deliberately re-implements name resolution,
 * conflict detection and exception accounting instead of reusing solver
 * internals, so a solver bug cannot silently validate itself.
 */

function resolveNames(subjects: readonly Subject[]): Map<string, Subject> {
  const byName = new Map<string, Subject>();
  for (const subject of subjects) {
    for (const name of [subject.id, ...(subject.aliases ?? [])]) {
      const existing = byName.get(name);
      if (existing !== undefined && existing.id !== subject.id) {
        throw new Error(
          `ambiguous name "${name}": maps to both "${existing.id}" and "${subject.id}"`,
        );
      }
      byName.set(name, subject);
    }
  }
  return byName;
}

function* combos<T>(items: readonly T[], k: number): Generator<T[]> {
  if (k === 0) {
    yield [];
    return;
  }
  for (let i = 0; i <= items.length - k; i++) {
    for (const rest of combos(items.slice(i + 1), k - 1)) {
      yield [items[i] as T, ...rest];
    }
  }
}

/**
 * Checks a (possibly partial) assignment against a problem.
 * - `invalid`  — hard violations (unknown subject, unqualified holder,
 *                conflicting positions without exception, budget exceeded);
 * - `pending`  — no violations, but some positions are still unassigned;
 * - `valid`    — complete and compliant.
 */
export function verify(
  problem: Problem,
  assignment: Readonly<Record<string, string>>,
): VerifyResult {
  const byName = resolveNames(problem.subjects);
  const budget = problem.exceptionBudget ?? 0;
  const violations: string[] = [];
  const unassigned: string[] = [];
  const knownPositions = new Set(problem.positions.map((p) => p.id));

  const groups = new Map<string, string[]>();
  for (const position of problem.positions) {
    const name = assignment[position.id];
    if (name === undefined) {
      unassigned.push(position.id);
      continue;
    }
    const subject = byName.get(name);
    if (subject === undefined) {
      violations.push(`position "${position.id}": unknown subject "${name}"`);
      continue;
    }
    if (position.requires && !evaluatePolicy(position.requires, subject.attributes ?? {})) {
      violations.push(
        `position "${position.id}": subject "${subject.id}" does not satisfy the qualification policy`,
      );
    }
    const group = groups.get(subject.id) ?? [];
    group.push(position.id);
    groups.set(subject.id, group);
  }
  for (const key of Object.keys(assignment)) {
    if (!knownPositions.has(key)) {
      violations.push(`assignment mentions unknown position "${key}"`);
    }
  }

  const exceptions = (problem.exceptions ?? []).map((exception) => {
    const subject = byName.get(exception.subject);
    if (subject === undefined) {
      throw new Error(
        `exception "${exception.id}" references unknown subject "${exception.subject}"`,
      );
    }
    return { id: exception.id, positions: new Set(exception.positions), subjectId: subject.id };
  });

  const pairCovers: string[][] = [];
  for (const [subjectId, positions] of groups) {
    for (let i = 0; i < positions.length; i++) {
      for (let j = i + 1; j < positions.length; j++) {
        const a = positions[i] as string;
        const b = positions[j] as string;
        const covering = exceptions
          .filter(
            (e) => e.subjectId === subjectId && e.positions.has(a) && e.positions.has(b),
          )
          .map((e) => e.id);
        if (covering.length === 0) {
          violations.push(
            `positions "${a}" and "${b}" are both held by subject "${subjectId}" with no covering exception`,
          );
        } else {
          pairCovers.push(covering);
        }
      }
    }
  }

  let exceptionsUsed: string[] = [];
  if (pairCovers.length > 0 && violations.length === 0) {
    const candidateIds = [...new Set(pairCovers.flat())];
    let found: string[] | null = null;
    for (let k = 0; k <= candidateIds.length && found === null; k++) {
      for (const combo of combos(candidateIds, k)) {
        const chosen = new Set(combo);
        if (pairCovers.every((covering) => covering.some((id) => chosen.has(id)))) {
          found = [...combo].sort();
          break;
        }
      }
    }
    exceptionsUsed = found ?? [];
  }
  if (exceptionsUsed.length > budget) {
    violations.push(
      `exception budget exceeded: ${exceptionsUsed.length} exception(s) used ` +
        `(${exceptionsUsed.join(', ')}), budget is ${budget}`,
    );
  }

  if (violations.length > 0) return { status: 'invalid', violations };
  if (unassigned.length > 0) return { status: 'pending', unassigned };
  return { status: 'valid', exceptionsUsed };
}
