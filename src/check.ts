// Compatibility checker: decides whether the new schema accepts every
// value the old schema accepts (new >= old, value-set inclusion).
//
// Result is one of:
//   compatible   - proven inclusion
//   incompatible - with a concrete minimal counterexample: a value that
//                  satisfies the old schema and violates the new one,
//                  minimal under (canonical-JSON UTF-8 byte size, then
//                  canonical-JSON lexicographic order with strings
//                  compared by Unicode code points)
//   unsupported  - a rule the checker cannot reason about; never
//                  defaults to "compatible"

import { isNumeric } from './types.ts';
import type { Schema, TypeNode, Value } from './types.ts';
import { parseSchema } from './validate.ts';
import type { SchemaIssue } from './validate.ts';
import {
  absentAccepted,
  canonicalJson,
  compareValues,
  isValid,
  minIntInRange,
  minValue,
  sizeOf,
  valueToJson,
} from './value.ts';

export type PathSegment = { field: number; name?: string } | 'items';

export interface Counterexample {
  value: Value;
  reason: string;
  path: PathSegment[];
}

export type CheckResult =
  | { result: 'compatible' }
  | {
      result: 'incompatible';
      reason: string;
      path: PathSegment[];
      counterexample: unknown; // JSON rendering
      canonical: string;
      sizeBytes: number;
    }
  | { result: 'unsupported'; reasons: string[] };

export function checkSchemas(oldSchema: Schema, newSchema: Schema): CheckResult {
  const cex = findCounterexample(oldSchema.root, newSchema.root);
  if (!cex) return { result: 'compatible' };
  // Safety net: never emit a counterexample that does not actually
  // witness the incompatibility.
  if (!isValid(cex.value, oldSchema.root) || isValid(cex.value, newSchema.root)) {
    return {
      result: 'unsupported',
      reasons: ['internal: could not construct a valid counterexample for this schema pair'],
    };
  }
  return {
    result: 'incompatible',
    reason: cex.reason,
    path: cex.path,
    counterexample: valueToJson(cex.value),
    canonical: canonicalJson(cex.value),
    sizeBytes: sizeOf(cex.value),
  };
}

export function check(oldRaw: unknown, newRaw: unknown): CheckResult {
  const oldParsed = parseSchema(oldRaw);
  const newParsed = parseSchema(newRaw);
  const reasons: string[] = [];
  const collect = (which: string, issues: SchemaIssue[]): void => {
    for (const i of issues) {
      if (i.level === 'warning') continue;
      reasons.push(`${which} schema [${i.level}] ${i.path}: ${i.message}`);
    }
  };
  collect('old', oldParsed.issues);
  collect('new', newParsed.issues);
  if (reasons.length > 0 || !oldParsed.schema || !newParsed.schema) {
    return { result: 'unsupported', reasons };
  }
  return checkSchemas(oldParsed.schema, newParsed.schema);
}

// ---------------------------------------------------------------------------
// Counterexample search (returns null iff new accepts all old values)
// ---------------------------------------------------------------------------

export function findCounterexample(oldT: TypeNode, newT: TypeNode): Counterexample | null {
  // Numeric kinds are mutually comparable via their integer ranges.
  if (isNumeric(oldT) && isNumeric(newT)) {
    if (newT.min <= oldT.min && oldT.max <= newT.max) return null;
    const candidates: bigint[] = [];
    if (oldT.min < newT.min) {
      const hi = oldT.max < newT.min - 1n ? oldT.max : newT.min - 1n;
      const v = minIntInRange(oldT.min, hi);
      if (v !== null) candidates.push(v);
    }
    if (oldT.max > newT.max) {
      const lo = oldT.min > newT.max + 1n ? oldT.min : newT.max + 1n;
      const v = minIntInRange(lo, oldT.max);
      if (v !== null) candidates.push(v);
    }
    if (candidates.length === 0) {
      // Ranges overlap but neither side violation is constructible; this
      // cannot happen for integer intervals, so treat as unverifiable.
      throw new Error('internal: numeric incompatibility without witness');
    }
    const best = candidates
      .map((v) => ({ kind: 'int', value: v }) as Value)
      .reduce((a, b) => (compareValues(a, b) <= 0 ? a : b));
    return {
      value: best,
      reason: `numeric range [${oldT.min}, ${oldT.max}] (${oldT.kind}) not covered by [${newT.min}, ${newT.max}] (${newT.kind})`,
      path: [],
    };
  }

  if (oldT.kind !== newT.kind) {
    return {
      value: minValue(oldT),
      reason: `type kind changed from ${oldT.kind} to ${newT.kind}`,
      path: [],
    };
  }

  switch (oldT.kind) {
    case 'bool':
      return null;

    case 'string': {
      const n = newT as { kind: 'string'; maxLength: number };
      if (n.maxLength >= oldT.maxLength) return null;
      return {
        value: { kind: 'string', value: ' '.repeat(n.maxLength + 1) },
        reason: `string maxLength narrowed from ${oldT.maxLength} to ${n.maxLength}`,
        path: [],
      };
    }

    case 'bytes': {
      const n = newT as { kind: 'bytes'; maxLength: number };
      if (n.maxLength >= oldT.maxLength) return null;
      return {
        value: { kind: 'bytes', value: new Uint8Array(n.maxLength + 1) },
        reason: `bytes maxLength narrowed from ${oldT.maxLength} to ${n.maxLength}`,
        path: [],
      };
    }

    case 'array':
      return arrayCounterexample(oldT, newT as { kind: 'array'; items: TypeNode; minLength: number; maxLength: number });

    case 'record':
      return recordCounterexample(oldT, newT as { kind: 'record'; fields: typeof oldT.fields });
  }
}

