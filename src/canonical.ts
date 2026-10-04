// Canonical JSON encoding and the counterexample ordering:
//   1. total UTF-8 byte count of the canonical encoding (smaller wins)
//   2. lexicographic order of the canonical JSON text, compared as a
//      sequence of Unicode code points.

import { Rec } from './types.ts';
import type { Value } from './types.ts';

const encoder = new TextEncoder();

export function utf8Length(s: string): number {
  return encoder.encode(s).length;
}

export function compareCodePoints(a: string, b: string): number {
  const ac = Array.from(a);
  const bc = Array.from(b);
  const n = Math.min(ac.length, bc.length);
  for (let i = 0; i < n; i++) {
    if (ac[i] !== bc[i]) return ac[i].codePointAt(0)! - bc[i].codePointAt(0)!;
  }
  return ac.length - bc.length;
}

export function compareEncoded(a: string, b: string): number {
  const d = utf8Length(a) - utf8Length(b);
  if (d !== 0) return d;
  return compareCodePoints(a, b);
}

export function base64Encode(b: Uint8Array): string {
  return Buffer.from(b).toString('base64');
}

// Strict canonical base64 (standard alphabet, correct padding, round-trips).
export function base64Decode(s: string): Uint8Array | null {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(s)) return null;
  const buf = Buffer.from(s, 'base64');
  if (buf.toString('base64') !== s) return null;
  return new Uint8Array(buf);
}

// Canonical JSON: object keys sorted by code-point order, no whitespace,
// minimal string escaping (JSON.stringify), integers in plain decimal,
// bytes as canonical base64 strings. Record values encode their known
// fields (the unknown bag is a converter concern, never part of checked
// messages).
export function canonical(v: Value): string {
  if (typeof v === 'boolean') return v ? 'true' : 'false';
  if (typeof v === 'bigint') return v.toString();
  if (typeof v === 'string') return JSON.stringify(v);
  if (v instanceof Uint8Array) return JSON.stringify(base64Encode(v));
  if (Array.isArray(v)) return '[' + v.map(canonical).join(',') + ']';
  if (v instanceof Rec) {
    const keys = Object.keys(v.fields).sort(compareCodePoints);
    return '{' + keys.map((k) => JSON.stringify(k) + ':' + canonical(v.fields[k])).join(',') + '}';
  }
  throw new Error('not a normalized value');
}
