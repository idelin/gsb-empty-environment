/** AST definitions for the expression language. */

export type BinOp =
  | "||" | "&&"
  | "==" | "!="
  | "<" | "<=" | ">" | ">="
  | "+" | "-"
  | "*" | "/" | "%";

export type UnOp = "-" | "!";

export type Expr =
  | { kind: "num"; value: number }
  | { kind: "str"; value: string }
  | { kind: "bool"; value: boolean }
  | { kind: "ident"; name: string }
  | { kind: "unary"; op: UnOp; operand: Expr }
  | { kind: "binary"; op: BinOp; left: Expr; right: Expr }
  | { kind: "call"; callee: Expr; args: Expr[] };

export const num = (value: number): Expr => ({ kind: "num", value });
export const str = (value: string): Expr => ({ kind: "str", value });
export const bool = (value: boolean): Expr => ({ kind: "bool", value });
export const ident = (name: string): Expr => ({ kind: "ident", name });
export const unary = (op: UnOp, operand: Expr): Expr => ({ kind: "unary", op, operand });
export const binary = (op: BinOp, left: Expr, right: Expr): Expr => ({ kind: "binary", op, left, right });
export const call = (callee: Expr, args: Expr[]): Expr => ({ kind: "call", callee, args });
