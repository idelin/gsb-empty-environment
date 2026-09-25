import { UnitMigrationError } from './errors.ts';

/** The JSON-compatible tree type: the only input this library accepts. */
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

/**
 * Validates that `value` is a JSON-compatible tree. Object key order is
 * irrelevant to all downstream logic; array order is significant and
 * preserved. Violations are reported with the exact path of the offender.
 */
export function assertJsonTree(value: unknown, path = '$'): asserts value is JsonValue {
  if (value === null) return;
  switch (typeof value) {
    case 'boolean':
    case 'string':
      return;
    case 'number':
      if (!Number.isFinite(value)) {
        throw new UnitMigrationError('INPUT_NOT_JSON', `non-finite number at ${path}`, { path });
      }
      return;
    case 'object': {
      if (Array.isArray(value)) {
        value.forEach((item, i) => assertJsonTree(item, `${path}[${i}]`));
        return;
      }
      for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
        assertJsonTree(item, `${path}.${key}`);
      }
      return;
    }
    default:
      throw new UnitMigrationError(
        'INPUT_NOT_JSON',
        `value of type "${typeof value}" at ${path} is not JSON-compatible`,
        { path },
      );
  }
}
