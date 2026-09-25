import type { Obligation, ObligationConflict } from './types.ts';

export interface MergeOutcome {
  obligations: Obligation[];
  conflicts: ObligationConflict[];
}

function keyOf(ob: Obligation): string {
  return ob.kind === 'quota' ? `quota:${ob.id}` : `mask:${ob.id}:${ob.field}`;
}

function mergePair(a: Obligation, b: Obligation): { merged?: Obligation; conflict?: ObligationConflict } {
  if (a.kind === 'quota' && b.kind === 'quota' && a.id === b.id) {
    return { merged: { kind: 'quota', id: a.id, limit: Math.min(a.limit, b.limit) } };
  }
  if (a.kind === 'mask' && b.kind === 'mask' && a.id === b.id && a.field === b.field) {
    if (a.format === b.format) return { merged: a };
    return {
      conflict: {
        reason: `incompatible mask formats for field "${a.field}": "${a.format}" vs "${b.format}"`,
        left: a,
        right: b,
      },
    };
  }
  return {};
}

export function mergeObligations(sets: readonly Obligation[][]): MergeOutcome {
  const byKey = new Map<string, Obligation>();
  const conflicts: ObligationConflict[] = [];
  for (const set of sets) {
    for (const ob of set) {
      const key = keyOf(ob);
      const existing = byKey.get(key);
      if (!existing) {
        byKey.set(key, ob);
        continue;
      }
      const { merged, conflict } = mergePair(existing, ob);
      if (conflict) conflicts.push(conflict);
      else if (merged) byKey.set(key, merged);
    }
  }
  return { obligations: [...byKey.values()], conflicts };
}
