/**
 * Condition algebra for policy rules.
 *
 * Constraints (by design):
 *  - Policies read ONLY explicit attributes supplied at query time.
 *    A condition referencing a missing attribute evaluates to `false`.
 *  - Attribute domains are finite: either an enumerated set of strings
 *    or a bounded integer range. No external identity/lookup services.
 *  - Boolean combinators: and / or / not.
 */

export type AttrValue = string | number;
export type Attrs = Readonly<Record<string, AttrValue | undefined>>;

export type IntOp = '<' | '<=' | '>' | '>=' | '==' | '!=';

export type Cond =
  | { readonly kind: 'eq'; readonly attr: string; readonly value: string }
  | { readonly kind: 'in'; readonly attr: string; readonly values: readonly string[] }
  | { readonly kind: 'int'; readonly attr: string; readonly op: IntOp; readonly value: number }
  | { readonly kind: 'and'; readonly all: readonly Cond[] }
  | { readonly kind: 'or'; readonly any: readonly Cond[] }
  | { readonly kind: 'not'; readonly cond: Cond };

export const eq = (attr: string, value: string): Cond => ({ kind: 'eq', attr, value });
export const inSet = (attr: string, values: readonly string[]): Cond => ({ kind: 'in', attr, values });
export const intCmp = (attr: string, op: IntOp, value: number): Cond => ({ kind: 'int', attr, op, value });
export const and = (...all: Cond[]): Cond => ({ kind: 'and', all });
export const or = (...any: Cond[]): Cond => ({ kind: 'or', any });
export const not = (cond: Cond): Cond => ({ kind: 'not', cond });
export const ALWAYS: Cond = { kind: 'and', all: [] };

function cmpInt(op: IntOp, lhs: number, rhs: number): boolean {
  switch (op) {
    case '<': return lhs < rhs;
    case '<=': return lhs <= rhs;
    case '>': return lhs > rhs;
    case '>=': return lhs >= rhs;
    case '==': return lhs === rhs;
    case '!=': return lhs !== rhs;
  }
}

/** Evaluate a condition against explicit attributes. Missing attr => false. */
export function evalCond(cond: Cond, attrs: Attrs): boolean {
  switch (cond.kind) {
    case 'eq': {
      const v = attrs[cond.attr];
      return typeof v === 'string' && v === cond.value;
    }
    case 'in': {
      const v = attrs[cond.attr];
      return typeof v === 'string' && cond.values.includes(v);
    }
    case 'int': {
      const v = attrs[cond.attr];
      return typeof v === 'number' && Number.isInteger(v) && cmpInt(cond.op, v, cond.value);
    }
    case 'and':
      return cond.all.every((c) => evalCond(c, attrs));
    case 'or':
      return cond.any.some((c) => evalCond(c, attrs));
    case 'not':
      return !evalCond(cond.cond, attrs);
  }
}

/** Attribute domain schema: finite enum sets and bounded integer ranges only. */
export type AttrDomain =
  | { readonly kind: 'enum'; readonly values: readonly string[] }
  | { readonly kind: 'int'; readonly min: number; readonly max: number };

export type Schema = Readonly<Record<string, AttrDomain>>;

/** Validate that a condition only references attributes declared in the schema. */
export function validateCond(cond: Cond, schema: Schema, path = 'cond'): string[] {
  const errors: string[] = [];
  const checkAttr = (attr: string, expect: 'enum' | 'int'): void => {
    const domain = schema[attr];
    if (!domain) {
      errors.push(`${path}: unknown attribute "${attr}"`);
    } else if (domain.kind !== expect) {
      errors.push(`${path}: attribute "${attr}" is ${domain.kind}, expected ${expect}`);
    }
  };
  switch (cond.kind) {
    case 'eq':
      checkAttr(cond.attr, 'enum');
      break;
    case 'in':
      checkAttr(cond.attr, 'enum');
      if (cond.values.length === 0) errors.push(`${path}: "in" requires a non-empty set`);
      break;
    case 'int':
      checkAttr(cond.attr, 'int');
      if (!Number.isInteger(cond.value)) errors.push(`${path}: int comparison value must be an integer`);
      break;
    case 'and':
      cond.all.forEach((c, i) => errors.push(...validateCond(c, schema, `${path}.and[${i}]`)));
      break;
    case 'or':
      cond.any.forEach((c, i) => errors.push(...validateCond(c, schema, `${path}.or[${i}]`)));
      break;
    case 'not':
      errors.push(...validateCond(cond.cond, schema, `${path}.not`));
      break;
  }
  return errors;
}

/** Enumerate the full Cartesian product of attribute domains (verification only). */
export function* enumerateAttrs(schema: Schema): Generator<Record<string, AttrValue>> {
  const names = Object.keys(schema);
  const domains = names.map((n) => {
    const d = schema[n]!;
    if (d.kind === 'enum') return d.values as readonly AttrValue[];
    const vals: AttrValue[] = [];
    for (let v = d.min; v <= d.max; v++) vals.push(v);
    return vals;
  });
  const combo: Record<string, AttrValue> = {};
  const idx = new Array<number>(names.length).fill(0);
  while (true) {
    for (let i = 0; i < names.length; i++) combo[names[i]!] = domains[i]![idx[i]!]!;
    yield { ...combo };
    let i = names.length - 1;
    while (i >= 0) {
      idx[i]!++;
      if (idx[i]! < domains[i]!.length) break;
      idx[i] = 0;
      i--;
    }
    if (i < 0) return;
  }
}
