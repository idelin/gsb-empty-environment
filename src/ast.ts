export interface Span {
  start: number;
  end: number;
}

export type Stmt = LetStmt | BlockStmt | ExprStmt;

export interface LetStmt {
  kind: 'let';
  name: string;
  nameSpan: Span;
  init: Expr;
  span: Span;
}

export interface BlockStmt {
  kind: 'block';
  body: Stmt[];
  span: Span;
}

export interface ExprStmt {
  kind: 'expr';
  expr: Expr;
  span: Span;
}

export type Expr = IdentExpr | NumberExpr | BinaryExpr;

export interface IdentExpr {
  kind: 'ident';
  name: string;
  span: Span;
}

export interface NumberExpr {
  kind: 'num';
  value: number;
  span: Span;
}

export interface BinaryExpr {
  kind: 'binary';
  op: '+' | '*';
  left: Expr;
  right: Expr;
  span: Span;
}

export interface Program {
  body: Stmt[];
  span: Span;
}
