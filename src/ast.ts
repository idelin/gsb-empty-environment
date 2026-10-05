// AST for the teaching language: blocks, sequential declarations,
// int/bool expressions, variable references, nested functions with
// parameter declarations. Node ids are assigned once at construction
// and never change when a node is moved inside the tree.

export type NodeId = string;
export type TypeName = 'int' | 'bool';

export interface Diagnostic {
  nodeId: NodeId;
  attr: string;
  message: string;
}

interface Base {
  id: NodeId;
  kind: string;
}

export interface Program extends Base {
  kind: 'program';
  body: Block;
}
export interface Block extends Base {
  kind: 'block';
  stmts: Stmt[];
}
export interface ConstDecl extends Base {
  kind: 'const';
  name: string;
  init: Expr;
}
export interface FuncDecl extends Base {
  kind: 'func';
  name: string;
  params: Param[];
  retType: TypeName;
  body: Block;
}
export interface Param extends Base {
  kind: 'param';
  name: string;
  paramType: TypeName;
}
export interface ExprStmt extends Base {
  kind: 'exprStmt';
  expr: Expr;
}
export type Stmt = ConstDecl | FuncDecl | ExprStmt | Block;

export interface IntLit extends Base {
  kind: 'int';
  value: number;
}
export interface BoolLit extends Base {
  kind: 'bool';
  value: boolean;
}
export interface Ref extends Base {
  kind: 'ref';
  name: string;
}
export type BinOp = '+' | '-' | '*' | '<' | '<=' | '>' | '>=' | '==' | '!=' | '&&' | '||';
export interface Bin extends Base {
  kind: 'bin';
  op: BinOp;
  left: Expr;
  right: Expr;
}
export type UnOp = '-' | '!';
export interface Un extends Base {
  kind: 'un';
  op: UnOp;
  expr: Expr;
}
export type Expr = IntLit | BoolLit | Ref | Bin | Un;

export type AnyNode =
  | Program
  | Block
  | ConstDecl
  | FuncDecl
  | Param
  | ExprStmt
  | IntLit
  | BoolLit
  | Ref
  | Bin
  | Un;

const STMT_KINDS = new Set(['const', 'func', 'exprStmt', 'block']);

export function isStmt(node: AnyNode): boolean {
  return STMT_KINDS.has(node.kind);
}

export function isExpr(node: AnyNode): boolean {
  return node.kind === 'int' || node.kind === 'bool' || node.kind === 'ref' || node.kind === 'bin' || node.kind === 'un';
}

export function childrenOf(node: AnyNode): AnyNode[] {
  switch (node.kind) {
    case 'program':
      return [node.body];
    case 'block':
      return node.stmts;
    case 'const':
      return [node.init];
    case 'func':
      return [...node.params, node.body];
    case 'exprStmt':
      return [node.expr];
    case 'bin':
      return [node.left, node.right];
    case 'un':
      return [node.expr];
    default:
      return [];
  }
}

export function* walk(node: AnyNode): Generator<AnyNode> {
  yield node;
  for (const child of childrenOf(node)) yield* walk(child);
}

// --- id allocation -------------------------------------------------------

let counter = 0;

export function resetIds(): void {
  counter = 0;
}

export function nid(): NodeId {
  counter += 1;
  return `n${counter}`;
}

// --- builders ------------------------------------------------------------

export function program(body: Block): Program {
  return { id: nid(), kind: 'program', body };
}
export function block(stmts: Stmt[]): Block {
  return { id: nid(), kind: 'block', stmts };
}
export function constDecl(name: string, init: Expr): ConstDecl {
  return { id: nid(), kind: 'const', name, init };
}
export function funcDecl(name: string, params: Array<[string, TypeName]>, retType: TypeName, body: Block): FuncDecl {
  return { id: nid(), kind: 'func', name, params: params.map(([pname, ptype]) => param(pname, ptype)), retType, body };
}
export function param(name: string, paramType: TypeName): Param {
  return { id: nid(), kind: 'param', name, paramType };
}
export function exprStmt(expr: Expr): ExprStmt {
  return { id: nid(), kind: 'exprStmt', expr };
}
export function intLit(value: number): IntLit {
  return { id: nid(), kind: 'int', value };
}
export function boolLit(value: boolean): BoolLit {
  return { id: nid(), kind: 'bool', value };
}
export function ref(name: string): Ref {
  return { id: nid(), kind: 'ref', name };
}
export function bin(op: BinOp, left: Expr, right: Expr): Bin {
  return { id: nid(), kind: 'bin', op, left, right };
}
export function un(op: UnOp, expr: Expr): Un {
  return { id: nid(), kind: 'un', op, expr };
}
