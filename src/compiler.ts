import { Expr, Row } from './ast';
import { compareScalars, describeOperand, resolveOperand } from './operands';
import { EvalResult, FALSE, TRUE, UNKNOWN, triErr, triOk } from './result';

export type RowPredicate = (row: Row) => EvalResult;

const constant = (result: EvalResult): RowPredicate => () => result;

export function compile(expr: Expr): RowPredicate {
  return compileAt(expr, '$');
}

function compileAt(expr: Expr, path: string): RowPredicate {
  switch (expr.kind) {
    case 'and': {
      const l = compileAt(expr.left, path + '.left');
      const r = compileAt(expr.right, path + '.right');
      return (row) => {
        const lr = l(row);
        if (!lr.ok) return lr;
        if (lr.value === 'false') return FALSE;
        if (lr.value === 'true') return r(row);
        const rr = r(row);
        if (!rr.ok) return rr;
        return rr.value === 'false' ? FALSE : UNKNOWN;
      };
    }
    case 'or': {
      const l = compileAt(expr.left, path + '.left');
      const r = compileAt(expr.right, path + '.right');
      return (row) => {
        const lr = l(row);
        if (!lr.ok) return lr;
        if (lr.value === 'true') return TRUE;
        if (lr.value === 'false') return r(row);
        const rr = r(row);
        if (!rr.ok) return rr;
        return rr.value === 'true' ? TRUE : UNKNOWN;
      };
    }
    case 'not': {
      const inner = compileAt(expr.arg, path + '.arg');
      return (row) => {
        const r = inner(row);
        if (!r.ok) return r;
        if (r.value === 'unknown') return UNKNOWN;
        return r.value === 'true' ? FALSE : TRUE;
      };
    }
    case 'cmp':
      return compileCmp(expr, path);
    case 'isNull': {
      const { operand } = expr;
      if (operand.kind === 'literal') return constant(triOk(operand.value === null ? 'true' : 'false'));
      return (row) => triOk(resolveOperand(operand, row).state === 'nullish' ? 'true' : 'false');
    }
    case 'isNotNull': {
      const { operand } = expr;
      if (operand.kind === 'literal') return constant(triOk(operand.value === null ? 'false' : 'true'));
      return (row) => triOk(resolveOperand(operand, row).state === 'nullish' ? 'false' : 'true');
    }
    case 'value': {
      const { operand } = expr;
      if (operand.kind === 'literal') return constant(coerceLiteral(operand.value, path));
      return (row) => {
        const r = resolveOperand(operand, row);
        if (r.state === 'nullish') return UNKNOWN;
        if (typeof r.value === 'boolean') return r.value ? TRUE : FALSE;
        return triErr(path, `expected boolean, got ${typeof r.value} from ${describeOperand(operand)}`);
      };
    }
  }
}

function coerceLiteral(value: null | string | number | boolean, path: string): EvalResult {
  if (value === null) return UNKNOWN;
  if (typeof value === 'boolean') return value ? TRUE : FALSE;
  return triErr(path, `expected boolean, got ${typeof value} from literal ${JSON.stringify(value)}`);
}

function compileCmp(expr: Extract<Expr, { kind: 'cmp' }>, path: string): RowPredicate {
  const { op, left, right } = expr;
  if (left.kind === 'literal' && right.kind === 'literal') {
    return constant(evaluateConstantCmp(op, left.value, right.value, path, expr));
  }
  return (row) => {
    const l = resolveOperand(left, row);
    const r = resolveOperand(right, row);
    if (l.state === 'nullish' || r.state === 'nullish') return UNKNOWN;
    const outcome = compareScalars(op, l.value, r.value);
    if (outcome === 'type-error') return triErr(path, cmpErrorMessage(expr, typeof l.value, typeof r.value));
    return triOk(outcome);
  };
}

function evaluateConstantCmp(
  op: Extract<Expr, { kind: 'cmp' }>['op'],
  lv: null | string | number | boolean,
  rv: null | string | number | boolean,
  path: string,
  expr: Extract<Expr, { kind: 'cmp' }>,
): EvalResult {
  if (lv === null || rv === null) return UNKNOWN;
  const outcome = compareScalars(op, lv, rv);
  if (outcome === 'type-error') return triErr(path, cmpErrorMessage(expr, typeof lv, typeof rv));
  return triOk(outcome);
}

function cmpErrorMessage(
  expr: Extract<Expr, { kind: 'cmp' }>,
  lt: string,
  rt: string,
): string {
  return (
    `cannot apply ${expr.op} to ${lt} and ${rt} ` +
    `(${describeOperand(expr.left)} vs ${describeOperand(expr.right)})`
  );
}
