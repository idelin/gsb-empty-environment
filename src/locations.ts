import type { AcceptanceError, ErrorCode, ErrorLocation } from './types.ts';

/** Escape one JSON Pointer reference token (RFC 6901). */
export function escapeToken(token: string): string {
  return token.replaceAll('~', '~0').replaceAll('/', '~1');
}

export function unescapeToken(token: string): string {
  return token.replaceAll('~1', '/').replaceAll('~0', '~');
}

export function joinPointer(parent: string, key: string): string {
  return `${parent === '/' ? '' : parent}/${escapeToken(key)}`;
}

export function parentPointer(pointer: string): string {
  if (pointer === '' || pointer === '/') return '';
  const idx = pointer.lastIndexOf('/');
  return idx <= 0 ? '' : pointer.slice(0, idx);
}

export function pointerSegments(pointer: string): string[] {
  if (pointer === '') return [];
  return pointer.split('/').slice(1).map(unescapeToken);
}

/**
 * Match a pointer against a grant/protection pattern.
 * Supported: exact `/a/b`, single-segment `*`, and trailing `/**`.
 */
export function patternMatches(pattern: string, pointer: string): boolean {
  const patternSegs = pointerSegments(pattern);
  const targetSegs = pointerSegments(pointer);

  if (patternSegs.length >= 1 && patternSegs[patternSegs.length - 1] === '**') {
    const prefix = patternSegs.slice(0, -1);
    if (prefix.length === 0) return true; // /** matches everything
    if (targetSegs.length < prefix.length) return false;
    return prefix.every((seg, i) => seg === '*' || seg === targetSegs[i]);
  }

  if (patternSegs.length !== targetSegs.length) return false;
  return patternSegs.every((seg, i) => seg === '*' || seg === targetSegs[i]);
}

export function makeError(
  code: ErrorCode,
  message: string,
  location: ErrorLocation,
  detail?: AcceptanceError['detail'],
): AcceptanceError {
  return { code, message, location, ...(detail ? { detail } : {}) };
}

/** Stable ordering so failure locations are deterministic. */
export function errorSortKey(error: AcceptanceError): string {
  const loc = error.location;
  return [
    loc.pointer,
    loc.facet ?? '',
    loc.nodeId ?? '',
    error.code,
    JSON.stringify(error.detail ?? {}),
  ].join('¦');
}

export function sortErrors(errors: AcceptanceError[]): AcceptanceError[] {
  const seen = new Set<string>();
  const deduped: AcceptanceError[] = [];
  for (const error of errors) {
    const key = errorSortKey(error) + error.message;
    if (seen.has(key)) continue;
    seen.add(key);
    deduped.push(error);
  }
  return deduped.sort((a, b) => errorSortKey(a).localeCompare(errorSortKey(b)));
}

/** Deterministic canonical JSON (object keys sorted). */
export function canonicalJson(value: unknown): string {
  return JSON.stringify(sortDeep(value));
}

function sortDeep(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortDeep);
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>;
    return Object.keys(record)
      .sort()
      .reduce<Record<string, unknown>>((acc, key) => {
        acc[key] = sortDeep(record[key]);
        return acc;
      }, {});
  }
  return value;
}

/** Small deterministic FNV-1a hash for publication ids. */
export function shortHash(input: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, '0');
}
