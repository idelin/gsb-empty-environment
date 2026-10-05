// CheckedProgram: owns the AST, the incremental evaluator, structural
// edits, and diagnostic queries.
//
// Edit rule of thumb: an edit must seed-invalidate exactly those
// attributes whose compute function directly reads the mutated
// structure (statement lists, parameter names, expression children).
// Everything else is reached automatically through the recorded
// dependency graph.

import {
  childrenOf,
  isExpr,
  isStmt,
  walk,
  type AnyNode,
  type Block,
  type Diagnostic,
  type Expr,
  type FuncDecl,
  type NodeId,
  type Param,
  type Program,
  type Stmt,
} from './ast.ts';
import { Evaluator, type AttrKey } from './evaluator.ts';
import { flattenEnv, type Env } from './env.ts';
import { makeAttrFactory, parseKey, type TypeRes, type Value, type View } from './semantics.ts';

// Attributes whose compute function reads mutable AST structure
// directly (sibling position, parent links, declared names, enclosing
// blocks). They are always recomputed when dirty; type/value are pure
// attribute-to-attribute computations and use dependency verification.
const STRUCTURAL_ATTRS = new Set(['env', 'envOut', 'scopeBase', 'cell', 'bind', 'errors']);

export interface Snapshot {
  envs: Record<NodeId, Array<[string, NodeId]>>;
  binds: Record<NodeId, NodeId | null>;
  types: Record<NodeId, string>;
  values: Record<NodeId, Value>;
  errors: Diagnostic[];
}

export type Edit =
  | { op: 'insert'; blockId: NodeId; index: number; stmt: Stmt }
  | { op: 'remove'; stmtId: NodeId }
  | { op: 'move'; stmtId: NodeId; targetBlockId: NodeId; index: number }
  | { op: 'renameParam'; paramId: NodeId; newName: string }
  | { op: 'replaceExpr'; exprId: NodeId; expr: Expr };

export class CheckedProgram {
  readonly root: Program;
  private readonly byId = new Map<NodeId, AnyNode>();
  private readonly parentMap = new Map<NodeId, NodeId | null>();
  private readonly indexMap = new Map<NodeId, number>();
  readonly ev: Evaluator;

  constructor(root: Program) {
    this.root = root;
    this.reindex();
    const view: View = {
      node: (id) => this.byId.get(id),
      parent: (id) => this.parentMap.get(id) ?? null,
      stmtIndex: (id) => this.indexMap.get(id) ?? -1,
    };
    const ev = new Evaluator(
      makeAttrFactory(view, (key) => ev.get(key)),
      (key) => STRUCTURAL_ATTRS.has(parseKey(key).attr),
    );
    this.ev = ev;
  }

  private reindex(): void {
    this.byId.clear();
    this.parentMap.clear();
    this.indexMap.clear();
    const visit = (node: AnyNode, parent: NodeId | null): void => {
      this.byId.set(node.id, node);
      this.parentMap.set(node.id, parent);
      if (node.kind === 'block') {
        node.stmts.forEach((s, i) => this.indexMap.set(s.id, i));
      }
      for (const c of childrenOf(node)) visit(c, node.id);
    };
    visit(this.root, null);
  }

  node(id: NodeId): AnyNode {
    const n = this.byId.get(id);
    if (n === undefined) throw new Error(`unknown node ${id}`);
    return n;
  }

  // --- queries -----------------------------------------------------------

  check(): Diagnostic[] {
    const diags: Diagnostic[] = [];
    for (const n of walk(this.root)) {
      diags.push(...(this.ev.get(`errors@${n.id}`) as Diagnostic[]));
    }
    return diags;
  }

  typeOf(id: NodeId): string {
    return (this.ev.get(`type@${id}`) as TypeRes).t;
  }

  valueOf(id: NodeId): Value {
    return this.ev.get(`value@${id}`) as Value;
  }

  bindOf(id: NodeId): NodeId | null {
    return this.ev.get(`bind@${id}`) as NodeId | null;
  }

  envOf(id: NodeId): Array<[string, NodeId]> {
    return flattenEnv(this.ev.get(`env@${id}`) as Env | null).map(
      (e) => [e.name, e.declId] as [string, NodeId],
    );
  }

