export type LiteralValue = number | string | boolean | null;

export type Expr =
  | { readonly kind: 'Literal'; readonly value: LiteralValue }
  | { readonly kind: 'Identifier'; readonly name: string }
  | { readonly kind: 'Unary'; readonly op: '!' | '-'; readonly operand: Expr }
  | { readonly kind: 'Binary'; readonly op: BinaryOp; readonly left: Expr; readonly right: Expr }
  | { readonly kind: 'Call'; readonly callee: string; readonly args: readonly Expr[] };

export type BinaryOp =
  | '+'
  | '-'
  | '*'
  | '/'
  | '%'
  | '=='
  | '!='
  | '<'
  | '<='
  | '>'
  | '>='
  | '&&'
  | '||';

/** A parsed, validated expression captured as a first-class value (used by `self` / `eval`). */
export interface QuotedExpr {
  readonly __brand: 'QuotedExpr';
  readonly expr: Expr;
}

export function quote(expr: Expr): QuotedExpr {
  return { __brand: 'QuotedExpr', expr };
}

export function isQuoted(value: unknown): value is QuotedExpr {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as QuotedExpr).__brand === 'QuotedExpr'
  );
}
