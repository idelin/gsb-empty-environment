// AST 编辑操作 + 可序列化、可重放的编辑日志。
// 每次编辑负责：1) 变更 AST 结构；2) 把受影响的“根因属性”标记为强制重算。
// 其余失效由求值器沿已记录的依赖边传播，读取时按需验证。

import { AttrEvaluator, keyOf } from './evaluator.ts';
import type { Key, NodeId } from './evaluator.ts';
import type { BinOp, Expr, Program, Stmt } from './ast.ts';

// ---- 可序列化的节点描述（编辑日志中的“新子树”以数据形式存在）----
export type ExprSpec =
  | { kind: 'num'; value: number }
  | { kind: 'bool'; value: boolean }
  | { kind: 'ref'; name: string }
  | { kind: 'bin'; op: BinOp; left: ExprSpec; right: ExprSpec }
  | { kind: 'not'; expr: ExprSpec };

export type StmtSpec =
  | { kind: 'const'; name: string; expr: ExprSpec }
  | { kind: 'func'; name: string; params: Array<readonly [string, 'int' | 'bool']>; body: StmtSpec[] }
  | { kind: 'expr'; expr: ExprSpec }
  | { kind: 'block'; stmts: StmtSpec[] };

export type Edit =
  | { op: 'insert'; block: NodeId; index: number; stmt: StmtSpec }
  | { op: 'remove'; stmt: NodeId }
  | { op: 'move'; stmt: NodeId; toBlock: NodeId; index: number }
  | { op: 'renameParam'; param: NodeId; name: string }
  | { op: 'batch'; edits: Edit[] };

export function buildExpr(prog: Program, spec: ExprSpec): Expr {
  switch (spec.kind) {
    case 'num': return prog.num(spec.value);
    case 'bool': return prog.bool(spec.value);
    case 'ref': return prog.ref(spec.name);
    case 'bin': return prog.bin(spec.op, buildExpr(prog, spec.left), buildExpr(prog, spec.right));
    case 'not': return prog.not(buildExpr(prog, spec.expr));
  }
}

export function buildStmt(prog: Program, spec: StmtSpec): Stmt {
  switch (spec.kind) {
    case 'const': return prog.constDecl(spec.name, buildExpr(prog, spec.expr));
    case 'func':
      return prog.funcDecl(spec.name, spec.params,
        prog.block(...spec.body.map((s) => buildStmt(prog, s))));
    case 'expr': return prog.exprStmt(buildExpr(prog, spec.expr));
    case 'block': return prog.block(...spec.stmts.map((s) => buildStmt(prog, s)));
  }
}

/**
 * 块内语句列表发生变化后的根因失效：
 * - 每条语句的 envIn/envOut（它们在序列中的位置/前驱可能变了）；
 * - 块的 errors / blockEnv（它们枚举子语句，成员可能增删）。
 * 其余属性（表达式的 type/const/binding 等）沿依赖边传播、按需验证。
 */
function invalidateBlock(ev: AttrEvaluator, prog: Program, blockId: NodeId): void {
  const block = prog.node(blockId);
  if (block.kind !== 'block') throw new Error(`${blockId} is not a block`);
  const keys: Key[] = [keyOf(blockId, 'errors'), keyOf(blockId, 'blockEnv')];
  for (const s of block.stmts) {
    keys.push(keyOf(s.id, 'envIn'), keyOf(s.id, 'envOut'));
  }
  ev.invalidate(keys, { force: true });
}

export function applyEdit(prog: Program, ev: AttrEvaluator, edit: Edit): void {
  switch (edit.op) {
    case 'insert': {
      const stmt = buildStmt(prog, edit.stmt);
      prog.attach(edit.block, edit.index, stmt);
      invalidateBlock(ev, prog, edit.block);
      break;
    }
    case 'remove': {
      const parentId = prog.parents.get(edit.stmt);
      if (parentId === undefined) throw new Error(`stmt ${edit.stmt} is not attached`);
      const ids = prog.subtreeIds(edit.stmt);
      prog.detach(edit.stmt);
      invalidateBlock(ev, prog, parentId);
      ev.deleteAttributes(new Set(ids));
      break;
    }
    case 'move': {
      // index 以“摘下之后”的目标块语句列表为准；节点 ID 不变。
      const srcBlock = prog.parents.get(edit.stmt);
      if (srcBlock === undefined) throw new Error(`stmt ${edit.stmt} is not attached`);
      const stmt = prog.node(edit.stmt) as Stmt;
      prog.detach(edit.stmt);
      prog.attach(edit.toBlock, edit.index, stmt);
      invalidateBlock(ev, prog, srcBlock);
      if (edit.toBlock !== srcBlock) invalidateBlock(ev, prog, edit.toBlock);
      break;
    }
    case 'renameParam': {
      const funcId = prog.parents.get(edit.param);
      if (funcId === undefined) throw new Error(`param ${edit.param} is not attached`);
      const func = prog.node(funcId);
      if (func.kind !== 'func') throw new Error(`${funcId} is not a func`);
      prog.renameParam(edit.param, edit.name);
      // 函数体块的 envIn 由参数构成；func 的 errors 重新检查参数重名。
      ev.invalidate(
        [keyOf(func.body.id, 'envIn'), keyOf(funcId, 'errors')],
        { force: true },
      );
      break;
    }
    case 'batch': {
      // 批量编辑：连续应用全部结构变更，只求值一次（读取时惰性重算）。
      for (const e of edit.edits) applyEdit(prog, ev, e);
      break;
    }
  }
}

export function applyEdits(prog: Program, ev: AttrEvaluator, edits: Edit[]): void {
  for (const e of edits) applyEdit(prog, ev, e);
}
