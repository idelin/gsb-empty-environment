/**
 * Attribute policy evaluation and identity resolution.
 *
 * The policy language is deliberately closed: only explicit attributes of
 * declared subjects are read, values belong to finite sets (or integers),
 * and the only connectives are boolean and/or/not. Nothing here performs I/O
 * or consults an external identity service.
 */
import type { AttrValue, Attrs, Condition, Problem } from "./model.ts";

export function evalCondition(cond: Condition, attrs: Attrs): boolean {
  switch (cond.kind) {
    case "always":
      return true;
    case "eq": {
      const v = attrs[cond.attr];
      return v !== undefined && v === cond.value;
    }
    case "in": {
      const v = attrs[cond.attr];
      return v !== undefined && cond.values.includes(v);
    }
    case "lte": {
      const v = attrs[cond.attr];
      return typeof v === "number" && v <= cond.value;
    }
    case "gte": {
      const v = attrs[cond.attr];
      return typeof v === "number" && v >= cond.value;
    }
    case "and":
      return cond.parts.every((p) => evalCondition(p, attrs));
    case "or":
      return cond.parts.some((p) => evalCondition(p, attrs));
    case "not":
      return !evalCondition(cond.part, attrs);
  }
}

/**
 * Canonical identity resolver. Alias chains are followed until a canonical
 * subject id is reached. Unknown names resolve to null. Alias cycles are
 * reported as null (fail closed) rather than looping forever.
 */
export class IdentityResolver {
  private readonly canon: Map<string, string>;
  private readonly canonicalIds: Set<string>;

  constructor(problem: Problem) {
    this.canon = new Map(Object.entries(problem.aliases ?? {}));
    this.canonicalIds = new Set(problem.subjects.map((s) => s.id));
  }

  /** Resolve a name (canonical id or alias) to its canonical subject id. */
  resolve(name: string): string | null {
    if (this.canonicalIds.has(name)) return name;
    const seen = new Set<string>();
    let cur = name;
    while (true) {
      if (this.canonicalIds.has(cur)) return cur;
      if (seen.has(cur)) return null;
      seen.add(cur);
      const next = this.canon.get(cur);
      if (next === undefined) return null;
      cur = next;
    }
  }

  /** Canonical ids of every declared subject. */
  subjects(): string[] {
    return [...this.canonicalIds];
  }
}

export interface ResolvedSubject {
  id: string;
  attrs: Attrs;
}

/**
 * Look up a subject by any name (alias included). Returns null when the name
 * denotes no declared entity.
 */
export function lookupSubject(problem: Problem, resolver: IdentityResolver, name: string): ResolvedSubject | null {
  const id = resolver.resolve(name);
  if (id === null) return null;
  const found = problem.subjects.find((s) => s.id === id);
  if (!found) return null;
  return { id, attrs: found.attrs ?? {} };
}

export function isQualified(subject: ResolvedSubject, required: Condition): boolean {
  return evalCondition(required, subject.attrs);
}

/**
 * Candidate domain of a position: the explicit pool (primary + substitutes)
 * when declared, otherwise every known subject; in both cases reduced to
 * qualified canonical ids.
 */
export function positionDomain(problem: Problem, resolver: IdentityResolver, positionId: string): string[] {
  const position = problem.positions.find((p) => p.id === positionId);
  if (!position) return [];
  const rawPool = position.candidates ?? problem.subjects.map((s) => s.id);
  const out: string[] = [];
  for (const name of rawPool) {
    const subject = lookupSubject(problem, resolver, name);
    if (!subject) continue;
    if (!isQualified(subject, position.required)) continue;
    if (!out.includes(subject.id)) out.push(subject.id);
  }
  return out;
}

export function assignmentCostOf(problem: Problem, assignment: Assignment): number {
  const table = problem.costs ?? {};
  let total = 0;
  for (const [positionId, subjectId] of Object.entries(assignment)) {
    if (subjectId === null) continue;
    total += table[positionId]?.[subjectId] ?? 0;
  }
  return total;
}

export type { AttrValue };
