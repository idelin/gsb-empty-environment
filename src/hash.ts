import { createHash } from 'node:crypto';

/** Canonical JSON: object keys sorted recursively so hashing is stable. */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalize).join(',')}]`;
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${canonicalize(obj[k])}`).join(',')}}`;
}

export function sha256hex(data: string): string {
  return createHash('sha256').update(data, 'utf8').digest('hex');
}

/** Digest of an arbitrary JSON-able value. */
export function digestOf(value: unknown): string {
  return sha256hex(canonicalize(value));
}
