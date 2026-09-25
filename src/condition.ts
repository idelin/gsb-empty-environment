/**
 * Boolean condition AST. Deliberately small and closed:
 *   - integer comparisons against declared int attributes
 *   - membership in an explicit finite set (int or enum attributes)
 *   - boolean attribute tests
 *   - and / or / not / const
 * No arithmetic on attributes, no string matching, no external calls.
 */

import type { AttributeValue, Attributes, Schema } from './schema.ts';

export type CmpOp = 'eq' | 'ne' | 'lt' | 'lte' | 'gt' | 'gte';

export type Condition =
  | { op: 'cmp'; attr: string; cmp: CmpOp; value: number }
  | { op: 'in'; attr: string; values: readonly (number | string)[] }
  | { op: 'bool'; attr: string; value: boolean }
  | { op: 'and'; args: readonly Condition[] }
  | { op: 'or'; args: readonly Condition[] }
  | { op: 'not'; arg: Condition }
  | { op: 'const'; value: boolean };

export type Tri = true | false | 'unknown';

function cmpInt(cmp: CmpOp, lhs: number, rhs: number): boolean {
  switch (cmp) {
    case 'eq': return lhs === rhs;
    case 'ne': return lhs !== rhs;
    case 'lt': return lhs < rhs;
    case 'lte': return lhs <= rhs;
    case 'gt': return lhs > rhs;
    case 'gte': return lhs >= rhs;
  }
}

/** Validate the condition against the schema and collect nothing. Throws on misuse. */
export function validateCondition(schema: Schema, cond: Condition): void {
  switch (cond.op) {
    case 'cmp': {
      const dom = schema[cond.attr];
      if (!dom) throw new Error(`condition reads undeclared attribute ${cond.attr}`);
      if (dom.type !== 'int') throw new Error(`cmp condition requires int attribute, ${cond.attr} is ${dom.type}`);
      if (!Number.isInteger(cond.value)) throw new Error('cmp value must be an integer');
      return;
    }
    case 'in': {
      const dom = schema[cond.attr];
      if (!dom) throw new Error(`condition reads undeclared attribute ${cond.attr}`);
      if (dom.type === 'bool') throw new Error(`in condition not allowed on bool attribute ${cond.attr}; use bool`);
      if (!Array.isArray(cond.values) || cond.values.length === 0) {
        throw new Error('in condition needs a non-empty finite values list');
      }
      for (const v of cond.values) {
        if (dom.type === 'int' && !(typeof v === 'number' && Number.isInteger(v))) {
          throw new Error(`in value ${JSON.stringify(v)} is not an integer for int attribute ${cond.attr}`);
        }
        if (dom.type === 'enum' && !(typeof v === 'string' && dom.values.includes(v))) {
          throw new Error(`in value ${JSON.stringify(v)} is outside enum domain of ${cond.attr}`);
        }
      }
      return;
    }
    case 'bool': {
      const dom = schema[cond.attr];
      if (!dom) throw new Error(`condition reads undeclared attribute ${cond.attr}`);
      if (dom.type !== 'bool') throw new Error(`bool condition requires bool attribute, ${cond.attr} is ${dom.type}`);
      if (typeof cond.value !== 'boolean') throw new Error('bool condition value must be a boolean');
      return;
    }
    case 'and':
    case 'or':
      if (!Array.isArray(cond.args) || cond.args.length === 0) {
        throw new Error(`${cond.op} needs at least one argument`);
      }
      for (const a of cond.args) validateCondition(schema, a);
      return;
    case 'not':
      validateCondition(schema, cond.arg);
      return;
    case 'const':
      if (typeof cond.value !== 'boolean') throw new Error('const condition value must be a boolean');
      return;
    default:
      throw new Error(`unknown condition op ${(cond as { op: string }).op}`);
  }
}

/** Attributes a condition reads (used for incremental invalidation). */
export function conditionDeps(cond: Condition): ReadonlySet<string> {
  const out = new Set<string>();
  const walk = (c: Condition): void => {
    switch (c.op) {
      case 'cmp':
      case 'in':
      case 'bool':
        out.add(c.attr);
        return;
      case 'and':
      case 'or':
        for (const a of c.args) walk(a);
        return;
      case 'not':
        walk(c.arg);
        return;
      case 'const':
        return;
    }
  };
  walk(cond);
  return out;
}

/**
 * Tri-state evaluation: attributes absent from `attrs` yield 'unknown'
 * (propagated through and/or/not with Kleene semantics).
 */
export function evalCondition(cond: Condition, attrs: Attributes): Tri {
  switch (cond.op) {
    case 'cmp': {
      const v = attrs[cond.attr];
      if (v === undefined) return 'unknown';
      return cmpInt(cond.cmp, v as number, cond.value);
    }
    case 'in': {
      const v = attrs[cond.attr];
      if (v === undefined) return 'unknown';
      return (cond.values as readonly AttributeValue[]).includes(v);
    }
    case 'bool': {
      const v = attrs[cond.attr];
      if (v === undefined) return 'unknown';
      return v === cond.value;
    }
    case 'and': {
      let unknown = false;
      for (const a of cond.args) {
        const r = evalCondition(a, attrs);
        if (r === false) return false;
        if (r === 'unknown') unknown = true;
      }
      return unknown ? 'unknown' : true;
    }
    case 'or': {
      let unknown = false;
      for (const a of cond.args) {
        const r = evalCondition(a, attrs);
        if (r === true) return true;
        if (r === 'unknown') unknown = true;
      }
      return unknown ? 'unknown' : false;
    }
    case 'not': {
      const r = evalCondition(cond.arg, attrs);
      return r === 'unknown' ? 'unknown' : !r;
    }
    case 'const':
      return cond.value;
  }
}

/**
 * Canonical form: order-insensitive for and/or/in, recursively normalized.
 * Two rules with the same id are "the same rule" iff their canonical contents
 * match; otherwise the duplicate id is a conflict and the policy is rejected.
 */
export function canonicalCondition(cond: Condition): unknown {
  switch (cond.op) {
    case 'cmp':
      return { op: 'cmp', attr: cond.attr, cmp: cond.cmp, value: cond.value };
    case 'in': {
      const values = [...cond.values].sort((a, b) =>
        String(a) < String(b) ? -1 : String(a) > String(b) ? 1 : 0,
      );
      return { op: 'in', attr: cond.attr, values };
    }
    case 'bool':
      return { op: 'bool', attr: cond.attr, value: cond.value };
    case 'and':
    case 'or': {
      const args = cond.args
        .map(canonicalCondition)
        .map((a) => JSON.stringify(a))
        .sort()
        .map((s) => JSON.parse(s) as unknown);
      return { op: cond.op, args };
    }
    case 'not':
      return { op: 'not', arg: canonicalCondition(cond.arg) };
    case 'const':
      return { op: 'const', value: cond.value };
  }
}
