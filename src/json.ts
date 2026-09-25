/** JSON value model and JSON Pointer utilities (RFC 6901 escaping). */

export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

export function deepClone<T extends Json>(value: T): T {
  if (value === null || typeof value !== 'object') return value;
  if (Array.isArray(value)) {
    const out: Json[] = new Array(value.length);
    for (let i = 0; i < value.length; i++) out[i] = deepClone(value[i]!);
    return out as T;
  }
  const out: { [key: string]: Json } = {};
  for (const key of Object.keys(value)) out[key] = deepClone(value[key]!);
  return out as T;
}

export function deepEqual(a: Json, b: Json): boolean {
  if (a === b) return true;
  if (a === null || b === null || typeof a !== 'object' || typeof b !== 'object') return false;
  const aArr = Array.isArray(a);
  const bArr = Array.isArray(b);
  if (aArr !== bArr) return false;
  if (aArr && bArr) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!deepEqual(a[i]!, b[i]!)) return false;
    return true;
  }
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  for (const k of ka) {
    if (!Object.prototype.hasOwnProperty.call(b, k)) return false;
    if (!deepEqual(a[k]!, (b as { [key: string]: Json })[k]!)) return false;
  }
  return true;
}

export class PointerError extends Error {}

export function escapeToken(token: string): string {
  return token.replace(/~/g, '~0').replace(/\//g, '~1');
}

export function unescapeToken(token: string): string {
  if (/~(?![01])/.test(token)) throw new PointerError(`invalid escape in token "${token}"`);
  return token.replace(/~1/g, '/').replace(/~0/g, '~');
}

/** Parse a JSON Pointer into unescaped tokens. "" addresses the whole document. */
export function parsePointer(path: string): string[] {
  if (typeof path !== 'string') throw new PointerError('path must be a string');
  if (path === '') return [];
  if (!path.startsWith('/')) throw new PointerError(`path must start with "/": ${path}`);
  return path.slice(1).split('/').map(unescapeToken);
}

export function formatPointer(tokens: readonly string[]): string {
  if (tokens.length === 0) return '';
  return '/' + tokens.map(escapeToken).join('/');
}

export function joinPointer(base: string, token: string): string {
  return base + '/' + escapeToken(token);
}

const INDEX_RE = /^(0|[1-9][0-9]*)$/;

/**
 * Validate an array index token. "-" (append) is only returned when allowAppend.
 * Leading zeros are rejected ("01" is not a valid index).
 */
export function arrayIndex(token: string, allowAppend: boolean): number | '-' {
  if (token === '-') {
    if (allowAppend) return '-';
    throw new PointerError('"-" (append) is only allowed for add');
  }
  if (!INDEX_RE.test(token)) throw new PointerError(`invalid array index "${token}"`);
  return Number(token);
}
