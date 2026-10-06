// Source-language AST. The language has nested first-class functions,
// mutable locals (`let`), immutable locals (`const`), arrays, and closures
// that may escape their defining scope.

export type BinOp =
  | '+' | '-' | '*' | '%'
  | '<' | '<=' | '>' | '>='
  | '==' | '!='
  | 'and' | 'or';

export interface NumExpr { kind: 'num'; value: number }
export interface BoolExpr { kind: 'bool'; value: boolean }
export interface VarExpr { kind: 'var'; name: string; decl?: Decl }
export interface BinExpr { kind: 'bin'; op: BinOp; left: Expr; right: Expr }
export interface NotExpr { kind: 'not'; expr: Expr }
export interface CallExpr { kind: 'call'; fn: Expr; args: Expr[] }
export interface FnExpr {
  kind: 'fn';
  selfName: string | null; // optional self-binding for recursion
  params: string[];
  body: Stmt[];
}
export interface ArrExpr { kind: 'arr'; items: Expr[] }
export interface IndexExpr { kind: 'index'; arr: Expr; index: Expr }
export interface PushExpr { kind: 'push'; arr: Expr; value: Expr }
export interface LenExpr { kind: 'len'; arr: Expr }

export type Expr =
  | NumExpr | BoolExpr | VarExpr | BinExpr | NotExpr | CallExpr
  | FnExpr | ArrExpr | IndexExpr | PushExpr | LenExpr;

export interface LetStmt {
  kind: 'let';
  mutable: boolean; // false => const
  name: string;
  init: Expr;
  decl?: Decl;
}
export interface SetStmt { kind: 'set'; name: string; value: Expr; decl?: Decl }
export interface ExprStmt { kind: 'expr'; expr: Expr }
export interface BlockStmt { kind: 'block'; body: Stmt[] }
export interface IfStmt { kind: 'if'; cond: Expr; then: Stmt[]; else: Stmt[] }
export interface WhileStmt { kind: 'while'; cond: Expr; body: Stmt[] }
export interface ReturnStmt { kind: 'return'; expr: Expr | null }
export interface PrintStmt { kind: 'print'; expr: Expr }

export type Stmt =
  | LetStmt | SetStmt | ExprStmt | BlockStmt
  | IfStmt | WhileStmt | ReturnStmt | PrintStmt;

export interface Program { body: Stmt[] }

// A Decl is the resolved identity of a binding. The resolver attaches Decls
// to VarExpr / SetStmt / LetStmt nodes; `owner` is the lexically enclosing
// function (null = top level). `captured` is set when some nested function
// references the binding.
export interface Decl {
  id: number;
  name: string;
  mutable: boolean;
  isParam: boolean;
  isSelf: boolean;
  owner: FnExpr | null;
  captured: boolean;
}
