// Field-level conflict resolution and lineage.
//
// Rules (deterministic, never arbitrary):
//  - each source holds at most one contribution per field (latest version wins);
//  - the effective value comes from the highest-priority tier;
//  - inside the top tier, distinct values from different sources make the
//    field PENDING (unresolved) instead of picking one;
//  - identical values inside the top tier collapse to the highest version,
//    then lowest source id for stable lineage.

import { jsonEquals } from './core.ts';
import type { Contribution, FieldState } from './core.ts';

export function newField(): FieldState {
  return { contributions: new Map(), effective: null, pending: null };
}

export function resolveField(field: FieldState, priorities: Map<string, number>): void {
  const contribs = [...field.contributions.values()];
  if (contribs.length === 0) {
    field.effective = null;
    field.pending = null;
    return;
  }
  let bestPriority = -Infinity;
  for (const c of contribs) {
    bestPriority = Math.max(bestPriority, priorities.get(c.source) ?? 0);
  }
  const top = contribs.filter((c) => (priorities.get(c.source) ?? 0) === bestPriority);
  const distinct: Contribution[] = [];
  for (const c of top) {
    if (!distinct.some((d) => jsonEquals(d.value, c.value))) distinct.push(c);
  }
  if (distinct.length > 1) {
    field.effective = null;
    field.pending = [...top].sort(compareContrib);
    return;
  }
  field.effective = [...top].sort(compareContrib)[0];
  field.pending = null;
}

function compareContrib(a: Contribution, b: Contribution): number {
  if (a.version !== b.version) return b.version - a.version;
  return a.source < b.source ? -1 : a.source > b.source ? 1 : 0;
}

export function fieldsHaveConflict(fields: Map<string, FieldState>): boolean {
  for (const f of fields.values()) {
    if (f.pending !== null) return true;
  }
  return false;
}
