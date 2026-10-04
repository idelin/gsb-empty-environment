// Value validation/normalization and minimal-value generators used to
// construct minimal counterexamples.

import { Rec } from './types.ts';
import type { RecordType, TypeNode, Value } from './types.ts';
import { base64Decode } from './canonical.ts';

export type Validation = { ok: true; value: Value } | { ok: false; path: string; reason: string };

const fail = (path: string, reason: string): { ok: false; path: string; reason: string } => ({
  ok: false,
  path,
  reason,
});

const ID_KEY = /^\d+$/;

function isPlainObject(x: unknown): x is Record<string, unknown> {
  if (x === null || typeof x !== 'object' || x instanceof Rec || x instanceof Uint8Array || Array.isArray(x)) {
    return false;
  }
  const p = Object.getPrototypeOf(x);
  return p === Object.prototype || p === null;
}

function toBigInt(raw: unknown): bigint | null {
  if (typeof raw === 'bigint') return raw;
  if (typeof raw === 'number') return Number.isSafeInteger(raw) ? BigInt(raw) : null;
  if (typeof raw === 'string' && /^-?\d+$/.test(raw)) return BigInt(raw);
  return null;
}

// Validates a raw JSON value (or an already-normalized Value; validation is
// idempotent) against a type. Null is never a valid value: missing fields
// are represented by absence, not null. Record validation collects unknown
// keys into the Rec unknown bag instead of rejecting them.
export function validateValue(t: TypeNode, raw: unknown, path: string): Validation {
  switch (t.kind) {
    case 'bool':
      return typeof raw === 'boolean' ? { ok: true, value: raw } : fail(path, 'expected bool');
    case 'int64':
    case 'uint64': {
      const b = toBigInt(raw);
      if (b === null) return fail(path, `expected ${t.kind} integer`);
      if (b < t.min || b > t.max) return fail(path, `integer ${b} out of range [${t.min}, ${t.max}]`);
      return { ok: true, value: b };
    }
    case 'string': {
      if (typeof raw !== 'string') return fail(path, 'expected string');
      const cps = Array.from(raw);
      for (const ch of cps) {
        const cp = ch.codePointAt(0)!;
        if (cp >= 0xd800 && cp <= 0xdfff) return fail(path, 'invalid Unicode (lone surrogate)');
      }
      if (cps.length > t.maxLen) {
        return fail(path, `string length ${cps.length} code points exceeds maxLen ${t.maxLen}`);
      }
      return { ok: true, value: raw };
    }
    case 'bytes': {
      if (raw instanceof Uint8Array) {
        return raw.length <= t.maxLen
          ? { ok: true, value: raw }
          : fail(path, `bytes length ${raw.length} exceeds maxLen ${t.maxLen}`);
      }
      if (typeof raw !== 'string') return fail(path, 'expected canonical base64 string for bytes');
      const b = base64Decode(raw);
      if (b === null) return fail(path, 'invalid or non-canonical base64');
      if (b.length > t.maxLen) return fail(path, `bytes length ${b.length} exceeds maxLen ${t.maxLen}`);
      return { ok: true, value: b };
    }
    case 'array': {
      if (!Array.isArray(raw)) return fail(path, 'expected array');
      if (raw.length > t.maxLen) return fail(path, `array length ${raw.length} exceeds maxLen ${t.maxLen}`);
      const out: Value[] = [];
      for (let i = 0; i < raw.length; i++) {
        const r = validateValue(t.element, raw[i], path + '[]');
        if (!r.ok) return r;
        out.push(r.value);
      }
      return { ok: true, value: out };
    }
    case 'record': {
      const isRec = raw instanceof Rec;
      if (!isRec && !isPlainObject(raw)) {
        return fail(path, 'expected record object');
      }
      const src: Record<string, unknown> = isRec ? raw.fields : (raw as Record<string, unknown>);
      const unknown: Record<string, unknown> = isRec ? { ...raw.unknown } : {};
      const byId = new Map(t.fields.map((f) => [String(f.id), f]));
      const fields: Record<string, Value> = {};
      for (const [k, v] of Object.entries(src)) {
        const f = ID_KEY.test(k) && String(Number(k)) === k ? byId.get(k) : undefined;
        if (f === undefined) {
          unknown[k] = v; // unknown field: preserved, not rejected
          continue;
        }
        const r = validateValue(f.type, v, path === '' ? `.${k}` : `${path}.${k}`);
        if (!r.ok) return r;
        fields[k] = r.value;
      }
      for (const f of t.fields) {
        if (f.required && !(String(f.id) in fields)) {
          return fail(path, `missing required field ${f.id} (${f.name})`);
        }
      }
      return { ok: true, value: new Rec(fields, unknown) };
    }
  }
}

