export type Scalar = number | string | boolean;
export type Value = Scalar | null;
export type Row = Record<string, Value | undefined>;

export type CmpOp = 'eq' | 'ne' | 'lt' | 'lte' | 'gt' | 'gte';

export type Operand =
  | { kind: 'field'; name: string }
  | { kind: 'literal'; value: Value };

export type Expr =
  | { kind: 'and'; left: Expr; right: Expr }
  | { kind: 'or'; left: Expr; right: Expr }
  | { kind: 'not'; arg: Expr }
  | { kind: 'cmp'; op: CmpOp; left: Operand; right: Operand }
  | { kind: 'isNull'; operand: Operand }
  | { kind: 'isNotNull'; operand: Operand }
  | { kind: 'value'; operand: Operand };

export const field = (name: string): Operand => ({ kind: 'field', name });
export const lit = (value: Value): Operand => ({ kind: 'literal', value });
export const and = (left: Expr, right: Expr): Expr => ({ kind: 'and', left, right });
export const or = (left: Expr, right: Expr): Expr => ({ kind: 'or', left, right });
export const not = (arg: Expr): Expr => ({ kind: 'not', arg });
export const cmp = (op: CmpOp, left: Operand, right: Operand): Expr => ({ kind: 'cmp', op, left, right });
export const isNull = (operand: Operand): Expr => ({ kind: 'isNull', operand });
export const isNotNull = (operand: Operand): Expr => ({ kind: 'isNotNull', operand });
export const boolOf = (operand: Operand): Expr => ({ kind: 'value', operand });
