import { PolicyError } from './types.ts';
import type { AttrKey } from './types.ts';

export type CmpOp = 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge';

export type CondExpr =
  | { readonly kind: 'cmp'; readonly attr: AttrKey; readonly op: CmpOp; readonly value: number }
  | { readonly kind: 'in'; readonly attr: AttrKey; readonly values: readonly (number | string)[] };

export type Expr =
  | CondExpr
  | { readonly kind: 'and'; readonly args: readonly Expr[] }
  | { readonly kind: 'or'; readonly args: readonly Expr[] }
  | { readonly kind: 'not'; readonly arg: Expr };

export function cmp(attr: AttrKey, op: CmpOp, value: number): Expr {
  return { kind: 'cmp', attr, op, value };
}

export function inSet(attr: AttrKey, values: readonly (number | string)[]): Expr {
  return { kind: 'in', attr, values };
}

export function and(...args: readonly Expr[]): Expr {
  return { kind: 'and', args };
}

export function or(...args: readonly Expr[]): Expr {
  return { kind: 'or', args };
}

export function not(arg: Expr): Expr {
  return { kind: 'not', arg };
}

export function attrsOf(e: Expr): Set<AttrKey> {
  const out = new Set<AttrKey>();
  const walk = (n: Expr): void => {
    switch (n.kind) {
      case 'cmp':
      case 'in':
        out.add(n.attr);
        return;
      case 'not':
        walk(n.arg);
        return;
      case 'and':
      case 'or':
        n.args.forEach(walk);
        return;
    }
  };
  walk(e);
  return out;
}

export function validateExpr(e: Expr, known: ReadonlySet<AttrKey>): void {
  switch (e.kind) {
    case 'cmp':
      if (!known.has(e.attr)) {
        throw new PolicyError(`policy references undeclared attribute: ${e.attr}`);
      }
      if (!Number.isInteger(e.value)) {
        throw new PolicyError(`cmp on "${e.attr}" requires an integer literal`);
      }
      return;
    case 'in':
      if (!known.has(e.attr)) {
        throw new PolicyError(`policy references undeclared attribute: ${e.attr}`);
      }
      if (!Array.isArray(e.values) || e.values.length === 0) {
        throw new PolicyError(`in on "${e.attr}" requires a non-empty finite set`);
      }
      for (const v of e.values) {
        const ok = typeof v === 'string' || (typeof v === 'number' && Number.isInteger(v));
        if (!ok) {
          throw new PolicyError(`in set members for "${e.attr}" must be integers or strings`);
        }
      }
      return;
    case 'not':
      validateExpr(e.arg, known);
      return;
    case 'and':
    case 'or':
      if (e.args.length === 0) {
        throw new PolicyError(`${e.kind} requires at least one argument`);
      }
      for (const a of e.args) {
        validateExpr(a, known);
      }
      return;
  }
}

export function compare(actual: number, op: CmpOp, expected: number): boolean {
  switch (op) {
    case 'eq':
      return actual === expected;
    case 'ne':
      return actual !== expected;
    case 'lt':
      return actual < expected;
    case 'le':
      return actual <= expected;
    case 'gt':
      return actual > expected;
    case 'ge':
      return actual >= expected;
  }
}
