import { CmpOp, Operand, Row, Scalar } from './ast';
import { Tri } from './result';

export type Resolved =
  | { state: 'value'; value: Scalar }
  | { state: 'nullish' };

export function resolveOperand(operand: Operand, row: Row): Resolved {
  if (operand.kind === 'literal') {
    return operand.value === null
      ? { state: 'nullish' }
      : { state: 'value', value: operand.value };
  }
  const v = row[operand.name];
  if (v === undefined || v === null) return { state: 'nullish' };
  return { state: 'value', value: v };
}

export function describeOperand(operand: Operand): string {
  return operand.kind === 'field' ? `field "${operand.name}"` : `literal ${JSON.stringify(operand.value)}`;
}

export function compareScalars(op: CmpOp, a: Scalar, b: Scalar): Tri | 'type-error' {
  const ta = typeof a;
  const tb = typeof b;
  if (op === 'eq' || op === 'ne') {
    if (ta !== tb) return 'type-error';
    const equal = a === b;
    const result = op === 'eq' ? equal : !equal;
    return result ? 'true' : 'false';
  }
  if (ta === 'number' && tb === 'number') {
    return orderResult(op, a as number, b as number);
  }
  if (ta === 'string' && tb === 'string') {
    return orderResult(op, a as string, b as string);
  }
  return 'type-error';
}

function orderResult(op: CmpOp, a: number | string, b: number | string): Tri {
  let r: boolean;
  switch (op) {
    case 'lt': r = a < b; break;
    case 'lte': r = a <= b; break;
    case 'gt': r = a > b; break;
    case 'gte': r = a >= b; break;
    default: r = false;
  }
  return r ? 'true' : 'false';
}
