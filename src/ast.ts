// AST for the small language, plus the transformed (boxed) AST the compiler emits.
//
// Surface language:
//   - mutable locals via `let`, updated via assignment
//   - nested function declarations; functions are values and may escape
//   - if / while / return / print / expression statements
// Scoping rules (shared by both interpreters):
//   - only function bodies and while-loop *iterations* introduce scopes
//   - a `let` inside a loop body is fresh per iteration
//   - no shadowing: names declared in one function must be distinct

export type BinOp = '+' | '-' | '*' | '<' | '<=' | '>' | '>=' | '==' | '!=';

export type Expr =
  | { tag: 'num'; value: number }
  | { tag: 'bool'; value: boolean }
  | { tag: 'null' }
  | { tag: 'var'; name: string }
  | { tag: 'bin'; op: BinOp; left: Expr; right: Expr }
  | { tag: 'call'; fn: Expr; args: Expr[] };

export type Stmt =
  | { tag: 'let'; name: string; init: Expr }
  | { tag: 'assign'; name: string; value: Expr }
  | { tag: 'func'; name: string; params: string[]; body: Stmt[] }
  | { tag: 'if'; cond: Expr; then: Stmt[]; else: Stmt[] }
  | { tag: 'while'; cond: Expr; body: Stmt[] }
  | { tag: 'return'; value: Expr }
  | { tag: 'print'; value: Expr }
  | { tag: 'expr'; value: Expr };

export type Program = Stmt[];

// ---- Transformed AST ----
// `let` is annotated with whether the local is heap-boxed.
// `func` carries an explicit capture list plus which params need boxing.

export interface Capture {
  name: string;
  by: 'box' | 'value';
}

export type TStmt =
  | { tag: 'let'; name: string; init: Expr; boxed: boolean }
  | { tag: 'assign'; name: string; value: Expr }
  | {
      tag: 'func';
      name: string;
      params: string[];
      boxedParams: string[];
      captures: Capture[];
      selfRef: boolean;
      body: TStmt[];
    }
  | { tag: 'if'; cond: Expr; then: TStmt[]; else: TStmt[] }
  | { tag: 'while'; cond: Expr; body: TStmt[] }
  | { tag: 'return'; value: Expr }
  | { tag: 'print'; value: Expr }
  | { tag: 'expr'; value: Expr };

export type TProgram = TStmt[];

// ---- Builders (used by tests and embedders) ----

export const num = (value: number): Expr => ({ tag: 'num', value });
export const bool = (value: boolean): Expr => ({ tag: 'bool', value });
export const nil: Expr = { tag: 'null' };
export const v = (name: string): Expr => ({ tag: 'var', name });
export const bin = (op: BinOp, left: Expr, right: Expr): Expr => ({ tag: 'bin', op, left, right });
export const call = (fn: Expr, ...args: Expr[]): Expr => ({ tag: 'call', fn, args });

export const let_ = (name: string, init: Expr): Stmt => ({ tag: 'let', name, init });
export const set = (name: string, value: Expr): Stmt => ({ tag: 'assign', name, value });
export const func = (name: string, params: string[], ...body: Stmt[]): Stmt => ({
  tag: 'func',
  name,
  params,
  body,
});
export const if_ = (cond: Expr, then: Stmt[], els: Stmt[] = []): Stmt => ({
  tag: 'if',
  cond,
  then,
  else: els,
});
export const while_ = (cond: Expr, ...body: Stmt[]): Stmt => ({ tag: 'while', cond, body });
export const ret = (value: Expr): Stmt => ({ tag: 'return', value });
export const print = (value: Expr): Stmt => ({ tag: 'print', value });
export const expr = (value: Expr): Stmt => ({ tag: 'expr', value });
