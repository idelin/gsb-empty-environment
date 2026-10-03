import { createHash } from 'node:crypto';

/**
 * Canonical serialization: object keys are sorted recursively so that any
 * two semantically identical states produce byte-identical output.
 */
export function canonicalize(value: unknown): string {
  return JSON.stringify(sortValue(value));
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(sortValue);
  }
  if (value !== null && typeof value === 'object') {
    const input = value as Record<string, unknown>;
    const sorted: Record<string, unknown> = {};
    for (const key of Object.keys(input).sort()) {
      sorted[key] = sortValue(input[key]);
    }
    return sorted;
  }
  return value;
}

/** Canonical state bytes (UTF-8). Byte-level comparison target for replay. */
export function stateBytes(value: unknown): Buffer {
  return Buffer.from(canonicalize(value), 'utf8');
}

/** SHA-256 hex digest of the canonical state bytes. */
export function hashState(value: unknown): string {
  return createHash('sha256').update(stateBytes(value)).digest('hex');
}
