import type { BoolExpr, PathAst, Segment } from './ast.ts';
import { parsePath } from './parser.ts';

/**
 * A compiled path expression: a flat list of segment operations executed by
 * the pausable VM in vm.ts. Filter expressions carry a precomputed cost so
 * every evaluation consumes a bounded, predictable share of the budget.
 */
export type SegmentOp =
  | { op: 'key'; name: string }
  | { op: 'index'; index: number }
  | { op: 'wildcard' }
  | { op: 'descend'; name: string }
  | { op: 'filter'; expr: BoolExpr; cost: number };

export interface Plan {
  readonly source: string;
  readonly ops: readonly SegmentOp[];
}

/** Static cost of a filter expression: one unit per evaluated node. */
export function filterCost(expr: BoolExpr): number {
  switch (expr.kind) {
    case 'or':
    case 'and':
      return 1 + expr.items.reduce((n, e) => n + filterCost(e), 0);
    case 'not':
      return 1 + filterCost(expr.item);
    case 'cmp':
      return 1 + operandCost(expr.left) + operandCost(expr.right);
    case 'in':
      return 1 + operandCost(expr.target) + expr.set.length;
  }
}

function operandCost(o: { kind: string; steps?: unknown[] }): number {
  return o.kind === 'self' ? 1 + (o.steps?.length ?? 0) : 1;
}

function compileSegment(seg: Segment): SegmentOp {
  switch (seg.kind) {
    case 'key':
      return { op: 'key', name: seg.name };
    case 'index':
      return { op: 'index', index: seg.index };
    case 'wildcard':
      return { op: 'wildcard' };
    case 'descend':
      return { op: 'descend', name: seg.name };
    case 'filter':
      return { op: 'filter', expr: seg.expr, cost: filterCost(seg.expr) };
  }
}

/** Parse and compile a path expression into an immutable execution plan. */
export function compilePath(source: string): Plan {
  const ast: PathAst = parsePath(source);
  return compileAst(ast);
}

export function compileAst(ast: PathAst): Plan {
  return {
    source: ast.source,
    ops: Object.freeze(ast.segments.map(compileSegment)),
  };
}
