import type { Group, Json, Schema } from './types.ts';
import { ABSENT, OTHER, Solver, type Assignment, type CellValue } from './solver.ts';
import { validate } from './validate.ts';
import { deepEqual, hasField } from './presence.ts';

export interface UnsatCore {
  rulePaths: string[];
  groups: Group[];
}

export function minimalUnsatCore(schema: Schema): UnsatCore | null {
  const groups = schema.groups ?? [];
  if (new Solver(schema).solve().status === 'sat') return null;
  const isUnsat = (idxs: number[]): boolean =>
    new Solver({ ...schema, groups: idxs.map((i) => groups[i]) }).solve().status === 'unsat';
  let core = groups.map((_, i) => i);
  for (const i of [...core]) {
    const trial = core.filter((j) => j !== i);
    if (trial.length > 0 && isUnsat(trial)) core = trial;
  }
  return {
    rulePaths: core.map((i) => `/groups/${i}`),
    groups: core.map((i) => groups[i]),
  };
}

export interface Correction {
  set: Record<string, Json>;
  unset: string[];
  size: number;
}

function otherPlaceholder(field: string, literals: CellValue[]): string {
  let candidate = `__other:${field}__`;
  let n = 0;
  while (literals.some((v) => typeof v !== 'symbol' && deepEqual(v, candidate))) {
    n += 1;
    candidate = `__other:${field}#${n}__`;
  }
  return candidate;
}

export function materialize(assignment: Assignment, solver: Solver): Record<string, Json> {
  const obj: Record<string, Json> = {};
  for (const [field, v] of assignment) {
    if (v === ABSENT) continue;
    if (v === OTHER) {
      const literals = solver.baseDomain(field).filter((x) => x !== ABSENT && x !== OTHER);
      obj[field] = otherPlaceholder(field, literals);
    } else {
      obj[field] = v as Json;
    }
  }
  return obj;
}

function currentCell(instance: Record<string, Json>, field: string, solver: Solver): CellValue {
  if (!hasField(instance, field)) return ABSENT;
  const value = instance[field];
  const literals = solver.baseDomain(field).filter((x) => x !== ABSENT && x !== OTHER);
  return literals.some((v) => deepEqual(v, value)) ? value : OTHER;
}

export function minimalCorrections(
  schema: Schema,
  instance: Record<string, Json>,
  limit = 32,
  maxSize?: number,
): Correction[] {
  const solver = new Solver(schema);
  const fields = solver.fields;
  const current = new Map<string, CellValue>(
    fields.map((f) => [f, currentCell(instance, f, solver)]),
  );
  const alternatives = new Map<string, CellValue[]>(
    fields.map((f) => [
      f,
      solver.baseDomain(f).filter((v) => {
        const cur = current.get(f)!;
        if (typeof v === 'symbol' || typeof cur === 'symbol') return v !== cur;
        return !deepEqual(v, cur);
      }),
    ]),
  );
  const movable = fields.filter((f) => alternatives.get(f)!.length > 0);
  const upper = maxSize ?? movable.length;

  const applyMoves = (moves: Array<[string, CellValue]>): Record<string, Json> => {
    const next: Record<string, Json> = { ...instance };
    for (const [field, v] of moves) {
      if (v === ABSENT) {
        delete next[field];
      } else if (v === OTHER) {
        const literals = solver.baseDomain(field).filter((x) => x !== ABSENT && x !== OTHER);
        next[field] = otherPlaceholder(field, literals);
      } else {
        next[field] = v as Json;
      }
    }
    return next;
  };

  const toCorrection = (moves: Array<[string, CellValue]>): Correction => {
    const set: Record<string, Json> = {};
    const unset: string[] = [];
    const next = applyMoves(moves);
    for (const [field, v] of moves) {
      if (v === ABSENT) unset.push(field);
      else set[field] = next[field];
    }
    return { set, unset: unset.sort(), size: moves.length };
  };

  for (let size = 0; size <= upper; size += 1) {
    const found: Correction[] = [];
    const moves: Array<[string, CellValue]> = [];
    const visit = (start: number, left: number): void => {
      if (found.length >= limit) return;
      if (left === 0) {
        if (validate(applyMoves(moves), schema).valid) found.push(toCorrection(moves));
        return;
      }
      for (let i = start; i < movable.length; i += 1) {
        const field = movable[i];
        for (const v of alternatives.get(field)!) {
          moves.push([field, v]);
          visit(i + 1, left - 1);
          moves.pop();
          if (found.length >= limit) return;
        }
      }
    };
    visit(0, size);
    if (found.length > 0) return found;
  }
  return [];
}
