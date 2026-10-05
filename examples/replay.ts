// 可重放编辑样例：构造程序 -> 全量求值 -> 逐步编辑 -> 增量重算，
// 每一步都与“从头按作用域遍历”的参考求值器对拍。
// 运行：node examples/replay.ts   （需要 Node >= 22.6，直接执行 TypeScript）

import assert from 'node:assert/strict';
import { Program } from '../src/ast.ts';
import { AttrEvaluator } from '../src/evaluator.ts';
import { registerLanguage, serializeConst, serializeErrors, snapshotIncremental } from '../src/lang.ts';
import type { ConstValue, Diagnostic } from '../src/lang.ts';
import { evaluateReference } from '../src/reference.ts';
import { applyEdit } from '../src/edits.ts';
import type { Edit } from '../src/edits.ts';

const prog = new Program();
const ev = new AttrEvaluator();
registerLanguage(ev, prog);

// 初始程序：
//   const x = 1;
//   const y = x + 2;
//   func f(p: int) { const z = p * y; exprStmt(z) }
//   exprStmt(y * 10)
const declX = prog.constDecl('x', prog.num(1));
const refXinY = prog.ref('x');
const declY = prog.constDecl('y', prog.bin('+', refXinY, prog.num(2)));
const bodyZ = prog.constDecl('z', prog.bin('*', prog.ref('p'), prog.ref('y')));
const useZ = prog.exprStmt(prog.ref('z'));
const funcF = prog.funcDecl('f', [['p', 'int']], prog.block(bodyZ, useZ));
const refYinTail = prog.ref('y');
const tailExpr = prog.bin('*', refYinTail, prog.num(10));
const tailUse = prog.exprStmt(tailExpr);
const root = prog.block(declX, declY, funcF, tailUse);
prog.setRoot(root);

function show(label: string): void {
  const errs = ev.get(root.id, 'errors') as Diagnostic[];
  console.log(`\n== ${label}`);
  console.log(`   errors:          ${serializeErrors(errs)}`);
  console.log(
    `   tail (y*10):     const=${serializeConst(ev.get(tailExpr.id, 'const') as ConstValue)} type=${String(ev.get(tailExpr.id, 'type'))}`,
  );
  console.log(
    `   dep of ref y:    ${JSON.stringify(ev.dependenciesOf(refYinTail.id, 'binding'))}`,
  );
  console.log(
    `   stats:           recomputed=${ev.stats.recomputed} verified=${ev.stats.verified} cacheHits=${ev.stats.cacheHits}`,
  );
  const inc = snapshotIncremental(prog, ev);
  const ref = evaluateReference(prog);
  assert.deepStrictEqual(inc, ref);
  console.log('   reference check: OK');
}

function step(label: string, edit: Edit): void {
  ev.resetStats();
  applyEdit(prog, ev, edit);
  show(label);
}

console.log(`node ${process.version} — 可重放编辑样例`);
ev.resetStats();
show('STEP 0 初始全量求值');

step("STEP 1 move: 把 'const y' 移到 tail 之后（声明跨过引用）", {
  op: 'move', stmt: declY.id, toBlock: root.id, index: 3,
});

step("STEP 2 move: 把 'const y' 移回 index 1（错误修复）", {
  op: 'move', stmt: declY.id, toBlock: root.id, index: 1,
});

step("STEP 3 remove: 删除外层 'const x'（y 的右值变未绑定）", {
  op: 'remove', stmt: declX.id,
});

step("STEP 4 insert: 在 index 0 插入 'const x = 10'（插入声明修复错误）", {
  op: 'insert', block: root.id, index: 0,
  stmt: { kind: 'const', name: 'x', expr: { kind: 'num', value: 10 } },
});

step("STEP 5 renameParam: 参数 p -> q（函数体内 p 变未绑定）", {
  op: 'renameParam', param: funcF.params[0].id, name: 'q',
});

const declX2 = root.stmts[0]; // STEP 4 插入的新声明节点
step("STEP 6 batch: [参数改回 p, 'const x' 移到 y 之后, 追加 exprStmt(x)]", {
  op: 'batch',
  edits: [
    { op: 'renameParam', param: funcF.params[0].id, name: 'p' },
    { op: 'move', stmt: declX2.id, toBlock: root.id, index: 2 },
    { op: 'insert', block: root.id, index: 4,
      stmt: { kind: 'expr', expr: { kind: 'ref', name: 'x' } } },
  ],
});

console.log('\n全部步骤均与参考求值器一致。');
