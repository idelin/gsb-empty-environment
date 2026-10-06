import { Expr, Row, and, boolOf, interpret, lit, not, or } from '../src';

export function prune(expr: Expr, row: Row): Expr {
  switch (expr.kind) {
    case 'and': {
      const left = prune(expr.left, row);
      const lv = interpret(left, row);
      if (lv.ok && lv.value === 'false') return and(left, boolOf(lit(true)));
      return and(left, prune(expr.right, row));
    }
    case 'or': {
      const left = prune(expr.left, row);
      const lv = interpret(left, row);
      if (lv.ok && lv.value === 'true') return or(left, boolOf(lit(false)));
      return or(left, prune(expr.right, row));
    }
    case 'not':
      return not(prune(expr.arg, row));
    default:
      return expr;
  }
}

export function nodeAt(expr: Expr, path: string): Expr | undefined {
  let cur: Expr | undefined = expr;
  const segments = path.split('.').slice(1);
  for (const seg of segments) {
    if (!cur) return undefined;
    if (seg === 'left' && (cur.kind === 'and' || cur.kind === 'or')) cur = cur.left;
    else if (seg === 'right' && (cur.kind === 'and' || cur.kind === 'or')) cur = cur.right;
    else if (seg === 'arg' && cur.kind === 'not') cur = cur.arg;
    else return undefined;
  }
  return cur;
}

export function sameNode(a: Expr | undefined, b: Expr | undefined): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}
