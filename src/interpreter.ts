import { Expr, Row } from './ast';
import { andTable, notTable, orTable } from './logic';
import { compareScalars, describeOperand, resolveOperand } from './operands';
import { EvalResult, triErr, triOk } from './result';

export function interpret(expr: Expr, row: Row): EvalResult {
  return evalAt(expr, row, '$');
}

function evalAt(expr: Expr, row: Row, path: string): EvalResult {
  switch (expr.kind) {
    case 'and': {
      const l = evalAt(expr.left, row, path + '.left');
      const r = evalAt(expr.right, row, path + '.right');
      if (!l.ok) return l;
      if (!r.ok) return r;
      return triOk(andTable[l.value][r.value]);
    }
    case 'or': {
      const l = evalAt(expr.left, row, path + '.left');
      const r = evalAt(expr.right, row, path + '.right');
      if (!l.ok) return l;
      if (!r.ok) return r;
      return triOk(orTable[l.value][r.value]);
    }
    case 'not': {
      const inner = evalAt(expr.arg, row, path + '.arg');
      if (!inner.ok) return inner;
      return triOk(notTable[inner.value]);
    }
    case 'cmp':
      return evalCmp(expr, row, path);
    case 'isNull': {
      const r = resolveOperand(expr.operand, row);
      return triOk(r.state === 'nullish' ? 'true' : 'false');
    }
    case 'isNotNull': {
      const r = resolveOperand(expr.operand, row);
      return triOk(r.state === 'nullish' ? 'false' : 'true');
    }
    case 'value': {
      const r = resolveOperand(expr.operand, row);
      if (r.state === 'nullish') return triOk('unknown');
      if (typeof r.value === 'boolean') return triOk(r.value ? 'true' : 'false');
      return triErr(path, `expected boolean, got ${typeof r.value} from ${describeOperand(expr.operand)}`);
    }
  }
}

function evalCmp(
  expr: Extract<Expr, { kind: 'cmp' }>,
  row: Row,
  path: string,
): EvalResult {
  const l = resolveOperand(expr.left, row);
  const r = resolveOperand(expr.right, row);
  if (l.state === 'nullish' || r.state === 'nullish') return triOk('unknown');
  const outcome = compareScalars(expr.op, l.value, r.value);
  if (outcome === 'type-error') {
    return triErr(
      path,
      `cannot apply ${expr.op} to ${typeof l.value} and ${typeof r.value} ` +
        `(${describeOperand(expr.left)} vs ${describeOperand(expr.right)})`,
    );
  }
  return triOk(outcome);
}
