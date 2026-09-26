import type { Condition } from './schema.ts';

/**
 * Tri-state condition evaluation.
 *
 * A condition is 'undecided' when it references a field whose value cannot
 * be inspected: the field is missing, or it is a hypothetical "completion"
 * field whose presence is promised but whose value is not yet known.
 * `present` / `absent` are always decidable (that is their whole point).
 * How 'undecided' gates a rule is a contract policy (see validate.ts),
 * not a concern of this module.
 */
export type Tri = 'true' | 'false' | 'undecided';

export interface View {
  has(field: string): boolean;
  /** true iff the field's VALUE is inspectable (own property of the instance). */
  valueKnown(field: string): boolean;
  get(field: string): unknown;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((x, i) => deepEqual(x, b[i]))
    );
  }
  if (typeof a === 'object') {
    const ka = Object.keys(a as object);
    const kb = Object.keys(b as object);
    return (
      ka.length === kb.length &&
      ka.every(
        (k) =>
          Object.prototype.hasOwnProperty.call(b, k) &&
          deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
      )
    );
  }
  return false;
}

export function evalCondition(cond: Condition, view: View): Tri {
  switch (cond.op) {
    case 'present':
      return view.has(cond.field) ? 'true' : 'false';
    case 'absent':
      return view.has(cond.field) ? 'false' : 'true';
    case 'equals': {
      if (!view.has(cond.field) || !view.valueKnown(cond.field)) return 'undecided';
      return deepEqual(view.get(cond.field), cond.value) ? 'true' : 'false';
    }
    case 'notEquals': {
      if (!view.has(cond.field) || !view.valueKnown(cond.field)) return 'undecided';
      return deepEqual(view.get(cond.field), cond.value) ? 'false' : 'true';
    }
    case 'and': {
      let undecided = false;
      for (const c of cond.all) {
        const t = evalCondition(c, view);
        if (t === 'false') return 'false';
        if (t === 'undecided') undecided = true;
      }
      return undecided ? 'undecided' : 'true';
    }
    case 'or': {
      let undecided = false;
      for (const c of cond.any) {
        const t = evalCondition(c, view);
        if (t === 'true') return 'true';
        if (t === 'undecided') undecided = true;
      }
      return undecided ? 'undecided' : 'false';
    }
    case 'not': {
      const t = evalCondition(cond.cond, view);
      return t === 'true' ? 'false' : t === 'false' ? 'true' : 'undecided';
    }
  }
}
