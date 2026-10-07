export type Scalar = string | number | boolean;
export type Value = Scalar | null;

export type CmpOp = 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge';

export type Operand =
  | { kind: 'field'; name: string }
  | { kind: 'literal'; value: Value };

export type Expr =
  | { kind: 'and' | 'or'; left: Expr; right: Expr }
  | { kind: 'not'; operand: Expr }
  | { kind: 'cmp'; op: CmpOp; left: Operand; right: Operand }
  | { kind: 'isNull'; operand: Operand; negated: boolean };

/** A data row. A missing key and an explicit `null` are both "absent". */
export type Row = Record<string, Value | undefined>;

export const field = (name: string): Operand => ({ kind: 'field', name });
export const lit = (value: Value): Operand => ({ kind: 'literal', value });
export const and = (left: Expr, right: Expr): Expr => ({ kind: 'and', left, right });
export const or = (left: Expr, right: Expr): Expr => ({ kind: 'or', left, right });
export const not = (operand: Expr): Expr => ({ kind: 'not', operand });
export const cmp = (op: CmpOp, left: Operand, right: Operand): Expr => ({
  kind: 'cmp',
  op,
  left,
  right,
});
export const isNull = (operand: Operand): Expr => ({ kind: 'isNull', operand, negated: false });
export const isNotNull = (operand: Operand): Expr => ({ kind: 'isNull', operand, negated: true });