// ---- minimal values (by the canonical ordering) ----

export function minRecordValue(r: RecordType): Rec {
  const fields: Record<string, Value> = {};
  for (const f of r.fields) {
    if (f.required) fields[String(f.id)] = minValue(f.type);
  }
  return new Rec(fields, {});
}

export function minValue(t: TypeNode): Value {
  switch (t.kind) {
    case 'bool':
      return true; // "true" is 4 bytes, "false" is 5
    case 'int64':
    case 'uint64':
      return BigInt(minNumberString(t.min, t.max));
    case 'string':
      return '';
    case 'bytes':
      return new Uint8Array(0);
    case 'array':
      return [];
    case 'record':
      return minRecordValue(t);
  }
}

// Lexicographically smallest canonical decimal string of minimal length
// representing an integer in [lo, hi].
export function minNumberString(lo: bigint, hi: bigint): string {
  for (let len = 1; len <= 25; len++) {
    const s = lexMinNumberString(lo, hi, len);
    if (s !== null) return s;
  }
  throw new Error(`empty integer range [${lo}, ${hi}]`);
}

function lexMinNumberString(lo: bigint, hi: bigint, len: number): string | null {
  let prefix = '';
  for (let pos = 0; pos < len; pos++) {
    let chosen: string | null = null;
    for (const ch of candidateChars(prefix, pos, len)) {
      if (feasible(prefix + ch, len, lo, hi)) {
        chosen = ch;
        break;
      }
    }
    if (chosen === null) return null;
    prefix += chosen;
  }
  return prefix;
}

const DIGITS = ['0', '1', '2', '3', '4', '5', '6', '7', '8', '9'];

function candidateChars(prefix: string, pos: number, len: number): string[] {
  if (pos === 0) {
    // '-' (0x2D) sorts before digits; no leading zeros, no bare '-'.
    return len === 1 ? DIGITS : ['-', ...DIGITS.slice(1)];
  }
  if (pos === 1 && prefix === '-') return DIGITS.slice(1); // no "-0", no "-05"
  return DIGITS;
}

function feasible(prefix: string, len: number, lo: bigint, hi: bigint): boolean {
  const rem = BigInt(len - prefix.length);
  const pow = 10n ** rem;
  let a: bigint;
  let b: bigint;
  if (prefix.startsWith('-')) {
    const d = prefix.length === 1 ? 0n : BigInt(prefix.slice(1));
    a = -(d * pow + (pow - 1n));
    b = -(d * pow);
  } else {
    const d = BigInt(prefix);
    a = d * pow;
    b = d * pow + (pow - 1n);
  }
  return b >= lo && a <= hi;
}

// Minimal string of exactly n code points: U+0020 SPACE is the smallest
// code point that encodes to 1 UTF-8 byte and needs no JSON escaping.
export function minStringOfLen(n: number): string {
  return ' '.repeat(n);
}

// Sextet preference order by ascending code point of its base64 char:
// '+'(62) '/'(63) '0'-'9'(52-61) 'A'-'Z'(0-25) 'a'-'z'(26-51).
const B64_PREF: number[] = (() => {
  const order = [62, 63];
  for (let s = 52; s <= 61; s++) order.push(s);
  for (let s = 0; s <= 25; s++) order.push(s);
  for (let s = 26; s <= 51; s++) order.push(s);
  return order;
})();

// Minimal n-byte value: all n-byte values base64-encode to the same length,
// so minimize the base64 text lexicographically, sextet by sextet (the last
// sextet must keep its padding bits zero).
export function minBytesOfLen(n: number): Uint8Array {
  if (n === 0) return new Uint8Array(0);
  const k = Math.ceil((8 * n) / 6);
  const padding = 6 * k - 8 * n; // 0, 2 or 4
  const sextets: number[] = [];
  for (let i = 0; i < k; i++) {
    const isLast = i === k - 1 && padding > 0;
    const mod = 1 << padding;
    sextets.push(isLast ? B64_PREF.find((x) => x % mod === 0)! : B64_PREF[0]);
  }
  const out = new Uint8Array(n);
  let bitPos = 0;
  for (const s of sextets) {
    for (let b = 5; b >= 0; b--) {
      const bit = (s >> b) & 1;
      const byteIdx = bitPos >> 3;
      if (byteIdx < n) out[byteIdx] |= bit << (7 - (bitPos & 7));
      bitPos++;
    }
  }
  return out;
}
