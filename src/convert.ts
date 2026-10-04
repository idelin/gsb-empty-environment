// Message converter. Maps fields by numeric ID, preserves unknown fields
// (and fields dropped by the new schema) in a separate "unknown" area at
// every record level, never materializes defaults (missing is not null),
// and is atomic: validation and conversion either fully succeed or return
// an error with no partial record.

import { Rec } from './types.ts';
import type { RecordType, TypeNode, Value } from './types.ts';
import { validateValue } from './values.ts';
import { base64Encode } from './canonical.ts';

export interface ConvertedRecord {
  fields: Record<string, unknown>;
  unknown: Record<string, unknown>;
}

export type ConvertResult =
  | { ok: true; record: ConvertedRecord }
  | { ok: false; path: string; reason: string };

export function convertMessage(oldS: RecordType, newS: RecordType, message: unknown): ConvertResult {
  // Pass 1: validate against the old schema (unknown keys are collected,
  // not rejected). Any failure aborts before any output is produced.
  const v = validateValue(oldS, message, '');
  if (!v.ok) return { ok: false, path: v.path, reason: v.reason };
  // Pass 2: map onto the new schema. Any failure likewise aborts atomically.
  const mapped = mapRecord(oldS, newS, v.value as Rec, '');
  if (!mapped.ok) return mapped;
  return { ok: true, record: recToJson(mapped.record) };
}

type MapResult = { ok: true; record: Rec } | { ok: false; path: string; reason: string };

function mapRecord(oldR: RecordType, newR: RecordType, rec: Rec, path: string): MapResult {
  const oldById = new Map(oldR.fields.map((f) => [f.id, f]));
  const newById = new Map(newR.fields.map((f) => [f.id, f]));
  const fields: Record<string, Value> = {};
  const unknown: Record<string, unknown> = { ...rec.unknown };
  // Fields known to the old schema but dropped by the new one are preserved
  // in the unknown area rather than discarded.
  for (const [k, v] of Object.entries(rec.fields)) {
    if (!newById.has(Number(k))) unknown[k] = valueToJson(v);
  }
  for (const nf of newR.fields) {
    const key = String(nf.id);
    if (!(key in rec.fields)) {
      if (nf.required) {
        return { ok: false, path, reason: `missing required field ${nf.id} (${nf.name})` };
      }
      continue; // optional and absent: stays absent; defaults are not materialized
    }
    const of = oldById.get(nf.id)!;
    const mv = mapValue(of.type, nf.type, rec.fields[key], path === '' ? `.${key}` : `${path}.${key}`);
    if (!mv.ok) return mv;
    fields[key] = mv.value;
  }
  return { ok: true, record: new Rec(fields, unknown) };
}

type MV = { ok: true; value: Value } | { ok: false; path: string; reason: string };

function mapValue(oldT: TypeNode, newT: TypeNode, v: Value, path: string): MV {
  if (oldT.kind === 'record' && newT.kind === 'record') {
    const r = mapRecord(oldT, newT, v as Rec, path);
    return r.ok ? { ok: true, value: r.record } : r;
  }
  if (oldT.kind === 'array' && newT.kind === 'array') {
    const arr = v as Value[];
    if (arr.length > newT.maxLen) {
      return { ok: false, path, reason: `array length ${arr.length} exceeds maxLen ${newT.maxLen}` };
    }
    const out: Value[] = [];
    for (let i = 0; i < arr.length; i++) {
      const mv = mapValue(oldT.element, newT.element, arr[i], path + '[]');
      if (!mv.ok) return mv;
      out.push(mv.value);
    }
    return { ok: true, value: out };
  }
  if (oldT.kind === 'bytes' && newT.kind === 'string') {
    // Bytes are base64 strings at the message level; re-encode so the
    // value keeps its message-level representation under the string type.
    const r = validateValue(newT, base64Encode(v as Uint8Array), path);
    if (!r.ok) return { ok: false, path: r.path, reason: r.reason };
    return { ok: true, value: r.value };
  }
  const r = validateValue(newT, v, path);
  if (!r.ok) return { ok: false, path: r.path, reason: r.reason };
  return { ok: true, value: r.value };
}

// ---- JSON output ----

export function scalarToJson(v: Value): unknown {
  if (typeof v === 'boolean' || typeof v === 'string') return v;
  if (typeof v === 'bigint') return v >= -(2n ** 53n) && v <= 2n ** 53n - 1n ? Number(v) : v.toString();
  if (v instanceof Uint8Array) return base64Encode(v);
  throw new Error('not a scalar value');
}

// Converted-record form: { fields, unknown } at every record level.
export function valueToJson(v: Value): unknown {
  if (Array.isArray(v)) return v.map(valueToJson);
  if (v instanceof Rec) return recToJson(v);
  return scalarToJson(v);
}

function recToJson(r: Rec): ConvertedRecord {
  const fields: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(r.fields)) fields[k] = valueToJson(v);
  return { fields, unknown: r.unknown };
}

// Plain message form (used for counterexamples): id -> value, no unknown
// bag (checked messages never carry one).
export function messageToJson(v: Value): unknown {
  if (Array.isArray(v)) return v.map(messageToJson);
  if (v instanceof Rec) {
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v.fields)) out[k] = messageToJson(val);
    return out;
  }
  return scalarToJson(v);
}
