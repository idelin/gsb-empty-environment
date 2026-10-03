// AST node definitions for a small expression language, plus the shared
// precedence table used by both the parser and the pretty printer.

export type Expr =
  | { kind: 'num'; value: number }
  | { kind: 'str'; value: string }
  | { kind: 'ident'; name: string }
  | { kind: 'unary'; op: string; argument: Expr }
  | { kind: 'binary'; op: string; left: Expr; right: Expr }
  | { kind: 'cond'; test: Expr; consequent: Expr; alternate: Expr }
  | { kind: 'call'; callee: Expr; args: Expr[] }
  | { kind: 'member'; object: Expr; property: string }
  | { kind: 'index'; object: Expr; index: Expr }
  | { kind: 'array'; elements: Expr[] }
  | { kind: 'object'; props: Prop[] };

export interface Prop {
  key: string;
  value: Expr;
}

// Larger number binds tighter. Shared by parser (Pratt) and printer (parens).
export const TERNARY_PREC = 2;

export const BINARY_PREC: Record<string, number> = {
  '||': 4,
  '&&': 5,
  '|': 6,
  '^': 7,
  '&': 8,
  '==': 9,
  '!=': 9,
  '===': 9,
  '!==': 9,
  '<': 10,
  '<=': 10,
  '>': 10,
  '>=': 10,
  in: 10,
  instanceof: 10,
  '<<': 11,
  '>>': 11,
  '>>>': 11,
  '+': 12,
  '-': 12,
  '*': 13,
  '/': 13,
  '%': 13,
  '**': 14,
};

export const UNARY_PREC = 15;
export const POSTFIX_PREC = 17;

export const RIGHT_ASSOC_OPS = new Set(['**']);
