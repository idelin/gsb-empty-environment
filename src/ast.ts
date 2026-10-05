// AST：稳定节点 ID、父指针索引与结构编辑原语。
// 节点 ID 在创建时分配，之后永不改变 —— 移动语句不会改变其 ID，
// 因此属性缓存键 (nodeId, attrName) 在编辑后仍然有效。

export type NodeId = string;
export type TypeName = 'int' | 'bool' | 'func' | 'error';
export type BinOp = '+' | '-' | '*' | '<' | '==' | 'and' | 'or';

export interface NumExpr { kind: 'num'; id: NodeId; value: number }
export interface BoolExpr { kind: 'bool'; id: NodeId; value: boolean }
export interface RefExpr { kind: 'ref'; id: NodeId; name: string }
export interface BinExpr { kind: 'bin'; id: NodeId; op: BinOp; left: Expr; right: Expr }
export interface NotExpr { kind: 'not'; id: NodeId; expr: Expr }
export type Expr = NumExpr | BoolExpr | RefExpr | BinExpr | NotExpr;

export interface Param { kind: 'param'; id: NodeId; name: string; ptype: 'int' | 'bool' }
export interface BlockStmt { kind: 'block'; id: NodeId; stmts: Stmt[] }
export interface ConstDecl { kind: 'const'; id: NodeId; name: string; expr: Expr }
export interface FuncDecl { kind: 'func'; id: NodeId; name: string; params: Param[]; body: BlockStmt }
export interface ExprStmt { kind: 'expr'; id: NodeId; expr: Expr }
export type Stmt = BlockStmt | ConstDecl | FuncDecl | ExprStmt;
export type AnyNode = Stmt | Expr | Param;

export function isExpr(n: AnyNode): n is Expr {
  return n.kind === 'num' || n.kind === 'bool' || n.kind === 'ref' || n.kind === 'bin' || n.kind === 'not';
}
export function isStmt(n: AnyNode): n is Stmt {
  return n.kind === 'block' || n.kind === 'const' || n.kind === 'func' || n.kind === 'expr';
}

export class Program {
  readonly nodes = new Map<NodeId, AnyNode>();
  readonly parents = new Map<NodeId, NodeId>();
  private counter = 0;
  root: BlockStmt | null = null;

  private fresh(): NodeId {
    this.counter += 1;
    return `n${this.counter}`;
  }

  private register<T extends AnyNode>(node: T): T {
    this.nodes.set(node.id, node);
    return node;
  }

  private adopt(child: AnyNode, parentId: NodeId): void {
    this.parents.set(child.id, parentId);
  }

  num(value: number): NumExpr {
    return this.register({ kind: 'num', id: this.fresh(), value });
  }
  bool(value: boolean): BoolExpr {
    return this.register({ kind: 'bool', id: this.fresh(), value });
  }
  ref(name: string): RefExpr {
    return this.register({ kind: 'ref', id: this.fresh(), name });
  }
  bin(op: BinOp, left: Expr, right: Expr): BinExpr {
    const node = this.register({ kind: 'bin', id: this.fresh(), op, left, right });
    this.adopt(left, node.id);
    this.adopt(right, node.id);
    return node;
  }
  not(expr: Expr): NotExpr {
    const node = this.register({ kind: 'not', id: this.fresh(), expr });
    this.adopt(expr, node.id);
    return node;
  }
  constDecl(name: string, expr: Expr): ConstDecl {
    const node = this.register({ kind: 'const', id: this.fresh(), name, expr });
    this.adopt(expr, node.id);
    return node;
  }
  funcDecl(name: string, params: Array<readonly [string, 'int' | 'bool']>, body: BlockStmt): FuncDecl {
    const ps: Param[] = params.map(([pname, ptype]) =>
      this.register<Param>({ kind: 'param', id: this.fresh(), name: pname, ptype }));
    const node = this.register<FuncDecl>({ kind: 'func', id: this.fresh(), name, params: ps, body });
    for (const p of ps) this.adopt(p, node.id);
    this.adopt(body, node.id);
    return node;
  }
  exprStmt(expr: Expr): ExprStmt {
    const node = this.register({ kind: 'expr', id: this.fresh(), expr });
    this.adopt(expr, node.id);
    return node;
  }
  block(...stmts: Stmt[]): BlockStmt {
    const node = this.register<BlockStmt>({ kind: 'block', id: this.fresh(), stmts });
    for (const s of stmts) this.adopt(s, node.id);
    return node;
  }
  setRoot(block: BlockStmt): void {
    this.root = block;
  }

  node(id: NodeId): AnyNode {
    const n = this.nodes.get(id);
    if (!n) throw new Error(`unknown node ${id}`);
    return n;
  }

  childrenOf(id: NodeId): NodeId[] {
    const n = this.node(id);
    switch (n.kind) {
      case 'block': return n.stmts.map((s) => s.id);
      case 'const': return [n.expr.id];
      case 'func': return [...n.params.map((p) => p.id), n.body.id];
      case 'expr': return [n.expr.id];
      case 'bin': return [n.left.id, n.right.id];
      case 'not': return [n.expr.id];
      default: return [];
    }
  }

  /** 表达式所属语句：沿父指针向上找到最近的语句节点。 */
  enclosingStmtId(id: NodeId): NodeId {
    let cur = id;
    for (;;) {
      const p = this.parents.get(cur);
      if (p === undefined) throw new Error(`node ${id} has no enclosing statement`);
      if (isStmt(this.node(p))) return p;
      cur = p;
    }
  }

  subtreeIds(id: NodeId): NodeId[] {
    const out: NodeId[] = [];
    const walk = (nid: NodeId): void => {
      out.push(nid);
      for (const c of this.childrenOf(nid)) walk(c);
    };
    walk(id);
    return out;
  }

  /** 把语句挂到 blockId 的 index 处（结构变更；失效由 edits.ts 负责）。 */
  attach(blockId: NodeId, index: number, stmt: Stmt): void {
    const block = this.node(blockId);
    if (block.kind !== 'block') throw new Error(`${blockId} is not a block`);
    block.stmts.splice(index, 0, stmt);
    this.parents.set(stmt.id, blockId);
  }

  /** 把语句从其父块摘下（子树保留在 nodes 中，可重新 attach，ID 不变）。 */
  detach(stmtId: NodeId): void {
    const pid = this.parents.get(stmtId);
    if (pid === undefined) throw new Error(`stmt ${stmtId} is not attached`);
    const block = this.node(pid);
    if (block.kind !== 'block') throw new Error(`parent ${pid} is not a block`);
    const idx = block.stmts.findIndex((s) => s.id === stmtId);
    block.stmts.splice(idx, 1);
    this.parents.delete(stmtId);
  }

  renameParam(paramId: NodeId, name: string): void {
    const p = this.node(paramId);
    if (p.kind !== 'param') throw new Error(`${paramId} is not a param`);
    p.name = name;
  }
}
