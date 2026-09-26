/**
 * JSON serialization with two number modes:
 *  - "asis":      replay each number's original lexeme (keeps "-0", "1e+02")
 *  - "canonical": unique representation per value; combined with sorted
 *                 object keys this yields a canonical JSON text suitable
 *                 for signing (same logical document => same bytes).
 */

import { canonicalDecimal } from './decimal.ts';
import { isJsonNumber } from './incremental.ts';
import type { JsonValue } from './incremental.ts';

export type NumberMode = 'canonical' | 'asis';

export interface SerializeOptions {
  numberMode?: NumberMode;
  /** Defaults to true in canonical mode, false in as-is mode. */
  sortKeys?: boolean;
}

function escapeString(s: string): string {
  let out = '"';
  for (const ch of s) {
    const code = ch.codePointAt(0) as number;
    if (ch === '"') out += '\\"';
    else if (ch === '\\') out += '\\\\';
    else if (ch === '\b') out += '\\b';
    else if (ch === '\f') out += '\\f';
    else if (ch === '\n') out += '\\n';
    else if (ch === '\r') out += '\\r';
    else if (ch === '\t') out += '\\t';
    else if (code < 0x20) out += '\\u' + code.toString(16).padStart(4, '0');
    else out += ch;
  }
  return out + '"';
}

export function serializeJson(value: JsonValue, opts: SerializeOptions = {}): string {
  const mode: NumberMode = opts.numberMode ?? 'canonical';
  const sortKeys = opts.sortKeys ?? mode === 'canonical';
  const ser = (v: JsonValue): string => {
    if (v === null) return 'null';
    if (v === true) return 'true';
    if (v === false) return 'false';
    if (typeof v === 'string') return escapeString(v);
    if (Array.isArray(v)) return '[' + v.map(ser).join(',') + ']';
    if (isJsonNumber(v)) return mode === 'asis' ? v.decimal.raw : canonicalDecimal(v.decimal);
    let keys = Object.keys(v);
    if (sortKeys) keys = keys.sort();
    return '{' + keys.map((k) => escapeString(k) + ':' + ser(v[k])).join(',') + '}';
  };
  return ser(value);
}

/** Unique canonical text for a document: canonical numbers + sorted keys. */
export function canonicalJson(value: JsonValue): string {
  return serializeJson(value, { numberMode: 'canonical', sortKeys: true });
}
