/** AST for the restricted path-expression language. */

export type Literal = number | string | boolean | null;

export type RelStep =
  | { kind: 'key'; name: string }
  | { kind: 'index'; index: number };

/** Operand inside a filter: a literal, or `@` optionally followed by safe steps. */
export type Operand =
  | { kind: 'literal'; value: Literal }
  | { kind: 'self'; steps: RelStep[] };

export type CmpOp = '==' | '!=' | '<' | '<=' | '>' | '>=';

export type BoolExpr =
  | { kind: 'or'; items: BoolExpr[] }
  | { kind: 'and'; items: BoolExpr[] }
  | { kind: 'not'; item: BoolExpr }
  | { kind: 'cmp'; op: CmpOp; left: Operand; right: Operand }
  | { kind: 'in'; target: Operand; set: Literal[] };

export type Segment =
  | { kind: 'key'; name: string }
  | { kind: 'index'; index: number }
  | { kind: 'wildcard' }
  | { kind: 'descend'; name: string }
  | { kind: 'filter'; expr: BoolExpr };

export interface PathAst {
  source: string;
  segments: Segment[];
}
