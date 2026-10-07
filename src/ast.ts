// Core AST with binding identities.
// Every binder (lambda param / let binding / top-level function) owns a unique
// BindId; every variable occurrence carries `ref` pointing at its binder.
// Names are purely cosmetic: all analysis is done on BindId, so shadowing and
// alpha-renaming can never confuse the refactorer.

export type NodeId = number;
export type BindId = number;

export type BinOp = 'add' | 'sub' | 'mul' | 'eq' | 'lt' | 'and' | 'or';

export interface Binder {
  bind: BindId;
  name: string;
}

export type Expr =
  | { kind: 'num'; id: NodeId; value: number }
  | { kind: 'bool'; id: NodeId; value: boolean }
  | { kind: 'var'; id: NodeId; name: string; ref: BindId }
  | { kind: 'lam'; id: NodeId; params: Binder[]; body: Expr }
  | { kind: 'app'; id: NodeId; fn: Expr; args: Expr[] }
  | { kind: 'let'; id: NodeId; binder: Binder; value: Expr; body: Expr }
  | { kind: 'if'; id: NodeId; cond: Expr; then: Expr; els: Expr }
  | { kind: 'bin'; id: NodeId; op: BinOp; lhs: Expr; rhs: Expr }
  // `return` aborts the nearest enclosing lambda: a control-flow boundary.
  | { kind: 'return'; id: NodeId; value: Expr };

export interface FunDecl {
  name: string;
  bind: BindId;
  params: Binder[];
  body: Expr;
}

export interface Program {
  functions: FunDecl[];
  root: Expr;
}

export function childrenOf(e: Expr): Expr[] {
  switch (e.kind) {
    case 'num':
    case 'bool':
    case 'var':
      return [];
    case 'lam':
      return [e.body];
    case 'app':
      return [e.fn, ...e.args];
    case 'let':
      return [e.value, e.body];
    case 'if':
      return [e.cond, e.then, e.els];
    case 'bin':
      return [e.lhs, e.rhs];
    case 'return':
      return [e.value];
  }
}

export function forEachNode(e: Expr, fn: (e: Expr) => void): void {
  fn(e);
  for (const c of childrenOf(e)) forEachNode(c, fn);
}

export function findNode(root: Expr, id: NodeId): Expr | undefined {
  let hit: Expr | undefined;
  forEachNode(root, (e) => {
    if (e.id === id && hit === undefined) hit = e;
  });
  return hit;
}

export function maxNodeId(root: Expr): number {
  let m = -1;
  forEachNode(root, (e) => {
    if (e.id > m) m = e.id;
  });
  return m;
}
