export interface Span {
  start: number;
  end: number;
}

export interface Program {
  kind: "Program";
  body: Stmt[];
  span: Span;
}

export interface Block {
  kind: "Block";
  body: Stmt[];
  span: Span;
}

export interface LetDecl {
  kind: "LetDecl";
  name: string;
  nameSpan: Span;
  init: Expr;
  span: Span;
}

export interface ExprStmt {
  kind: "ExprStmt";
  expr: Expr;
  span: Span;
}

export interface Ident {
  kind: "Ident";
  text: string;
  span: Span;
}

export interface NumLit {
  kind: "NumLit";
  value: number;
  span: Span;
}

export interface Binary {
  kind: "Binary";
  op: string;
  left: Expr;
  right: Expr;
  span: Span;
}

export type Stmt = Block | LetDecl | ExprStmt;
export type Expr = Ident | NumLit | Binary;
export type Node = Program | Stmt | Expr;

export function childrenOf(node: Node): Node[] {
  switch (node.kind) {
    case "Program":
    case "Block":
      return node.body;
    case "LetDecl":
      return [node.init];
    case "ExprStmt":
      return [node.expr];
    case "Binary":
      return [node.left, node.right];
    default:
      return [];
  }
}
