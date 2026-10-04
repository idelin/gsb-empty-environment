// Value model: canonical JSON encoding, size metric, total order, and
// minimal-value construction.
//
// Counterexample ordering (per spec):
//   1. primary:   total UTF-8 byte count of the canonical JSON encoding
//   2. secondary: lexicographic order of canonical JSON, where string
//                 values compare by Unicode code point sequence
//
// Canonical JSON: object keys are decimal field IDs sorted as strings,
// no whitespace, minimal string escaping, ints in plain decimal, bytes
// as lowercase hex strings.

import type { Field, TypeNode, Value } from './types.ts';

const encoder = new TextEncoder();

export function utf8Bytes(s: string): number {
  return encoder.encode(s).length;
}

function escapeJsonString(s: string): string {
  let out = '"';
  for (const ch of s) {
    const cp = ch.codePointAt(0)!;
    if (ch === '"') out += '\\"';
    else if (ch === '\\') out += '\\\\';
    else if (cp < 0x20) out += '\\u' + cp.toString(16).padStart(4, '0');
    else out += ch;
  }
  return out + '"';
}

export function bytesToHex(b: Uint8Array): string {
  let s = '';
  for (const byte of b) s += byte.toString(16).padStart(2, '0');
  return s;
}

export function hexToBytes(s: string): Uint8Array {
  if (!/^(?:[0-9a-fA-F]{2})*$/.test(s)) throw new Error('expected even-length hex string for bytes');
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.slice(2 * i, 2 * i + 2), 16);
  return out;
}

export function canonicalJson(v: Value): string {
  switch (v.kind) {
    case 'bool':
      return v.value ? 'true' : 'false';
    case 'int':
      return v.value.toString();
    case 'string':
      return escapeJsonString(v.value);
    case 'bytes':
      return '"' + bytesToHex(v.value) + '"';
    case 'array':
      return '[' + v.items.map(canonicalJson).join(',') + ']';
    case 'record': {
      const entries: [string, string][] = v.fields.map(([id, val]) => [String(id), canonicalJson(val)]);
      for (const [key, raw] of v.unknown) entries.push([key, JSON.stringify(raw)]);
      entries.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
      return '{' + entries.map(([k, c]) => escapeJsonString(k) + ':' + c).join(',') + '}';
    }
  }
}

export function sizeOf(v: Value): number {
  return utf8Bytes(canonicalJson(v));
}

// ---------------------------------------------------------------------------
// Total order: (size, canonical-JSON lexicographic with code-point strings)
// ---------------------------------------------------------------------------

function codePoints(s: string): number[] {
  const out: number[] = [];
  for (const ch of s) out.push(ch.codePointAt(0)!);
  return out;
}

function compareCodePoints(a: string, b: string): number {
  const pa = codePoints(a);
  const pb = codePoints(b);
  const n = Math.min(pa.length, pb.length);
  for (let i = 0; i < n; i++) {
    if (pa[i] !== pb[i]) return pa[i] < pb[i] ? -1 : 1;
  }
  return pa.length - pb.length;
}

