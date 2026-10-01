/**
 * 运行样例：滑动窗口精确统计。
 * 运行：node examples/demo.ts   （Node >= 23.6，或 22.6+ 加 --experimental-strip-types）
 */
import { WindowStats } from '../src/stats.ts';

const line = (label: string, value: unknown) =>
  console.log(`${label.padEnd(58)} ${value}`);

// ── 场景 1：大偏移 + 小方差（浮点实现会在这里发生灾难消减） ──────────────
console.log('=== 1. 大偏移小方差：10^30 附近相差 1、2 的三个样本 ===');
const base = 10n ** 30n;
const w1 = new WindowStats();
for (const delta of [0n, 1n, 2n]) w1.add((base + delta).toString());
line('mean（精确）', w1.mean().toString());
line('sample variance（精确）', w1.sampleVariance()!.toString());
line('population variance（精确）', w1.populationVariance()!.toString());
// 对照：同样的数经 Number 中转后，连 +1 的差异都消失了
const asNumbers = [0n, 1n, 2n].map((d) => Number(base + d));
line('对照：Number(base+1) === Number(base)', asNumbers[0] === asNumbers[1]);

// ── 场景 2：滑动窗口增删 + 重复值 + 撤回恢复 ─────────────────────────────
console.log('\n=== 2. 大小为 3 的滑动窗口（含重复值），撤回后恢复原统计 ===');
const w2 = new WindowStats();
const stream = ['1.5', '2.5', '2.5', '4', '1e2', '-3.75'];
const windowSize = 3;
const inWindow: string[] = [];
for (const v of stream) {
  w2.add(v);
  inWindow.push(v);
  if (inWindow.length > windowSize) w2.remove(inWindow.shift()!);
  const snap = w2.snapshot(4, 'half-even');
  console.log(
    `  窗口 [${inWindow.join(', ')}]`.padEnd(46),
    `mean=${snap.mean}  sVar=${snap.sampleVariance}  pVar=${snap.populationVariance}`,
  );
}
// 撤回全部元素，验证恢复为空窗口
for (const v of [...inWindow]) w2.remove(v);
line('全部撤回后 count / sum', `${w2.count} / ${w2.sum().toString()}`);
line('全部撤回后 sample variance（未定义）', w2.sampleVariance());

// ── 场景 3：显式舍入规则 ─────────────────────────────────────────────────
console.log('\n=== 3. 同一精确值在不同舍入规则下的输出 ===');
const w3 = new WindowStats();
w3.add('1').add('2').add('2'); // 样本方差 = 1/3？实际为 1/3…见下
const exact = w3.sampleVariance()!;
line('样本方差精确值', exact.toString());
for (const mode of ['half-even', 'half-up', 'down', 'up', 'floor', 'ceil'] as const) {
  line(`toDecimalString(6, '${mode}')`, exact.toDecimalString(6, mode));
}

// ── 场景 4：边界 ─────────────────────────────────────────────────────────
console.log('\n=== 4. 边界行为 ===');
const w4 = new WindowStats();
w4.add('42');
line('n=1 样本方差', w4.sampleVariance());
line('n=1 总体方差', w4.populationVariance()!.toString());
try {
  w4.remove('42').remove('42');
} catch (e) {
  line('重复删除同一值', (e as Error).message);
}