  snapshot(): Snapshot {
    const snap: Snapshot = { envs: {}, binds: {}, types: {}, values: {}, errors: this.check() };
    for (const n of walk(this.root)) {
      const parentId = this.parentMap.get(n.id);
      const parent = parentId === undefined || parentId === null ? undefined : this.byId.get(parentId);
      // only nodes sitting in a block's statement list are statements
      // (a function/program body block is a scope, not a statement)
      if (isStmt(n) && parent !== undefined && parent.kind === 'block') {
        snap.envs[n.id] = this.envOf(n.id);
      }
      if (n.kind === 'ref') snap.binds[n.id] = this.bindOf(n.id);
      if (isExpr(n) || n.kind === 'const' || n.kind === 'func' || n.kind === 'param') {
        snap.types[n.id] = this.typeOf(n.id);
      }
      if (isExpr(n) || n.kind === 'const' || n.kind === 'param') {
        snap.values[n.id] = this.valueOf(n.id);
      }
    }
    return snap;
  }

  // --- edits ---------------------------------------------------------------

  apply(edit: Edit): void {
    this.applyAll([edit]);
  }

  applyAll(edits: Edit[]): void {
    const seeds = new Set<AttrKey>();
    const removed = new Set<NodeId>();
    const touchedBlocks = new Set<NodeId>();
    const structuralHolders = new Set<NodeId>();

    const collectSubtree = (id: NodeId): void => {
      const n = this.byId.get(id);
      if (n === undefined) return;
      removed.add(id);
      for (const c of childrenOf(n)) collectSubtree(c.id);
    };

    const blockOf = (id: NodeId): Block => {
      const n = this.byId.get(id);
      if (n === undefined || n.kind !== 'block') throw new Error(`${id} is not a block`);
      return n;
    };

    for (const edit of edits) {
      switch (edit.op) {
        case 'insert': {
          blockOf(edit.blockId).stmts.splice(edit.index, 0, edit.stmt);
          touchedBlocks.add(edit.blockId);
          break;
        }
        case 'remove': {
          const parentId = this.parentMap.get(edit.stmtId);
          if (parentId === undefined || parentId === null) throw new Error(`cannot remove ${edit.stmtId}`);
          const block = blockOf(parentId);
          const idx = block.stmts.findIndex((s) => s.id === edit.stmtId);
          block.stmts.splice(idx, 1);
          touchedBlocks.add(parentId);
          collectSubtree(edit.stmtId);
          break;
        }
        case 'move': {
          const srcId = this.parentMap.get(edit.stmtId);
          if (srcId === undefined || srcId === null) throw new Error(`cannot move ${edit.stmtId}`);
          const src = blockOf(srcId);
          const idx = src.stmts.findIndex((s) => s.id === edit.stmtId);
          const [stmt] = src.stmts.splice(idx, 1);
          blockOf(edit.targetBlockId).stmts.splice(edit.index, 0, stmt);
          touchedBlocks.add(srcId);
          touchedBlocks.add(edit.targetBlockId);
          break;
        }
        case 'renameParam': {
          const p = this.node(edit.paramId) as Param;
          p.name = edit.newName;
          const func = this.node(this.parentMap.get(edit.paramId) as NodeId) as FuncDecl;
          seeds.add(`scopeBase@${func.body.id}`);
          touchedBlocks.add(func.body.id);
          break;
        }
        case 'replaceExpr': {
          const holderId = this.parentMap.get(edit.exprId);
          if (holderId === undefined || holderId === null) throw new Error(`cannot replace ${edit.exprId}`);
          const holder = this.node(holderId);
          collectSubtree(edit.exprId);
          if (holder.kind === 'const') holder.init = edit.expr;
          else if (holder.kind === 'exprStmt') holder.expr = edit.expr;
          else if (holder.kind === 'bin') {
            if (holder.left.id === edit.exprId) holder.left = edit.expr;
            else holder.right = edit.expr;
          } else if (holder.kind === 'un') holder.expr = edit.expr;
          else throw new Error(`cannot replace expression under ${holder.kind}`);
          structuralHolders.add(holderId);
          break;
        }
      }
      // Keep the structural maps consistent for the next edit in a batch.
      this.reindex();
    }

    for (const b of touchedBlocks) {
      const block = this.byId.get(b);
      if (block === undefined || block.kind !== 'block') continue;
      // env@stmt reads the statement list (previous sibling) directly.
      for (const s of block.stmts) seeds.add(`env@${s.id}`);
      // scope cells read the statement list directly.
      for (const key of this.ev.keys()) {
        if (key.startsWith(`cell@${b}:`)) seeds.add(key);
      }
    }
    for (const h of structuralHolders) {
      seeds.add(`type@${h}`);
      seeds.add(`value@${h}`);
      seeds.add(`errors@${h}`);
    }
    this.ev.removeWhere((key) => removed.has(parseKey(key).nodeId));
    this.ev.resetStats();
    this.ev.invalidate(seeds);
  }
}
