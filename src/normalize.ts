import { isRef, keyOf } from './types.ts';
import type { EntityKey, Value } from './types.ts';

export interface FieldWriter {
  put(key: EntityKey, field: string, value: Value): void;
}

const isEntityObject = (raw: unknown): raw is Record<string, unknown> => {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return false;
  const obj = raw as Record<string, unknown>;
  return typeof obj.__typename === 'string' && obj.id !== undefined && obj.id !== null;
};

export function normalizeValueInto(writer: FieldWriter, raw: unknown): Value {
  if (raw === null || raw === undefined) return null;
  if (typeof raw === 'string' || typeof raw === 'number' || typeof raw === 'boolean') {
    return raw;
  }
  if (isRef(raw)) return raw;
  if (Array.isArray(raw)) return raw.map((item) => normalizeValueInto(writer, item));
  if (typeof raw === 'object') {
    if (isEntityObject(raw)) {
      const key = normalizeEntityInto(writer, raw);
      return { $ref: key };
    }
    // Object without __typename+id: embedded value, copied per parent. Two parents
    // holding structurally equal embedded objects never share or merge them.
    const embedded: { [field: string]: Value } = {};
    for (const [field, value] of Object.entries(raw as Record<string, unknown>)) {
      if (value === undefined) continue;
      embedded[field] = normalizeValueInto(writer, value);
    }
    return embedded;
  }
  throw new Error(`unsupported value in response: ${String(raw)}`);
}

export function normalizeEntityInto(
  writer: FieldWriter,
  obj: Record<string, unknown>,
): EntityKey {
  if (!isEntityObject(obj)) {
    throw new Error('expected an object with __typename and id');
  }
  const key = keyOf(obj.__typename as string, obj.id as string | number);
  for (const [field, value] of Object.entries(obj)) {
    if (field === '__typename') continue;
    if (field === 'id') continue;
    if (value === undefined) continue;
    // Only fields present in the payload are written; absent fields are left
    // untouched (partial response), explicit null is stored as null.
    writer.put(key, field, normalizeValueInto(writer, value));
  }
  return key;
}
