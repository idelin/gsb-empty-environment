export interface BaseNode {
  id: number;
  kind: string;
  start: number;
  end: number;
  line: number;
}

export type Stmt =
  | LetStmt
  | IfStmt
  | WhileStmt
  | FnDeclStmt
  | ReturnStmt
  | BlockStmt
  | ExprStmt;

export type Expr =
  | NumLit
  | StrLit
  | BoolLit
  | NullLit
  | Ident
  | UnaryExpr
  | BinaryExpr
  | AssignExpr
  | CallExpr;

export interface LetStmt extends BaseNode {
  kind: 'Let';
  name: string;
  init: Expr;
}

export interface IfStmt extends BaseNode {
  kind: 'If';
  cond: Expr;
  thenBranch: Stmt;
  elseBranch: Stmt | null;
}

export interface WhileStmt extends BaseNode {
  kind: 'While';
  cond: Expr;
  body: Stmt;
}

export interface FnDeclStmt extends BaseNode {
  kind: 'FnDecl';
  name: string;
  params: string[];
  body: BlockStmt;
}

export interface ReturnStmt extends BaseNode {
  kind: 'Return';
  value: Expr | null;
}

export interface BlockStmt extends BaseNode {
  kind: 'Block';
  body: Stmt[];
}

export interface ExprStmt extends BaseNode {
  kind: 'ExprStmt';
  expr: Expr;
}

export interface NumLit extends BaseNode {
  kind: 'Num';
  value: number;
}

export interface StrLit extends BaseNode {
  kind: 'Str';
  value: string;
}

export interface BoolLit extends BaseNode {
  kind: 'Bool';
  value: boolean;
}

export interface NullLit extends BaseNode {
  kind: 'Null';
}

export interface Ident extends BaseNode {
  kind: 'Ident';
  name: string;
}

export interface UnaryExpr extends BaseNode {
  kind: 'Unary';
  op: string;
  arg: Expr;
}

export interface BinaryExpr extends BaseNode {
  kind: 'Binary';
  op: string;
  left: Expr;
  right: Expr;
}

export interface AssignExpr extends BaseNode {
  kind: 'Assign';
  name: string;
  value: Expr;
}

export interface CallExpr extends BaseNode {
  kind: 'Call';
  callee: Expr;
  args: Expr[];
}

export interface Program extends BaseNode {
  kind: 'Program';
  body: Stmt[];
}

export type AnyNode = Program | Stmt | Expr;

export function childrenOf(node: AnyNode): AnyNode[] {
  switch (node.kind) {
    case 'Program':
    case 'Block':
      return [...node.body];
    case 'Let':
      return [node.init];
    case 'If': {
      const out: AnyNode[] = [node.cond, node.thenBranch];
      if (node.elseBranch) out.push(node.elseBranch);
      return out;
    }
    case 'While':
      return [node.cond, node.body];
    case 'FnDecl':
      return [node.body];
    case 'Return':
      return node.value ? [node.value] : [];
    case 'ExprStmt':
      return [node.expr];
    case 'Unary':
      return [node.arg];
    case 'Binary':
      return [node.left, node.right];
    case 'Assign':
      return [node.value];
    case 'Call':
      return [node.callee, ...node.args];
    default:
      return [];
  }
}

export function walkAst(node: AnyNode, visit: (n: AnyNode) => void): void {
  visit(node);
  for (const child of childrenOf(node)) walkAst(child, visit);
}
