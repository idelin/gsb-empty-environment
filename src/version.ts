import { createHash } from 'node:crypto';

/** Deterministic JSON serialization with sorted object keys. */
export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`;
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${stableStringify(record[k])}`).join(',')}}`;
}

export function hashVersion(input: string): string {
  return createHash('sha256').update(input).digest('hex').slice(0, 16);
}
