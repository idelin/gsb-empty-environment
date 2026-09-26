import { Rational } from '../src/rational.ts';
import { buildFixtureWorld, layoutText, makeMetrics, FIXTURE_TEXT } from '../src/fixtures.ts';
import { vec, vecToString } from '../src/types.ts';
import type { ScrollTransaction, Vec } from '../src/types.ts';

const world = buildFixtureWorld();

function showVec(v: Vec): string {
  return `(${v.x.toString().padStart(6)}, ${v.y.toString().padStart(6)})`;
}

function printTx(tx: ScrollTransaction): void {
  console.log(`  tx#${tx.id} source=${JSON.stringify(tx.source)} initial=${vecToString(tx.initialDelta)} target=${tx.targetId}`);
  console.log('    层        收到            消费            传递            舍弃            备注');
  for (const layer of tx.layers) {
    const notes: string[] = [];
    if (layer.preTransform) notes.push(`轴变换 ${vecToString(layer.preTransform)}→${vecToString(layer.received)}`);
    if (layer.rerouted) notes.push('捕获重定向');
    console.log(
      `    ${layer.nodeId.padEnd(8)}${showVec(layer.received)}  ${showVec(layer.consumed)}  ${showVec(layer.passed)}  ${showVec(layer.discarded)}  ${notes.join(' ')}`,
    );
  }
  console.log(`    逃逸 ${vecToString(tx.escaped)}`);
  const ok = tx.layers.every((l) =>
    (['x', 'y'] as const).every((a) =>
      l.consumed[a].add(l.passed[a]).add(l.discarded[a]).eq(l.received[a]),
    ),
  );
  console.log(`    守恒校验(每层 收到=消费+传递+舍弃): ${ok ? '✓' : '✗'}`);
}

function pos(id: string): string {
  return `${id}.scroll=${vecToString(world.mustGet(id).scroll)}`;
}

console.log('=== 嵌套滚动事务引擎 · 运行样例 ===\n');

const layout = layoutText(FIXTURE_TEXT, makeMetrics(), Rational.of('340'));
console.log(`[0] 夹具排版（字形度量，无浏览器布局引擎）`);
console.log(`    outer 文本 ${layout.lines} 行 × 行高 ${layout.height.div(Rational.of(BigInt(layout.lines)))} = 内容高 ${layout.height}`);
console.log(`    inner.maxScroll.y=${world.mustGet('inner').maxScroll('y')}  outer.maxScroll.y=${world.mustGet('outer').maxScroll('y')}\n`);

console.log('[1] 内层到底后剩余量传递给外层 (inner 先滚到 340/360，再滚 50)');
world.scrollTo('inner', vec('0', '340'));
printTx(world.dispatchWheel('inner', vec('0', '50')));
console.log(`    ${pos('inner')}  ${pos('outer')}\n`);

console.log('[2] 边界锁定：locked 触顶后剩余量舍弃，外层收不到');
world.scrollTo('locked', vec('0', '240'));
printTx(world.dispatchWheel('locked', vec('0', '30')));
console.log(`    ${pos('locked')}  ${pos('outer')}\n`);

console.log('[3] 轴方向：纵向节点收到混合增量，横向量原样穿透');
printTx(world.dispatchWheel('inner', vec('30', '20')));
console.log('');

console.log('[4] 轴变换：横向条带把纵向滚轮换算成横向（每事务一次）');
printTx(world.dispatchWheel('strip', vec('0', '60')));
console.log(`    ${pos('strip')}\n`);

console.log('[5] 程序滚动：来源令牌阻止反馈（回调内同令牌重入被挡）');
let feedbackAttempts = 0;
world.mustGet('inner').onScroll((e) => {
  if (e.source.kind === 'programmatic') {
    feedbackAttempts++;
    const r = world.scrollTo('inner', vec('0', '999'), e.source.token);
    if (feedbackAttempts === 1) {
      console.log(`    回调收到程序滚动(token=${e.source.token})，同令牌重入 → blocked=${r.blocked}`);
    }
  }
});
const prog = world.scrollTo('inner', vec('0', '100')); // inner 当时在 360，程序滚动回 100
console.log(`    scrollTo(inner, y=100) token=${prog.token} 实际位移=${vecToString(prog.applied)} 反馈尝试=${feedbackAttempts} 次，全部被阻止\n`);

console.log('[6] 惯性：衰减 1/2 的增量序列，每步独立事务');
const userTx = world.dispatchWheel('inner', vec('0', '40'));
printTx(userTx);
for (const tx of world.startInertia('inner', vec('0', '32'), { decay: Rational.of('1/2'), maxSteps: 4, originTx: userTx.id })) {
  printTx(tx);
}
console.log(`    ${pos('inner')}\n`);

console.log('[7] 捕获容器变化：滚动回调请求捕获 locked，剩余量重定向（locked 边界锁定，超出部分舍弃）');
world.mustGet('inner').onScroll((e) => {
  if (e.source.kind === 'user') world.requestCapture('locked');
});
world.releaseCapture();
world.scrollTo('inner', vec('0', '0'));
world.scrollTo('locked', vec('0', '0'));
printTx(world.dispatchWheel('inner', vec('0', '700')));
console.log(`    capture=${world.captureNodeId}  ${pos('inner')}  ${pos('locked')}  ${pos('outer')}`);
world.releaseCapture();
console.log('');

console.log('[8] 重放：全新世界中派发后重放，逐层记录一致');
const world2 = buildFixtureWorld();
world2.scrollTo('inner', vec('0', '340'));
const original = world2.dispatchWheel('inner', vec('0', '50'));
const { tx: replayed, identical } = world2.replay(original.id);
console.log(`    tx#${original.id} → replay → tx#${replayed.id}  identical=${identical}`);
console.log(`    inner=${vecToString(world2.mustGet('inner').scroll)}  outer=${vecToString(world2.mustGet('outer').scroll)}\n`);

console.log('[9] 精确残差：反向小增量累计可移动且回到原点');
const tiny = world.mustGet('both');
for (let i = 0; i < 3; i++) world.dispatchWheel('both', vec('0', '1/3'));
console.log(`    三次 +1/3 → both.scroll.y=${tiny.scroll.y} (精确等于 1: ${tiny.scroll.y.eq(Rational.of('1'))})`);
for (let i = 0; i < 3; i++) world.dispatchWheel('both', vec('0', '-1/3'));
console.log(`    三次 -1/3 → both.scroll.y=${tiny.scroll.y} (精确等于 0: ${tiny.scroll.y.isZero()})`);
console.log(`    对照裸浮点: 0.1+0.2===0.3 → ${0.1 + 0.2 === 0.3}；有理数 1/10+1/5===3/10 → ${Rational.of(0.1).add(Rational.of(0.2)).eq(Rational.of(0.3))}`);
