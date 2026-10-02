import { keyOf } from './types.ts';
import type { EntityKey, Ref, Value } from './types.ts';

export interface NormConfig {
  /** Field that carries the entity type on nested objects. Default "__typename". */
  typeKey: string;
  /** Field that carries the entity id. Default "id". */
  idKey: string;
}

export const defaultConfig: NormConfig = { typeKey: '__typename', idKey: 'id' };

/**
 * Splits a nested response into per-entity field patches keyed by `type:id`.
 *
 * Rules:
 * - An object with both an id and a type becomes an entity; it is stored in
 *   the shared entity table and replaced by a `{ $ref }` at its use site, so
 *   every parent aliases the same record.
 * - An object without an id (or without a type) stays inline. Inline objects
 *   are rebuilt wholesale on each write and are never merged by identity, so
 *   two parents' id-less objects can never be confused with each other.
 * - `undefined` field values mean "absent" (partial response: keep the old
 *   value); explicit `null` is stored as `null`.
 */
export class Normalizer {
  /** Accumulated entity patches: key -> normalized fields (later writes win). */
  readonly patches: Map<EntityKey, Record<string, Value>> = new Map();
  private readonly cfg: NormConfig;

  constructor(cfg?: Partial<NormConfig>) {
    this.cfg = { ...defaultConfig, ...cfg };
  }

  /** Normalize a root payload (single entity object or array of them). */
  rootOf(type: string, data: unknown): Value {
    if (Array.isArray(data)) {
      return data.map((d) => this.rootEntity(type, d));
    }
    return this.rootEntity(type, data);
  }

  /** Normalize a bag of fields for an already-keyed entity (partial update). */
  fieldsOf(obj: Record<string, unknown>): Record<string, Value> {
    const out: Record<string, Value> = {};
    for (const k of Object.keys(obj)) {
      const v = this.value(obj[k]);
      if (v !== undefined) out[k] = v;
    }
    return out;
  }

  private rootEntity(type: string, obj: unknown): Ref {
    if (typeof obj !== 'object' || obj === null || Array.isArray(obj)) {
      throw new Error(`root entity of type ${type} must be an object`);
    }
    const id = (obj as Record<string, unknown>)[this.cfg.idKey];
    if (typeof id !== 'string' && typeof id !== 'number') {
      throw new Error(`root entity of type ${type} requires "${this.cfg.idKey}"`);
    }
    return this.entity(type, id, obj as Record<string, unknown>);
  }

  private entity(type: string, id: string | number, obj: Record<string, unknown>): Ref {
    const key = keyOf(type, id);
    const fields = this.fieldsOf(obj);
    if (fields[this.cfg.typeKey] === undefined) fields[this.cfg.typeKey] = type;
    const prev = this.patches.get(key);
    // The same entity may appear several times in one payload (aliases);
    // merge its occurrences within this batch.
    this.patches.set(key, prev ? { ...prev, ...fields } : fields);
    return { $ref: key };
  }

  private value(v: unknown): Value | undefined {
    if (v === undefined) return undefined; // "missing" sentinel, never stored
    if (v === null || typeof v !== 'object') return v as Value;
    if (Array.isArray(v)) {
      const out: Value[] = [];
      for (const item of v) {
        const n = this.value(item);
        out.push(n === undefined ? null : n);
      }
      return out;
    }
    const obj = v as Record<string, unknown>;
    const id = obj[this.cfg.idKey];
    const t = obj[this.cfg.typeKey];
    if ((typeof id === 'string' || typeof id === 'number') && typeof t === 'string') {
      return this.entity(t, id, obj);
    }
    // Id-less object: embedded inline, rebuilt on every write, never merged.
    const out: Record<string, Value> = {};
    for (const k of Object.keys(obj)) {
      const n = this.value(obj[k]);
      if (n !== undefined) out[k] = n;
    }
    return out;
  }
}
