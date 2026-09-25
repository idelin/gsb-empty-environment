import { triAnd, triNot, triOfBoolean, triOr, U, type Tri } from './tri.ts';

/**
 * Policy expressions. Policies may only read explicitly named attributes and
 * combine them via:
 *   - finite-set membership (`in`)
 *   - integer comparisons (`eq` / `ne` / inclusive integer `range`)
 *   - boolean operators (`not` / `and` / `or`)
 * Attribute values are integers; anything missing or non-integer evaluates
 * to `unknown`, never to `false`.
 */
export type Expr = Lit | Cmp | InSet | Range | Not | And | Or;

export interface Lit {
  readonly kind: 'lit';
  readonly value: Tri;
}
export interface Cmp {
  readonly kind: 'cmp';
  readonly attr: string;
  readonly op: 'eq' | 'ne';
  readonly value: number;
}
export interface InSet {
  readonly kind: 'in';
  readonly attr: string;
  readonly set: readonly number[];
}
export interface Range {
  readonly kind: 'range';
  readonly attr: string;
  readonly min: number | null; // inclusive, null = unbounded
  readonly max: number | null; // inclusive, null = unbounded
}
export interface Not {
  readonly kind: 'not';
  readonly child: Expr;
}
export interface And {
  readonly kind: 'and';
  readonly children: readonly Expr[];
}
export interface Or {
  readonly kind: 'or';
  readonly children: readonly Expr[];
}

export const lit = (value: Tri): Lit => ({ kind: 'lit', value });
export const eq = (attr: string, value: number): Cmp => ({ kind: 'cmp', attr, op: 'eq', value });
export const ne = (attr: string, value: number): Cmp => ({ kind: 'cmp', attr, op: 'ne', value });
export const inSet = (attr: string, set: readonly number[]): InSet => ({ kind: 'in', attr, set });
export const range = (attr: string, min: number | null, max: number | null): Range => ({
  kind: 'range',
  attr,
  min,
  max,
});
export const gte = (attr: string, min: number): Range => range(attr, min, null);
export const lte = (attr: string, max: number): Range => range(attr, null, max);
export const gt = (attr: string, min: number): Range => range(attr, min + 1, null);
export const lt = (attr: string, max: number): Range => range(attr, null, max - 1);
export const not = (child: Expr): Not => ({ kind: 'not', child });
export const and = (...children: readonly Expr[]): And => ({ kind: 'and', children });
export const or = (...children: readonly Expr[]): Or => ({ kind: 'or', children });

/** Names of every attribute the expression may read. */
export function attrsOf(e: Expr, into: Set<string> = new Set()): Set<string> {
  switch (e.kind) {
    case 'lit':
      break;
    case 'cmp':
    case 'in':
    case 'range':
      into.add(e.attr);
      break;
    case 'not':
      attrsOf(e.child, into);
      break;
    case 'and':
    case 'or':
      for (const c of e.children) attrsOf(c, into);
      break;
  }
  return into;
}

function evalLeaf(e: Cmp | InSet | Range, lookup: (attr: string) => unknown): Tri {
  const v = lookup(e.attr);
  // Missing or non-integer values are unknown — never silently false.
  if (typeof v !== 'number' || !Number.isInteger(v)) return U;
  switch (e.kind) {
    case 'cmp':
      return triOfBoolean(e.op === 'eq' ? v === e.value : v !== e.value);
    case 'in':
      return triOfBoolean(e.set.includes(v));
    case 'range':
      return triOfBoolean((e.min === null || v >= e.min) && (e.max === null || v <= e.max));
  }
}

/**
 * Reference evaluator: plain recursive three-valued evaluation with no
 * caching, no sharing, no short-circuit tricks. Used as the ground truth in
 * the finite-domain equivalence tests.
 */
export function evalExpr(e: Expr, lookup: (attr: string) => unknown): Tri {
  switch (e.kind) {
    case 'lit':
      return e.value;
    case 'cmp':
    case 'in':
    case 'range':
      return evalLeaf(e, lookup);
    case 'not':
      return triNot(evalExpr(e.child, lookup));
    case 'and': {
      let acc: Tri = 'true';
      for (const c of e.children) acc = triAnd(acc, evalExpr(c, lookup));
      return acc;
    }
    case 'or': {
      let acc: Tri = 'false';
      for (const c of e.children) acc = triOr(acc, evalExpr(c, lookup));
      return acc;
    }
  }
}

export function toString(e: Expr): string {
  switch (e.kind) {
    case 'lit':
      return e.value;
    case 'cmp':
      return `${e.attr} ${e.op === 'eq' ? '==' : '!='} ${e.value}`;
    case 'in':
      return `${e.attr} in {${e.set.join(', ')}}`;
    case 'range': {
      const lo = e.min === null ? '-inf' : String(e.min);
      const hi = e.max === null ? '+inf' : String(e.max);
      return `${e.attr} in [${lo}, ${hi}]`;
    }
    case 'not':
      return `NOT (${toString(e.child)})`;
    case 'and':
      return `(${e.children.map(toString).join(' AND ')})`;
    case 'or':
      return `(${e.children.map(toString).join(' OR ')})`;
  }
}
