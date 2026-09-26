import type { Condition, Json } from './types.ts';
import { deepEqual } from './presence.ts';

export type Tri = boolean | 'undecided';

export type FieldState =
  | { state: 'absent' }
  | { state: 'value'; value: unknown }
  | { state: 'unknown' };

export type FieldGetter = (field: string) => FieldState;

export function evalCondition(cond: Condition, get: FieldGetter): Tri {
  switch (cond.op) {
    case 'present': {
      const s = get(cond.field);
      if (s.state === 'unknown') return 'undecided';
      return s.state === 'value';
    }
    case 'absent': {
      const s = get(cond.field);
      if (s.state === 'unknown') return 'undecided';
      return s.state === 'absent';
    }
    case 'equals': {
      const s = get(cond.field);
      if (s.state !== 'value') return 'undecided';
      return deepEqual(s.value, cond.value);
    }
    case 'and': {
      let und = false;
      for (const c of cond.all) {
        const r = evalCondition(c, get);
        if (r === false) return false;
        if (r === 'undecided') und = true;
      }
      return und ? 'undecided' : true;
    }
    case 'or': {
      let und = false;
      for (const c of cond.any) {
        const r = evalCondition(c, get);
        if (r === true) return true;
        if (r === 'undecided') und = true;
      }
      return und ? 'undecided' : false;
    }
    case 'not': {
      const r = evalCondition(cond.cond, get);
      return r === 'undecided' ? 'undecided' : !r;
    }
  }
}

export function conditionFields(cond: Condition, out: Set<string> = new Set()): Set<string> {
  switch (cond.op) {
    case 'present':
    case 'absent':
    case 'equals':
      out.add(cond.field);
      break;
    case 'and':
      for (const c of cond.all) conditionFields(c, out);
      break;
    case 'or':
      for (const c of cond.any) conditionFields(c, out);
      break;
    case 'not':
      conditionFields(cond.cond, out);
      break;
  }
  return out;
}

export function equalsLiterals(
  cond: Condition,
  out: Map<string, Json[]> = new Map(),
): Map<string, Json[]> {
  switch (cond.op) {
    case 'equals': {
      const arr = out.get(cond.field) ?? [];
      if (!arr.some((v) => deepEqual(v, cond.value))) arr.push(cond.value);
      out.set(cond.field, arr);
      break;
    }
    case 'and':
      for (const c of cond.all) equalsLiterals(c, out);
      break;
    case 'or':
      for (const c of cond.any) equalsLiterals(c, out);
      break;
    case 'not':
      equalsLiterals(cond.cond, out);
      break;
    case 'present':
    case 'absent':
      break;
  }
  return out;
}

export function missingEqualsFields(
  cond: Condition,
  get: FieldGetter,
  out: Set<string> = new Set(),
): Set<string> {
  switch (cond.op) {
    case 'equals':
      if (get(cond.field).state === 'absent') out.add(cond.field);
      break;
    case 'and':
      for (const c of cond.all) missingEqualsFields(c, get, out);
      break;
    case 'or':
      for (const c of cond.any) missingEqualsFields(c, get, out);
      break;
    case 'not':
      missingEqualsFields(cond.cond, get, out);
      break;
    case 'present':
    case 'absent':
      break;
  }
  return out;
}