function compareText(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

// Rank mirrors the first character of the canonical JSON encoding:
// '"' (string/bytes) < '-'/'0'-'9' (int) < '[' (array) < 'f'/'t' (bool) < '{' (record)
function rank(v: Value): number {
  switch (v.kind) {
    case 'string':
    case 'bytes':
      return 0;
    case 'int':
      return 1;
    case 'array':
      return 2;
    case 'bool':
      return 3;
    case 'record':
      return 4;
  }
}

function lexCompare(a: Value, b: Value): number {
  const ra = rank(a);
  const rb = rank(b);
  if (ra !== rb) return ra - rb;
  switch (a.kind) {
    case 'string':
      return compareCodePoints(a.value, (b as { value: string }).value);
    case 'bytes': {
      const bContent = b.kind === 'bytes' ? bytesToHex(b.value) : (b as { value: string }).value;
      return compareCodePoints(bytesToHex(a.value), bContent);
    }
    case 'int':
      return compareText(a.value.toString(), (b as { value: bigint }).value.toString());
    case 'array': {
      const items = (b as { items: Value[] }).items;
      const n = Math.min(a.items.length, items.length);
      for (let i = 0; i < n; i++) {
        const c = lexCompare(a.items[i], items[i]);
        if (c !== 0) return c;
      }
      return a.items.length - items.length;
    }
    case 'bool':
      return Number(a.value) - Number((b as { value: boolean }).value);
    case 'record': {
      const ea = recordEntries(a);
      const eb = recordEntries(b as { kind: 'record'; fields: [number, Value][]; unknown: [string, unknown][] });
      const n = Math.min(ea.length, eb.length);
      for (let i = 0; i < n; i++) {
        const kc = compareText(ea[i][0], eb[i][0]);
        if (kc !== 0) return kc;
        const vc = lexCompare(ea[i][1], eb[i][1]);
        if (vc !== 0) return vc;
      }
      return ea.length - eb.length;
    }
  }
}

function recordEntries(v: { fields: [number, Value][]; unknown: [string, unknown][] }): [string, Value][] {
  const entries: [string, Value][] = v.fields.map(([id, val]) => [String(id), val] as [string, Value]);
  for (const [key, raw] of v.unknown) entries.push([key, rawJsonToValue(raw)]);
  entries.sort((a, b) => compareText(a[0], b[0]));
  return entries;
}

// Best-effort embedding of raw JSON (unknown fields) into the Value model
// for canonical encoding / comparison purposes only.
function rawJsonToValue(raw: unknown): Value {
  if (typeof raw === 'boolean') return { kind: 'bool', value: raw };
  if (typeof raw === 'number') return { kind: 'int', value: BigInt(Math.trunc(raw)) };
  if (typeof raw === 'string') return { kind: 'string', value: raw };
  if (Array.isArray(raw)) return { kind: 'array', items: raw.map(rawJsonToValue) };
  if (isRecord(raw)) {
    const fields: [number, Value][] = [];
    const unknown: [string, unknown][] = [];
    for (const [k, val] of Object.entries(raw)) unknown.push([k, val]);
    return { kind: 'record', fields, unknown };
  }
  return { kind: 'string', value: String(raw) };
}

function isRecord(x: unknown): x is Record<string, unknown> {
  return typeof x === 'object' && x !== null && !Array.isArray(x);
}

export function compareValues(a: Value, b: Value): number {
  const sa = sizeOf(a);
  const sb = sizeOf(b);
  if (sa !== sb) return sa - sb;
  return lexCompare(a, b);
}

// ---------------------------------------------------------------------------
// Minimal values
// ---------------------------------------------------------------------------

function pow10(n: bigint): bigint {
  return 10n ** n;
}

// Minimal integer of [lo, hi] under (canonical-JSON byte size, then
// lexicographic decimal text). Returns null when the range is empty.
export function minIntInRange(lo: bigint, hi: bigint): bigint | null {
  if (lo > hi) return null;
  for (let len = 1; len <= 21; len++) {
    // Negative candidates: '-' followed by (len-1) digits. '-' (0x2d) sorts
    // before any digit, so a negative candidate always beats a positive one
    // of the same text length.
    if (len >= 2) {
      const digits = BigInt(len - 1);
      const absLo = pow10(digits - 1n); // smallest abs with `digits` digits
      const absHi = pow10(digits) - 1n;
      const aLo = absLo > -hi ? absLo : -hi; // abs >= -hi  (value <= hi)
      const aHi = absHi < -lo ? absHi : -lo; // abs <= -lo  (value >= lo)
      if (aLo <= aHi) return -aLo;
    }
    // Non-negative candidates with exactly `len` digits.
    const pLo = len === 1 ? 0n : pow10(BigInt(len - 1));
    const pHi = pow10(BigInt(len)) - 1n;
    const cLo = pLo > lo ? pLo : lo;
    const cHi = pHi < hi ? pHi : hi;
    if (cLo <= cHi) return cLo;
  }
  return null;
}

export function minValue(t: TypeNode): Value {
  switch (t.kind) {
    case 'bool':
      return { kind: 'bool', value: false };
    case 'int64':
    case 'uint64':
      return { kind: 'int', value: minIntInRange(t.min, t.max)! };
    case 'string':
      return { kind: 'string', value: '' };
    case 'bytes':
      return { kind: 'bytes', value: new Uint8Array(0) };
    case 'array': {
      const item = minValue(t.items);
      return { kind: 'array', items: Array.from({ length: t.minLength }, () => item) };
    }
    case 'record': {
      const fields: [number, Value][] = [];
      for (const f of t.fields) {
        if (!absentAccepted(f)) fields.push([f.id, minValue(f.type)]);
      }
      return { kind: 'record', fields, unknown: [] };
    }
  }
}

// ---------------------------------------------------------------------------
// Presence semantics
// ---------------------------------------------------------------------------

// A field's absence is accepted iff:
//   - it has a default and that default is a valid value of its type, or
//   - it has no default and is not required.
// Missing is never treated as null; there is no null in this type system.
export function absentAccepted(f: Field): boolean {
  if (!f.hasDefault) return !f.required;
  try {
    return isValid(jsonToValue(f.defaultValue, f.type), f.type);
  } catch {
    return false;
  }
}

// ---------------------------------------------------------------------------
// JSON <-> Value
// ---------------------------------------------------------------------------

export function jsonToValue(json: unknown, t: TypeNode): Value {
  switch (t.kind) {
    case 'bool':
      if (typeof json !== 'boolean') throw new Error('expected boolean');
      return { kind: 'bool', value: json };
    case 'int64':
    case 'uint64': {
      let v: bigint;
      if (typeof json === 'number') {
        if (!Number.isInteger(json)) throw new Error('expected integer');
        v = BigInt(json);
      } else if (typeof json === 'string' && /^-?\d+$/.test(json)) {
        v = BigInt(json);
      } else {
        throw new Error('expected integer (number or decimal string)');
      }
      return { kind: 'int', value: v };
    }
    case 'string':
      if (typeof json !== 'string') throw new Error('expected string');
      return { kind: 'string', value: json };
    case 'bytes':
      if (typeof json === 'string') return { kind: 'bytes', value: hexToBytes(json) };
      if (Array.isArray(json) && json.every((x) => Number.isInteger(x) && x >= 0 && x <= 255)) {
        return { kind: 'bytes', value: Uint8Array.from(json as number[]) };
      }
      throw new Error('expected bytes (hex string or byte array)');
    case 'array':
      if (!Array.isArray(json)) throw new Error('expected array');
      return { kind: 'array', items: json.map((x) => jsonToValue(x, t.items)) };
    case 'record': {
      if (!isRecord(json)) throw new Error('expected object');
      const byId = new Map(t.fields.map((f) => [f.id, f]));
      const fields: [number, Value][] = [];
      const unknown: [string, unknown][] = [];
      const seenIds = new Set<number>();
      for (const [key, val] of Object.entries(json)) {
        if (!/^\d+$/.test(key)) {
          unknown.push([key, val]);
          continue;
        }
        const id = Number(key);
        const field = byId.get(id);
        if (!field) {
          unknown.push([key, val]);
          continue;
        }
        if (seenIds.has(id)) throw new Error(`duplicate field id ${id}`);
        seenIds.add(id);
        fields.push([id, jsonToValue(val, field.type)]);
      }
      return { kind: 'record', fields, unknown };
    }
  }
}

export function valueToJson(v: Value): unknown {
  switch (v.kind) {
    case 'bool':
      return v.value;
    case 'int':
      return v.value <= 9007199254740991n && v.value >= -9007199254740991n
        ? Number(v.value)
        : v.value.toString();
    case 'string':
      return v.value;
    case 'bytes':
      return bytesToHex(v.value);
    case 'array':
      return v.items.map(valueToJson);
    case 'record': {
      const out: Record<string, unknown> = {};
      for (const [id, val] of v.fields) out[String(id)] = valueToJson(val);
      for (const [key, raw] of v.unknown) out[key] = raw;
      return out;
    }
  }
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

export interface ValidationError {
  path: string;
  message: string;
}

export function validateValue(v: Value, t: TypeNode, path: string, errors: ValidationError[]): void {
  switch (t.kind) {
    case 'bool':
      if (v.kind !== 'bool') errors.push({ path, message: `expected bool, got ${v.kind}` });
      return;
    case 'int64':
    case 'uint64':
      if (v.kind !== 'int') {
        errors.push({ path, message: `expected ${t.kind}, got ${v.kind}` });
      } else if (v.value < t.min || v.value > t.max) {
        errors.push({ path, message: `integer ${v.value} outside [${t.min}, ${t.max}]` });
      }
      return;
    case 'string':
      if (v.kind !== 'string') {
        errors.push({ path, message: `expected string, got ${v.kind}` });
      } else if ([...v.value].length > t.maxLength) {
        errors.push({ path, message: `string length ${[...v.value].length} exceeds maxLength ${t.maxLength}` });
      }
      return;
    case 'bytes':
      if (v.kind !== 'bytes') {
        errors.push({ path, message: `expected bytes, got ${v.kind}` });
      } else if (v.value.length > t.maxLength) {
        errors.push({ path, message: `bytes length ${v.value.length} exceeds maxLength ${t.maxLength}` });
      }
      return;
    case 'array':
      if (v.kind !== 'array') {
        errors.push({ path, message: `expected array, got ${v.kind}` });
        return;
      }
      if (v.items.length < t.minLength || v.items.length > t.maxLength) {
        errors.push({
          path,
          message: `array length ${v.items.length} outside [${t.minLength}, ${t.maxLength}]`,
        });
      }
      v.items.forEach((item, i) => validateValue(item, t.items, `${path}[${i}]`, errors));
      return;
    case 'record': {
      if (v.kind !== 'record') {
        errors.push({ path, message: `expected record, got ${v.kind}` });
        return;
      }
      const byId = new Map(v.fields);
      for (const f of t.fields) {
        const val = byId.get(f.id);
        const label = f.name !== undefined ? `${f.name}(#${f.id})` : `#${f.id}`;
        if (val !== undefined) {
          validateValue(val, f.type, `${path}.${label}`, errors);
        } else if (!absentAccepted(f)) {
          errors.push({ path, message: `missing field ${label} (required, no valid default)` });
        }
      }
      return;
    }
  }
}

export function isValid(v: Value, t: TypeNode): boolean {
  const errors: ValidationError[] = [];
  validateValue(v, t, '$', errors);
  return errors.length === 0;
}