function arrayCounterexample(
  oldT: { kind: 'array'; items: TypeNode; minLength: number; maxLength: number },
  newT: { kind: 'array'; items: TypeNode; minLength: number; maxLength: number },
): Counterexample | null {
  const candidates: Counterexample[] = [];
  const minItem = minValue(oldT.items);
  const arr = (len: number, item: Value): Value => ({
    kind: 'array',
    items: Array.from({ length: len }, () => item),
  });

  if (newT.minLength > oldT.minLength) {
    candidates.push({
      value: arr(oldT.minLength, minItem),
      reason: `array minLength raised from ${oldT.minLength} to ${newT.minLength}`,
      path: [],
    });
  }
  if (newT.maxLength < oldT.maxLength) {
    candidates.push({
      value: arr(newT.maxLength + 1, minItem),
      reason: `array maxLength narrowed from ${oldT.maxLength} to ${newT.maxLength}`,
      path: [],
    });
  }
  if (oldT.maxLength > 0) {
    const sub = findCounterexample(oldT.items, newT.items);
    if (sub) {
      const len = Math.max(oldT.minLength, 1);
      // Same size either way; place the offending item where it makes the
      // canonical JSON lexicographically smallest.
      const index = compareValues(sub.value, minItem) <= 0 ? 0 : len - 1;
      const items = Array.from({ length: len }, () => minItem);
      items[index] = sub.value;
      candidates.push({
        value: { kind: 'array', items },
        reason: sub.reason,
        path: ['items', ...sub.path],
      });
    }
  }
  return minCandidate(candidates);
}

function recordCounterexample(
  oldT: { kind: 'record'; fields: import('./types.ts').Field[] },
  newT: { kind: 'record'; fields: import('./types.ts').Field[] },
): Counterexample | null {
  const candidates: Counterexample[] = [];

  // Minimal old-valid record: omit every field whose absence is accepted.
  const baseFields = new Map<number, Value>();
  for (const f of oldT.fields) {
    if (!absentAccepted(f)) baseFields.set(f.id, minValue(f.type));
  }
  const base: Value = { kind: 'record', fields: [...baseFields], unknown: [] };

  const newById = new Map(newT.fields.map((f) => [f.id, f]));
  const oldIds = new Set(oldT.fields.map((f) => f.id));

  for (const f of oldT.fields) {
    const g = newById.get(f.id);
    if (!g) continue; // field dropped in new schema: data preserved as unknown, still accepted
    const seg: PathSegment = { field: g.id, ...(g.name !== undefined ? { name: g.name } : {}) };

    if (absentAccepted(f) && !absentAccepted(g)) {
      candidates.push({
        value: base,
        reason:
          `field ${fieldLabel(g)}: absence accepted by old schema but not by new ` +
          `(required without a valid default)`,
        path: [seg],
      });
    }

    const sub = findCounterexample(f.type, g.type);
    if (sub) {
      const fields = new Map(baseFields);
      fields.set(f.id, sub.value);
      candidates.push({
        value: { kind: 'record', fields: [...fields], unknown: [] },
        reason: sub.reason,
        path: [seg, ...sub.path],
      });
    }
  }

  for (const g of newT.fields) {
    if (oldIds.has(g.id)) continue;
    if (!absentAccepted(g)) {
      candidates.push({
        value: base,
        reason: `new field ${fieldLabel(g)} is required (no valid default) but absent from old schema`,
        path: [{ field: g.id, ...(g.name !== undefined ? { name: g.name } : {}) }],
      });
    }
  }

  return minCandidate(candidates);
}

function fieldLabel(f: { id: number; name?: string }): string {
  return f.name !== undefined ? `${f.name}(#${f.id})` : `#${f.id}`;
}

function minCandidate(candidates: Counterexample[]): Counterexample | null {
  if (candidates.length === 0) return null;
  return candidates.reduce((a, b) => (compareValues(a.value, b.value) <= 0 ? a : b));
}
