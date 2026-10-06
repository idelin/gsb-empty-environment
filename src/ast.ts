/** AST for formulas: numbers, variables, + - * / and integer powers. */

export type Expr =
  | { kind: "num"; value: number; start: number; end: number }
  | { kind: "var"; name: string; start: number; end: number }
  | { kind: "add"; op: "+" | "-"; left: Expr; right: Expr; start: number; end: number }
  | { kind: "mul"; op: "*" | "/"; left: Expr; right: Expr; start: number; end: number }
  | { kind: "pow"; base: Expr; exponent: number; start: number; end: number };

export function slice(source: string, e: Expr): string {
  return source.slice(e.start, e.end);
}
