import { createHash } from 'node:crypto';

/** Deterministic JSON: object keys sorted recursively, array order preserved. */
export function canonicalize(value: unknown): string {
  return JSON.stringify(sortValue(value));
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(sortValue);
  }
  if (value !== null && typeof value === 'object') {
    const source = value as Record<string, unknown>;
    const out: Record<string, unknown> = {};
    for (const key of Object.keys(source).sort()) {
      out[key] = sortValue(source[key]);
    }
    return out;
  }
  return value;
}

/** SHA-256 hex digest of the canonical form of a value. */
export function digest(value: unknown): string {
  return createHash('sha256').update(canonicalize(value)).digest('hex');
}
