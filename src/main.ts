/**
 * 演示：构造表达式 → 折叠 → 打印优化树与来源对应 → 在若干环境下对比运行。
 */

import { Builder, showExpr } from './ast';
import { runExpr } from './eval';
import { exprSize, fold, optSize, runOpt, showOpt, OptExpr } from './fold';

function showSrc(e: OptExpr, indent = ''): void {
  const self =
    e.kind === 'const' ? `const ${JSON.stringify(e.value)}` :
    e.kind === 'error' ? `error ${e.error.kind}@#${e.error.opId}` :
    e.kind === 'var' ? `var ${e.name}#${e.id}` :
    e.kind === 'bin' ? `bin ${e.op}#${e.id}` :
    e.kind === 'and' ? `and#${e.id}` :
    e.kind === 'or' ? `or#${e.id}` : `cond#${e.id}`;
  console.log(`${indent}${self}  <= src:[${e.src.join(', ')}]`);
  switch (e.kind) {
    case 'bin':
    case 'and':
    case 'or':
      showSrc(e.left, indent + '  ');
      showSrc(e.right, indent + '  ');
      break;
    case 'cond':
      showSrc(e.cond, indent + '  ');
      showSrc(e.then, indent + '  ');
      showSrc(e.else, indent + '  ');
      break;
  }
}

const b = new Builder();
// ((x && true) || (false && (1 / 0))) ? (10 / (2 - 2)) : (x * 2)
const expr = b.cond(
  b.or(
    b.and(b.variable('x'), b.bool(true)),
    b.and(b.bool(false), b.bin('/', b.num(1), b.num(0))),
  ),
  b.bin('/', b.num(10), b.bin('-', b.num(2), b.num(2))),
  b.bin('*', b.variable('x'), b.num(2)),
);

const opt = fold(expr);

console.log('原始表达式（节点带稳定 id）:');
console.log('  ' + showExpr(expr));
console.log(`  节点数: ${exprSize(expr)}`);
console.log('');
console.log('优化后表达式:');
console.log('  ' + showOpt(opt));
console.log(`  节点数: ${optSize(opt)}`);
console.log('');
console.log('优化树与来源对应（src = 来源节点 id 列表）:');
showSrc(opt);
console.log('');

for (const env of [{ x: true }, { x: false }, { x: 3 }]) {
  const before = runExpr(expr, env);
  const after = runOpt(opt, env);
  const fmt = (r: typeof before) =>
    (r.outcome.ok ? `value=${JSON.stringify(r.outcome.value)}`
                  : `error=${r.outcome.error.kind}@#${r.outcome.error.opId}`) +
    ` trace=[${r.trace.map((t) => `${t.name}#${t.id}`).join(' ')}]`;
  console.log(`env=${JSON.stringify(env)}`);
  console.log(`  优化前: ${fmt(before)}`);
  console.log(`  优化后: ${fmt(after)}`);
}
