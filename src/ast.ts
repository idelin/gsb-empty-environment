export interface Ident {
  type: 'Ident';
  name: string;
  id: number;
  start: number;
  end: number;
}

export interface NumLit {
  type: 'NumLit';
  value: number;
  start: number;
  end: number;
}

export interface StrLit {
  type: 'StrLit';
  value: string;
  start: number;
  end: number;
}

export interface BinExpr {
  type: 'BinExpr';
  op: string;
  left: Expr;
  right: Expr;
  start: number;
  end: number;
}

export interface CallExpr {
  type: 'CallExpr';
  callee: Expr;
  args: Expr[];
  start: number;
  end: number;
}

export type Expr = Ident | NumLit | StrLit | BinExpr | CallExpr;

export interface LetDecl {
  type: 'LetDecl';
  exported: boolean;
  name: Ident;
  init: Expr;
  start: number;
  end: number;
}

export interface FnDecl {
  type: 'FnDecl';
  exported: boolean;
  name: Ident;
  params: Ident[];
  body: Block;
  start: number;
  end: number;
}

export interface ImportSpec {
  imported: Ident;
  local: Ident | null;
}

export interface ImportDecl {
  type: 'ImportDecl';
  specs: ImportSpec[];
  source: string;
  sourceStart: number;
  sourceEnd: number;
  start: number;
  end: number;
}

export interface Block {
  type: 'Block';
  body: Stmt[];
  start: number;
  end: number;
}

export interface ReturnStmt {
  type: 'ReturnStmt';
  arg: Expr | null;
  start: number;
  end: number;
}

export interface ExprStmt {
  type: 'ExprStmt';
  expr: Expr;
  start: number;
  end: number;
}

export type Stmt = LetDecl | FnDecl | ImportDecl | Block | ReturnStmt | ExprStmt;

export interface Program {
  type: 'Program';
  body: Stmt[];
  idents: Ident[];
  start: number;
  end: number;
}
