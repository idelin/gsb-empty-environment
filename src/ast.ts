export interface Ident {
  kind: 'Ident';
  name: string;
  start: number;
  end: number;
}

export interface NumLit {
  kind: 'Num';
  start: number;
  end: number;
}

export interface StrLit {
  kind: 'Str';
  value: string;
  start: number;
  end: number;
}

export interface Binary {
  kind: 'Binary';
  op: string;
  left: Expr;
  right: Expr;
  start: number;
  end: number;
}

export interface Call {
  kind: 'Call';
  callee: Ident;
  args: Expr[];
  start: number;
  end: number;
}

export interface Assign {
  kind: 'Assign';
  target: Ident;
  value: Expr;
  start: number;
  end: number;
}

export type Expr = Ident | NumLit | StrLit | Binary | Call | Assign;

export interface LetDecl {
  kind: 'Let';
  id: Ident;
  init: Expr;
  start: number;
  end: number;
}

export interface FnDecl {
  kind: 'Fn';
  id: Ident;
  params: Ident[];
  body: Block;
  start: number;
  end: number;
}

export interface ImportSpec {
  imported: Ident;
  local: Ident;
}

export interface ImportDecl {
  kind: 'Import';
  specs: ImportSpec[];
  source: string;
  start: number;
  end: number;
}

export interface Block {
  kind: 'Block';
  body: Stmt[];
  start: number;
  end: number;
}

export interface ReturnStmt {
  kind: 'Return';
  arg: Expr;
  start: number;
  end: number;
}

export interface ExprStmt {
  kind: 'ExprStmt';
  expr: Expr;
  start: number;
  end: number;
}

export type Stmt = LetDecl | FnDecl | ImportDecl | Block | ReturnStmt | ExprStmt;

export interface Program {
  kind: 'Program';
  body: Stmt[];
  start: number;
  end: number;
}
