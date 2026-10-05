/**
 * Expression AST for fixed-width bit-vector arithmetic.
 *
 * Every expression is evaluated at a single global bit width; all results are
 * truncated to that width. Shift amounts greater than or equal to the width
 * produce zero (a fixed, total semantics — no undefined behavior).
 */

export type BinaryOp =
  | 'and' | 'or' | 'xor'
  | 'add' | 'sub'
  | 'shl' | 'lshr' | 'ashr';

export type Expr =
  | { readonly kind: 'var'; readonly name: string }
  | { readonly kind: 'const'; readonly value: bigint }
  | { readonly kind: 'not'; readonly arg: Expr }
  | { readonly kind: 'bin'; readonly op: BinaryOp; readonly lhs: Expr; readonly rhs: Expr }
  | { readonly kind: 'ite'; readonly cond: Expr; readonly then: Expr; readonly otherwise: Expr };

export const v = (name: string): Expr => ({ kind: 'var', name });
export const c = (value: bigint | number): Expr => ({ kind: 'const', value: BigInt(value) });
export const not = (arg: Expr): Expr => ({ kind: 'not', arg });
export const bin = (op: BinaryOp, lhs: Expr, rhs: Expr): Expr => ({ kind: 'bin', op, lhs, rhs });
export const ite = (cond: Expr, then: Expr, otherwise: Expr): Expr =>
  ({ kind: 'ite', cond, then, otherwise });

export const and = (a: Expr, b: Expr): Expr => bin('and', a, b);
export const or = (a: Expr, b: Expr): Expr => bin('or', a, b);
export const xor = (a: Expr, b: Expr): Expr => bin('xor', a, b);
export const add = (a: Expr, b: Expr): Expr => bin('add', a, b);
export const sub = (a: Expr, b: Expr): Expr => bin('sub', a, b);
export const shl = (a: Expr, b: Expr): Expr => bin('shl', a, b);
export const lshr = (a: Expr, b: Expr): Expr => bin('lshr', a, b);
export const ashr = (a: Expr, b: Expr): Expr => bin('ashr', a, b);

/** Variable names occurring in `e`, sorted for deterministic ordering. */
export function collectVars(e: Expr, into: Set<string> = new Set()): Set<string> {
  switch (e.kind) {
    case 'var':
      into.add(e.name);
      return into;
    case 'const':
      return into;
    case 'not':
      return collectVars(e.arg, into);
    case 'bin':
      collectVars(e.lhs, into);
      return collectVars(e.rhs, into);
    case 'ite':
      collectVars(e.cond, into);
      collectVars(e.then, into);
      return collectVars(e.otherwise, into);
  }
}

export function show(e: Expr): string {
  switch (e.kind) {
    case 'var':
      return e.name;
    case 'const':
      return `${e.value}`;
    case 'not':
      return `(~${show(e.arg)})`;
    case 'bin':
      return `(${show(e.lhs)} ${e.op} ${show(e.rhs)})`;
    case 'ite':
      return `(ite ${show(e.cond)} ${show(e.then)} ${show(e.otherwise)})`;
  }
}
