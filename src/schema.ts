// Schema parsing and validation. Anything the checker cannot reason about
// (unknown keywords, recursive references, missing length bounds, defaults
// on required fields, invalid defaults, out-of-domain ranges) is reported
// as unsupported -- never silently treated as compatible.

import type { Field, RecordType, TypeNode, Value } from './types.ts';
import { validateValue } from './values.ts';

export type ParseResult = { ok: true; schema: RecordType } | { ok: false; reason: string };

const I64_MIN = -(2n ** 63n);
const I64_MAX = 2n ** 63n - 1n;
const U64_MIN = 0n;
const U64_MAX = 2n ** 64n - 1n;
const MAX_LEN_CAP = 1_000_000;

export function parseSchema(json: unknown): ParseResult {
  const t = parseType(json, '');
  if (!t.ok) return t;
  if (t.type.kind !== 'record') return { ok: false, reason: 'top-level schema must be a record' };
  return { ok: true, schema: t.type };
}

type PT = { ok: true; type: TypeNode } | { ok: false; reason: string };

const at = (path: string) => (path === '' ? 'root' : path);

function unsupportedKeys(obj: Record<string, unknown>, allowed: string[], path: string): string | null {
  for (const k of Object.keys(obj)) {
    if (!allowed.includes(k)) {
      if (k === '$ref' || k === 'ref' || k === '$recursiveRef' || k === 'recursiveAnchor') {
        return `recursive/named type references are unsupported (at ${at(path)})`;
      }
      return `unsupported keyword "${k}" (at ${at(path)})`;
    }
  }
  return null;
}

function parseType(node: unknown, path: string): PT {
  if (node === null || typeof node !== 'object' || Array.isArray(node)) {
    return { ok: false, reason: `type node must be an object (at ${at(path)})` };
  }
  const o = node as Record<string, unknown>;
  const kind = o.kind;
  if (typeof kind !== 'string') return { ok: false, reason: `missing "kind" (at ${at(path)})` };
  switch (kind) {
    case 'bool': {
      const u = unsupportedKeys(o, ['kind'], path);
      if (u) return { ok: false, reason: u };
      return { ok: true, type: { kind: 'bool' } };
    }
    case 'int64':
    case 'uint64': {
      const u = unsupportedKeys(o, ['kind', 'min', 'max'], path);
      if (u) return { ok: false, reason: u };
      const lo = parseIntLiteral(o.min);
      const hi = parseIntLiteral(o.max);
      if (lo === null || hi === null) {
        return { ok: false, reason: `${kind} requires explicit integer "min" and "max" (at ${at(path)})` };
      }
      const [dLo, dHi] = kind === 'int64' ? [I64_MIN, I64_MAX] : [U64_MIN, U64_MAX];
      if (lo < dLo || hi > dHi) return { ok: false, reason: `range [${lo}, ${hi}] outside ${kind} domain (at ${at(path)})` };
      if (lo > hi) return { ok: false, reason: `min > max (at ${at(path)})` };
      return { ok: true, type: { kind, min: lo, max: hi } };
    }
    case 'string':
    case 'bytes': {
      const u = unsupportedKeys(o, ['kind', 'maxLen'], path);
      if (u) return { ok: false, reason: u };
      const m = parseLen(o.maxLen);
      if (m === null) {
        return { ok: false, reason: `${kind} requires explicit non-negative integer "maxLen" (at ${at(path)})` };
      }
      return { ok: true, type: { kind, maxLen: m } };
    }
    case 'array': {
      const u = unsupportedKeys(o, ['kind', 'element', 'maxLen'], path);
      if (u) return { ok: false, reason: u };
      const m = parseLen(o.maxLen);
      if (m === null) {
        return { ok: false, reason: `array requires explicit non-negative integer "maxLen" (at ${at(path)})` };
      }
      const el = parseType(o.element, path + '[]');
      if (!el.ok) return el;
      return { ok: true, type: { kind: 'array', element: el.type, maxLen: m } };
    }
    case 'record': {
      const u = unsupportedKeys(o, ['kind', 'fields'], path);
      if (u) return { ok: false, reason: u };
      if (!Array.isArray(o.fields)) return { ok: false, reason: `record requires a "fields" array (at ${at(path)})` };
      const seen = new Set<number>();
      const fields: Field[] = [];
      for (const fj of o.fields) {
        const f = parseField(fj, path);
        if (!f.ok) return f;
        if (seen.has(f.field.id)) return { ok: false, reason: `duplicate field id ${f.field.id} (at ${at(path)})` };
        seen.add(f.field.id);
        fields.push(f.field);
      }
      return { ok: true, type: { kind: 'record', fields } };
    }
    default:
      return { ok: false, reason: `unsupported type kind "${kind}" (at ${at(path)})` };
  }
}

type PF = { ok: true; field: Field } | { ok: false; reason: string };

function parseField(node: unknown, path: string): PF {
  if (node === null || typeof node !== 'object' || Array.isArray(node)) {
    return { ok: false, reason: `field must be an object (at ${at(path)})` };
  }
  const o = node as Record<string, unknown>;
  const u = unsupportedKeys(o, ['id', 'name', 'type', 'required', 'default'], path);
  if (u) return { ok: false, reason: u };
  const id = o.id;
  if (!Number.isSafeInteger(id) || (id as number) < 0) {
    return { ok: false, reason: `field id must be a non-negative safe integer (at ${at(path)})` };
  }
  if (typeof o.name !== 'string') return { ok: false, reason: `field ${id} requires a "name" string (display only)` };
  if (typeof o.required !== 'boolean') return { ok: false, reason: `field ${id} requires explicit boolean "required"` };
  const t = parseType(o.type, `${at(path)}.${id}`);
  if (!t.ok) return t;
  const hasDefault = 'default' in o;
  if (hasDefault && o.required === true) {
    return { ok: false, reason: `field ${id}: "default" on a required field is unsupported` };
  }
  let defaultValue: Value | undefined;
  if (hasDefault) {
    const v = validateValue(t.type, o.default, `${at(path)}.${id}.default`);
    if (!v.ok) return { ok: false, reason: `field ${id}: default does not validate against its type: ${v.reason}` };
    defaultValue = v.value;
  }
  return {
    ok: true,
    field: { id: id as number, name: o.name, required: o.required, hasDefault, defaultValue, type: t.type },
  };
}

function parseIntLiteral(x: unknown): bigint | null {
  if (typeof x === 'number') return Number.isSafeInteger(x) ? BigInt(x) : null;
  if (typeof x === 'string' && /^-?\d+$/.test(x)) return BigInt(x);
  return null;
}

function parseLen(x: unknown): number | null {
  return Number.isSafeInteger(x) && (x as number) >= 0 && (x as number) <= MAX_LEN_CAP ? (x as number) : null;
}
