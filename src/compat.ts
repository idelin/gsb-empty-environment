/**
 * Compatibility check: is every record valid under `oldSchema` also valid
 * under `newSchema`? If not, produce a minimal concrete counterexample.
 *
 * Domains are finite at every decision point: enums are finite sets, and
 * array lengths only need probing at the boundary points oldMin and
 * newMax + 1, so nested objects/arrays are checked by bounded enumeration
 * rather than exhaustive search.
 */

import type { Field, JsonValue, ObjectSchema, Schema } from './schema.ts';
import { validate } from './schema.ts';

export type PathSegment = string | number;

export interface Incompatibility {
  /** Minimal record accepted by oldSchema but rejected by newSchema. */
  counterexample: JsonValue;
  /** Path to the offending position inside the counterexample. */
  path: PathSegment[];
  reason: string;
}

export type CompatResult =
  | { compatible: true }
  | ({ compatible: false } & Incompatibility);

/** Structural size of a value; used to pick the minimal counterexample. */
function sizeOf(value: JsonValue): number {
  if (Array.isArray(value)) {
    return 1 + value.reduce<number>((sum, item) => sum + sizeOf(item), 0);
  }
  if (typeof value === 'object' && value !== null) {
    return 1 + Object.values(value).reduce<number>((sum, item) => sum + sizeOf(item), 0);
  }
  return 1;
}

/** Smallest value accepted by a schema (shortest array, first enum value). */
function minimalValue(schema: Schema): JsonValue {
  switch (schema.kind) {
    case 'enum':
      if (schema.values.length === 0) {
        throw new Error('enum schema has an empty domain; no valid values exist');
      }
      return schema.values[0]!;
    case 'array': {
      const min = schema.minLength ?? 0;
      const item = minimalValue(schema.items);
      return Array.from({ length: min }, () => item);
    }
    case 'object': {
      const record: Record<string, JsonValue> = {};
      for (const [name, field] of Object.entries(schema.fields)) {
        if (field.required) record[name] = minimalFieldValue(field);
      }
      return record;
    }
  }
}

function minimalFieldValue(field: Field): JsonValue {
  return field.nullable ? null : minimalValue(field.schema);
}

/** Minimal record for an object schema with `overrides` applied. */
function minimalRecord(schema: ObjectSchema, overrides: Record<string, JsonValue>): JsonValue {
  const record = minimalValue(schema) as Record<string, JsonValue>;
  for (const [key, value] of Object.entries(overrides)) record[key] = value;
  return record;
}

interface Candidate extends Incompatibility {}

/**
 * Every value valid under `old` but invalid under `new` contains a witness
 * generated here; we emit one candidate per violation source and keep the
 * structurally smallest.
 */
function collectCounterexamples(
  oldSchema: Schema,
  newSchema: Schema,
  path: PathSegment[],
  out: Candidate[],
): void {
  if (oldSchema.kind !== newSchema.kind) {
    // Kinds differ: any minimal old value is rejected by new.
    out.push({
      counterexample: minimalValue(oldSchema),
      path,
      reason: `kind mismatch: old is "${oldSchema.kind}", new is "${newSchema.kind}"`,
    });
    return;
  }

  switch (oldSchema.kind) {
    case 'enum': {
      const next = newSchema as Extract<Schema, { kind: 'enum' }>;
      for (const value of oldSchema.values) {
        if (!next.values.includes(value)) {
          out.push({
            counterexample: value,
            path,
            reason: `enum value ${JSON.stringify(value)} not in new domain`,
          });
          return; // first missing value is the minimal one
        }
      }
      return;
    }

    case 'array': {
      const next = newSchema as Extract<Schema, { kind: 'array' }>;
      const oldMin = oldSchema.minLength ?? 0;
      const oldMax = oldSchema.maxLength ?? Infinity;
      const newMin = next.minLength ?? 0;
      const newMax = next.maxLength ?? Infinity;
      const item = minimalValue(oldSchema.items);
      const fill = (length: number): JsonValue[] =>
        Array.from({ length }, () => item);

      if (oldMin < newMin) {
        out.push({
          counterexample: fill(oldMin),
          path,
          reason: `array length ${oldMin} below new minLength ${newMin}`,
        });
      }
      if (oldMax > newMax) {
        // newMax is finite here; oldMin <= oldMax keeps the length old-valid.
        const length = Math.max(oldMin, newMax + 1);
        out.push({
          counterexample: fill(length),
          path,
          reason: `array length ${length} above new maxLength ${newMax}`,
        });
      }
      // Item violations are reachable only if old permits a non-empty array.
      if (oldMax >= 1) {
        const nested: Candidate[] = [];
        collectCounterexamples(oldSchema.items, next.items, [...path, 0], nested);
        for (const n of nested) {
          const length = Math.max(oldMin, 1);
          const arr = fill(length);
          arr[0] = n.counterexample;
          out.push({ ...n, counterexample: arr });
        }
      }
      return;
    }

    case 'object': {
      const next = newSchema as ObjectSchema;

      // 1. New required fields are never ignorable: old records may omit them.
      for (const [name, field] of Object.entries(next.fields)) {
        const oldField = oldSchema.fields[name];
        if (field.required && !oldField?.required) {
          out.push({
            counterexample: minimalRecord(oldSchema, {}),
            path: [...path, name],
            reason: `new required field "${name}" may be absent from old records`,
          });
        }
      }

      // 2. Keys old can produce but new does not declare (closed objects).
      for (const [name, field] of Object.entries(oldSchema.fields)) {
        if (!(name in next.fields)) {
          out.push({
            counterexample: minimalRecord(oldSchema, { [name]: minimalFieldValue(field) }),
            path: [...path, name],
            reason: `old field "${name}" is not declared in the new schema`,
          });
        }
      }

      // 3. Shared fields: nullability and nested structure.
      for (const [name, oldField] of Object.entries(oldSchema.fields)) {
        const newField = next.fields[name];
        if (!newField) continue;
        if (oldField.nullable && !newField.nullable) {
          out.push({
            counterexample: minimalRecord(oldSchema, { [name]: null }),
            path: [...path, name],
            reason: `old field "${name}" may be null but new schema forbids null`,
          });
        }
        const nested: Candidate[] = [];
        collectCounterexamples(oldField.schema, newField.schema, [...path, name], nested);
        for (const n of nested) {
          out.push({
            ...n,
            counterexample: minimalRecord(oldSchema, { [name]: n.counterexample }),
          });
        }
      }
      return;
    }
  }
}

/**
 * Returns `{ compatible: true }` when every old-valid record is new-valid,
 * otherwise the minimal counterexample with its path and reason.
 */
export function checkCompatibility(oldSchema: Schema, newSchema: Schema): CompatResult {
  const candidates: Candidate[] = [];
  collectCounterexamples(oldSchema, newSchema, [], candidates);
  if (candidates.length === 0) return { compatible: true };

  let best = candidates[0]!;
  for (const c of candidates) {
    if (sizeOf(c.counterexample) < sizeOf(best.counterexample)) best = c;
  }

  // Soundness guard: a reported counterexample must really separate the schemas.
  if (!validate(oldSchema, best.counterexample) || validate(newSchema, best.counterexample)) {
    throw new Error(`internal error: invalid counterexample ${JSON.stringify(best.counterexample)}`);
  }
  return { compatible: false, ...best };
}
