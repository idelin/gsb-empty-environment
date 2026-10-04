// Message converter.
//
// - Maps fields by stable numeric ID (names are display-only).
// - Unknown fields are preserved in a separate `unknown` region keyed by
//   path; nothing is silently dropped. This covers both fields unknown
//   to the source schema and fields dropped by the target schema.
// - Missing is never treated as null (there is no null in this system):
//   absent fields get the target default if one exists, are omitted when
//   optional, and fail the conversion when required.
// - Validation and conversion of a whole message are atomic: any failure
//   yields an error result and no partial record is ever produced.

import type { Field, Schema, TypeNode, Value } from './types.ts';
import {
  jsonToValue,
  validateValue,
  valueToJson,
} from './value.ts';
import type { ValidationError } from './value.ts';

export interface ConvertOk {
  ok: true;
  data: unknown; // JSON rendering of the converted value (target schema)
  unknown: Record<string, unknown>; // preserved unknown fields, keyed by path
}

export interface ConvertErr {
  ok: false;
  stage: 'parse' | 'validate' | 'convert';
  errors: { path: string; message: string }[];
}

export type ConvertResult = ConvertOk | ConvertErr;

class ConvertError extends Error {
  path: string;
  constructor(path: string, message: string) {
    super(message);
    this.path = path;
  }
}

interface ConvertedValue {
  value: Value;
  unknown: [string, unknown][];
}

export function convertMessage(msg: unknown, from: Schema, to: Schema): ConvertResult {
  // 1. Parse against the source schema (collects unknown fields aside).
  let value: Value;
  try {
    value = jsonToValue(msg, from.root);
  } catch (e) {
    return { ok: false, stage: 'parse', errors: [{ path: '$', message: (e as Error).message }] };
  }

  // 2. Validate the whole message against the source schema.
  const errors: ValidationError[] = [];
  validateValue(value, from.root, '$', errors);
  if (errors.length > 0) return { ok: false, stage: 'validate', errors };

  // 3. Convert. Any failure aborts the whole conversion (atomic).
  try {
    const converted = convertValue(value, from.root, to.root, '$');
    return {
      ok: true,
      data: valueToJson(converted.value),
      unknown: Object.fromEntries(converted.unknown),
    };
  } catch (e) {
    if (e instanceof ConvertError) {
      return { ok: false, stage: 'convert', errors: [{ path: e.path, message: e.message }] };
    }
    throw e;
  }
}

function checkTarget(v: Value, toT: TypeNode, path: string): void {
  const errs: ValidationError[] = [];
  validateValue(v, toT, path, errs);
  if (errs.length > 0) throw new ConvertError(errs[0].path, errs[0].message);
}

function convertValue(v: Value, fromT: TypeNode, toT: TypeNode, path: string): ConvertedValue {
  if (fromT.kind === 'record' && toT.kind === 'record') {
    if (v.kind !== 'record') throw new ConvertError(path, `cannot convert ${v.kind} to record`);
    return convertRecord(v, fromT, toT, path);
  }
  if (fromT.kind === 'array' && toT.kind === 'array') {
    if (v.kind !== 'array') throw new ConvertError(path, `cannot convert ${v.kind} to array`);
    const items: Value[] = [];
    const unknown: [string, unknown][] = [];
    for (let i = 0; i < v.items.length; i++) {
      const c = convertValue(v.items[i], fromT.items, toT.items, `${path}[${i}]`);
      items.push(c.value);
      unknown.push(...c.unknown);
    }
    const out: Value = { kind: 'array', items };
    checkTarget(out, toT, path);
    return { value: out, unknown };
  }
  // Scalar conversion with target-side validation.
  const out = convertScalar(v, toT, path);
  checkTarget(out, toT, path);
  return { value: out, unknown: [] };
}

function convertScalar(v: Value, toT: TypeNode, path: string): Value {
  switch (toT.kind) {
    case 'bool':
      if (v.kind !== 'bool') throw new ConvertError(path, `cannot convert ${v.kind} to bool`);
      return v;
    case 'int64':
    case 'uint64':
      if (v.kind !== 'int') throw new ConvertError(path, `cannot convert ${v.kind} to ${toT.kind}`);
      return v;
    case 'string':
      if (v.kind !== 'string') throw new ConvertError(path, `cannot convert ${v.kind} to string`);
      return v;
    case 'bytes':
      if (v.kind !== 'bytes') throw new ConvertError(path, `cannot convert ${v.kind} to bytes`);
      return v;
    default:
      throw new ConvertError(path, `cannot convert ${v.kind} to ${toT.kind}`);
  }
}

function convertRecord(
  v: { kind: 'record'; fields: [number, Value][]; unknown: [string, unknown][] },
  fromT: { kind: 'record'; fields: Field[] },
  toT: { kind: 'record'; fields: Field[] },
  path: string,
): ConvertedValue {
  const fromById = new Map(fromT.fields.map((f) => [f.id, f]));
  const toById = new Map(toT.fields.map((f) => [f.id, f]));
  const present = new Map(v.fields);
  const outFields: [number, Value][] = [];
  const unknown: [string, unknown][] = [];

  // Carry over fields present in the message, mapped by field ID.
  for (const [id, val] of v.fields) {
    const fromField = fromById.get(id);
    const toField = toById.get(id);
    const key = `${path}/${id}`;
    if (!toField) {
      // Dropped by the target schema: preserve in the unknown region.
      unknown.push([key, valueToJson(val)]);
      continue;
    }
    const c = convertValue(val, fromField!.type, toField.type, key);
    outFields.push([id, c.value]);
    unknown.push(...c.unknown);
  }

  // Fields of the message the source schema itself does not know.
  for (const [key, raw] of v.unknown) {
    unknown.push([`${path}/${key}`, raw]);
  }

  // Fill target fields absent from the message.
  for (const toField of toT.fields) {
    if (present.has(toField.id)) continue;
    const key = `${path}/${toField.id}`;
    if (toField.hasDefault) {
      let dv: Value;
      try {
        dv = jsonToValue(toField.defaultValue, toField.type);
        checkTarget(dv, toField.type, key);
      } catch (e) {
        throw new ConvertError(key, `default is invalid: ${(e as Error).message}`);
      }
      outFields.push([toField.id, dv]);
    } else if (toField.required) {
      throw new ConvertError(key, `missing required field #${toField.id}`);
    }
    // optional without default: stays absent (missing is not null)
  }

  return { value: { kind: 'record', fields: outFields, unknown: [] }, unknown };
}
