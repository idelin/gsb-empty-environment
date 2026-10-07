import { Expr, Row, Value } from './ast';
import { EvalError } from './errors';
import { Tri, triAnd, triNot, triOr } from './tri';
import { evaluate } from './evaluate';

/** A type error found by the reference interpreter, with its position. */
export interface RefError {
  readonly path: readonly string[];
  readonly message: string;
}

/**
 * An expression tree annotated with truth-table values. `errors` holds only
 * the node's own type errors (leaf comparisons); use `reachableErrors` to
 * project the errors a short-circuiting run would observe.
 */
export interface EvalTree {
  readonly expr: Expr;
  readonly path: readonly string[];
  readonly value: Tri;
  readonly errors: readonly RefError[];
  readonly children: readonly EvalTree[];
}

/**
 * Truth-table interpreter: evaluates every branch (no short-circuit) and
 * combines results with the Kleene truth tables. Type errors are collected
 * instead of thrown; a node that fails to compare contributes 'U' upward so
 * the rest of the tree can still be valued.
 */
export const evaluateAll = (expr: Expr, row: Row, path: readonly string[] = []): EvalTree => {
  const node = (
    value: Tri,
    errors: readonly RefError[],
    children: readonly EvalTree[] = [],
  ): EvalTree => ({ expr, path, value, errors, children });

  switch (expr.kind) {
    case 'and':
    case 'or': {
      const left = evaluateAll(expr.left, row, [...path, 'left']);
      const right = evaluateAll(expr.right, row, [...path, 'right']);
      const combine = expr.kind === 'and' ? triAnd : triOr;
      return node(combine(left.value, right.value), [], [left, right]);
    }
    case 'not': {
      const operand = evaluateAll(expr.operand, row, [...path, 'operand']);
      return node(triNot(operand.value), [], [operand]);
    }
    case 'cmp':
    case 'isNull': {
      try {
        return node(evaluate(expr, row, path), []);
      } catch (err) {
        if (err instanceof EvalError) {
          return node('U', [{ path: err.path, message: err.message }]);
        }
        throw err;
      }
    }
  }
};

/**
 * Errors the short-circuiting evaluator is expected to raise, in
 * left-to-right evaluation order. A branch is reachable unless its parent
 * connective has already decided the result: AND skips the right branch on
 * false, OR skips it on true.
 */
export const reachableErrors = (tree: EvalTree): RefError[] => {
  const own = [...tree.errors];
  const { expr, children } = tree;
  switch (expr.kind) {
    case 'and': {
      const fromLeft = reachableErrors(children[0]);
      const skipRight = children[0].value === 'F';
      return [...own, ...fromLeft, ...(skipRight ? [] : reachableErrors(children[1]))];
    }
    case 'or': {
      const fromLeft = reachableErrors(children[0]);
      const skipRight = children[0].value === 'T';
      return [...own, ...fromLeft, ...(skipRight ? [] : reachableErrors(children[1]))];
    }
    case 'not':
      return [...own, ...reachableErrors(children[0])];
    default:
      return own;
  }
};

export interface Expected {
  readonly value: Tri;
  readonly errors: readonly RefError[];
}

/**
 * Expected observable behaviour of `evaluate` for one (expr, row) pair:
 * the truth-table value, plus the type errors positioned at reachable nodes
 * in evaluation order. If `errors` is non-empty, `evaluate` must throw an
 * `EvalError` whose path equals `errors[0].path`; otherwise it must return
 * `value`.
 */
export const expectedOutcome = (expr: Expr, row: Row): Expected => {
  const tree = evaluateAll(expr, row);
  return { value: tree.value, errors: reachableErrors(tree) };
};
