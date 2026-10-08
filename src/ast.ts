// Whitelisted rule AST. There is deliberately no node kind for eval,
// host-function calls or dynamic imports: anything outside this union is
// rejected as ForbiddenNode before it can execute.

export type Value = bigint | boolean | Value[];

export type Expr =
  | { kind: 'int'; value: string | number | bigint } // signed 64-bit integer literal
  | { kind: 'bool'; value: boolean }
  | { kind: 'var'; name: string }
  | { kind: 'neg'; arg: Expr }
  | { kind: 'arith'; op: 'add' | 'sub' | 'mul' | 'div' | 'mod'; left: Expr; right: Expr }
  | { kind: 'cmp'; op: 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge'; left: Expr; right: Expr }
  | { kind: 'and'; args: Expr[] } // short-circuit
  | { kind: 'or'; args: Expr[] } // short-circuit
  | { kind: 'not'; arg: Expr }
  | { kind: 'if'; cond: Expr; then: Expr; else: Expr }
  | { kind: 'map'; list: Expr; param: string; body: Expr }
  | { kind: 'filter'; list: Expr; param: string; body: Expr }
  | { kind: 'fold'; list: Expr; param: string; acc: string; init: Expr; body: Expr };
