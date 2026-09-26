/**
 * Serializer for the exact-decimal DOM.
 *
 * `numberMode: 'canonical'` emits the normalized unique spelling of each
 * number; `numberMode: 'original'` re-emits the exact lexeme found in the
 * source text (so -0, 1e+02 and padding zeroes round-trip byte-for-byte for
 * the number tokens themselves).
 */

import { Decimal, canonicalDecimal } from './decimal.ts';
import type { JsonValue } from './parser.ts';

export interface SerializeOptions {
  numberMode?: 'canonical' | 'original';
  /** Pretty-print with this many spaces per level; omitted = compact. */
  indent?: number;
}

export function serializeJson(value: JsonValue, options: SerializeOptions = {}): string {
  const numberMode = options.numberMode ?? 'canonical';
  const indent = options.indent ?? 0;
  return write(value, numberMode, indent, 0);
}

function write(
  value: JsonValue,
  numberMode: 'canonical' | 'original',
  indent: number,
  depth: number,
): string {
  if (value === null) return 'null';
  if (value instanceof Decimal) {
    return numberMode === 'original' ? value.raw : canonicalDecimal(value);
  }
  if (typeof value === 'number') {
    throw new TypeError('plain numbers are not part of the exact DOM; use Decimal');
  }
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'string') return JSON.stringify(value);

  if (Array.isArray(value)) {
    if (value.length === 0) return '[]';
    if (indent === 0) {
      return '[' + value.map((item) => write(item, numberMode, indent, depth + 1)).join(',') + ']';
    }
    const pad = ' '.repeat(indent * (depth + 1));
    const closePad = ' '.repeat(indent * depth);
    return (
      '[\n' +
      value.map((item) => pad + write(item, numberMode, indent, depth + 1)).join(',\n') +
      '\n' + closePad + ']'
    );
  }

  if (typeof value === 'object') {
    const entries = Object.entries(value);
    if (entries.length === 0) return '{}';
    if (indent === 0) {
      return (
        '{' +
        entries
          .map(([key, item]) => JSON.stringify(key) + ':' + write(item, numberMode, indent, depth + 1))
          .join(',') +
        '}'
      );
    }
    const pad = ' '.repeat(indent * (depth + 1));
    const closePad = ' '.repeat(indent * depth);
    return (
      '{\n' +
      entries
        .map(
          ([key, item]) =>
            pad + JSON.stringify(key) + ': ' + write(item, numberMode, indent, depth + 1),
        )
        .join(',\n') +
      '\n' + closePad + '}'
    );
  }

  throw new TypeError(`cannot serialize value of type ${typeof value}`);
}
