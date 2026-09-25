/**
 * Pure filter semantics shared by the compiled plan and the slow reference
 * interpreter, so the differential tests compare control flow only.
 */
import type { CmpOp, FilterExpr, Literal, Operand } from './ast.ts';
import { getOwn } from './safeAccess.ts';

export interface Resolved { present: boolean; value?: unknown }

export const ABSENT: Resolved = { present: false };

export function resolveAt(path: string[], item: unknown): Resolved {
  let cur: unknown = item;
  for (const key of path) {
    const r = getOwn(cur, key);
    if (r.kind !== 'value') return ABSENT;
    cur = r.value;
  }
  return { present: true, value: cur };
}

export function resolveOperand(op: Operand, item: unknown): Resolved {
  switch (op.type) {
    case 'self': return { present: true, value: item };
    case 'field': return resolveAt(op.path, item);
    case 'literal': return { present: true, value: op.value };
  }
}

export function isTruthy(value: unknown): boolean {
  if (value === undefined || value === null || value === false) return false;
  if (value === 0 || value === '') return false;
  if (typeof value === 'number' && Number.isNaN(value)) return false;
  return true;
}

function isOrderedPair(a: unknown, b: unknown): boolean {
  return (typeof a === 'number' && typeof b === 'number')
      || (typeof a === 'string' && typeof b === 'string');
}

/** Absent on either side fails every comparison, including `!=`. */
export function compareValues(op: CmpOp, left: Resolved, right: Resolved): boolean {
  if (!left.present || !right.present) return false;
  const a = left.value;
  const b = right.value;
  switch (op) {
    case '==': return a === b;
    case '!=': return a !== b;
    case '<': return isOrderedPair(a, b) && (a as number) < (b as number);
    case '<=': return isOrderedPair(a, b) && (a as number) <= (b as number);
    case '>': return isOrderedPair(a, b) && (a as number) > (b as number);
    case '>=': return isOrderedPair(a, b) && (a as number) >= (b as number);
  }
}

export function inSet(left: Resolved, set: Literal[]): boolean {
  if (!left.present) return false;
  return set.some((v) => v === left.value);
}

/** One non-generator filter evaluation step, shared by both engines. */
export function evalFilterSync(expr: FilterExpr, item: unknown): boolean {
  switch (expr.type) {
    case 'or': return expr.items.some((e) => evalFilterSync(e, item));
    case 'and': return expr.items.every((e) => evalFilterSync(e, item));
    case 'not': return !evalFilterSync(expr.expr, item);
    case 'exists': return resolveAt(expr.path, item).present;
    case 'truthy': {
      const r = resolveAt(expr.path, item);
      return r.present && isTruthy(r.value);
    }
    case 'compare':
      return compareValues(expr.op, resolveOperand(expr.left, item), resolveOperand(expr.right, item));
    case 'in':
      return inSet(resolveOperand(expr.left, item), expr.set);
  }
}
