import assert from 'node:assert/strict';

/**
 * Structural deep equality that ignores whether records were created with a
 * null prototype. The security property (null prototype) is asserted
 * separately.
 */
export function assertStructurallyEqual(actual: unknown, expected: unknown): void {
  assert.deepEqual(toPlain(actual), toPlain(expected));
}

function toPlain(value: unknown): unknown {
  if (value === null || typeof value !== 'object') {
    return value;
  }
  if (Array.isArray(value)) {
    return value.map(toPlain);
  }
  const result: Record<string, unknown> = {};
  for (const key of Object.keys(value)) {
    result[key] = toPlain((value as Record<string, unknown>)[key]);
  }
  return result;
}
