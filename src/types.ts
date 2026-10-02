/** Entity key in the form `${type}:${id}`, e.g. "Todo:42". */
export type EntityKey = string;

/** A shared reference to an entity living in the entity table. */
export interface Ref {
  $ref: EntityKey;
}

/**
 * A stored value: JSON scalar, a shared Ref, or arrays / inline objects of
 * values. Inline (id-less) objects are stored embedded in their parent and
 * are rebuilt wholesale on every write; they never merge by identity.
 */
export type Value =
  | null
  | boolean
  | number
  | string
  | Ref
  | Value[]
  | { [key: string]: Value };

export function isRef(v: unknown): v is Ref {
  return (
    typeof v === 'object' &&
    v !== null &&
    typeof (v as { $ref?: unknown }).$ref === 'string'
  );
}

export function keyOf(type: string, id: string | number): EntityKey {
  return `${type}:${id}`;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((v, i) => deepEqual(v, b[i]));
  }
  const ka = Object.keys(a as object);
  const kb = Object.keys(b as object);
  if (ka.length !== kb.length) return false;
  return ka.every((k) =>
    deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
  );
}

/** Collect ref targets (with occurrence counts) reachable from a stored value. */
export function collectRefs(
  value: Value,
  out: Map<EntityKey, number> = new Map(),
): Map<EntityKey, number> {
  if (isRef(value)) {
    out.set(value.$ref, (out.get(value.$ref) ?? 0) + 1);
  } else if (Array.isArray(value)) {
    for (const item of value) collectRefs(item, out);
  } else if (typeof value === 'object' && value !== null) {
    for (const k of Object.keys(value)) {
      collectRefs((value as Record<string, Value>)[k], out);
    }
  }
  return out;
}
