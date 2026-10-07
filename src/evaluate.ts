import { CmpOp, Expr, Operand, Row, Value } from './ast';
import { EvalError } from './errors';
import { Tri, triAnd, triNot, triOf, triOr } from './tri';

const isAbsent = (v: Value | undefined): v is null | undefined =>
  v === null || v === undefined;

const resolveOperand = (operand: Operand, row: Row): Value | undefined =>
  operand.kind === 'field' ? row[operand.name] : operand.value;

const typeName = (v: Value | undefined): string =>
  v === undefined ? 'missing' : v === null ? 'null' : typeof v;

const compare = (op: CmpOp, a: Value | undefined, b: Value | undefined, path: readonly string[]): Tri => {
  // Missing fields and nulls make the comparison unknown, never an error.
  if (isAbsent(a) || isAbsent(b)) return 'U';
  if (typeof a !== typeof b) {
    throw new EvalError(path, `type mismatch: cannot compare ${typeName(a)} with ${typeName(b)}`);
  }
  if (typeof a === 'boolean' && op !== 'eq' && op !== 'ne') {
    throw new EvalError(path, `type error: cannot order booleans with '${op}'`);
  }
  switch (op) {
    case 'eq': return triOf(a === b);
    case 'ne': return triOf(a !== b);
    case 'lt': return triOf(a < (b as typeof a));
    case 'le': return triOf(a <= (b as typeof a));
    case 'gt': return triOf(a > (b as typeof a));
    case 'ge': return triOf(a >= (b as typeof a));
  }
};

/**
 * Short-circuiting three-valued evaluator.
 *
 * AND skips its right branch when the left is false; OR skips it when the
 * left is true. A skipped branch is never evaluated, so its field accesses
 * and type errors never happen. Errors in branches that are evaluated
 * propagate immediately, preserving left-to-right error priority.
 */
export const evaluate = (expr: Expr, row: Row, path: readonly string[] = []): Tri => {
  switch (expr.kind) {
    case 'and': {
      const left = evaluate(expr.left, row, [...path, 'left']);
      if (left === 'F') return 'F';
      const right = evaluate(expr.right, row, [...path, 'right']);
      return triAnd(left, right);
    }
    case 'or': {
      const left = evaluate(expr.left, row, [...path, 'left']);
      if (left === 'T') return 'T';
      const right = evaluate(expr.right, row, [...path, 'right']);
      return triOr(left, right);
    }
    case 'not':
      return triNot(evaluate(expr.operand, row, [...path, 'operand']));
    case 'cmp':
      return compare(
        expr.op,
        resolveOperand(expr.left, row),
        resolveOperand(expr.right, row),
        path,
      );
    case 'isNull': {
      const absent = isAbsent(resolveOperand(expr.operand, row));
      return triOf(expr.negated ? !absent : absent);
    }
  }
};

/** Selects exactly the rows for which the predicate evaluates to true. */
export const filterRows = (rows: readonly Row[], expr: Expr): Row[] =>
  rows.filter((row) => evaluate(expr, row) === 'T');
