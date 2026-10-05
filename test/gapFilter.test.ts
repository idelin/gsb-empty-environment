import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GapTolerantFilter } from '../src/index.ts';

/** 零初始状态参考 IIR，用于独立校验 */
function referenceOutputs(b: number[], a: number[], inputs: number[]): number[] {
  const a0 = a[0];
  const bb = b.map((v) => v / a0);
  const aa = a.map((v) => v / a0);
  const ys: number[] = [];
  for (let n = 0; n < inputs.length; n++) {
    let y = 0;
    for (let i = 0; i < bb.length; i++) y += bb[i] * (inputs[n - i] ?? 0);
    for (let j = 1; j < aa.length; j++) y -= aa[j] * (ys[n - j] ?? 0);
    ys.push(y);
  }
  return ys;
}

function assertClose(actual: number, expected: number, msg?: string): void {
  assert.ok(Math.abs(actual - expected) < 1e-9, `${msg ?? ''} 期望 ${expected}，实际 ${actual}`);
}

test('连续样本：输出与参考 IIR 一致，版本单调递增', () => {
  const f = new GapTolerantFilter({ b: [1], a: [1, -0.5], gapThreshold: 2 });
  const xs = [1, 2, 3, 4, 5];
  xs.forEach((x, i) => {
    const cs = f.addSample(i, x);
    assert.equal(cs.version, i + 1);
    assert.equal(cs.cause, i === 0 ? 'initial' : 'append');
  });
  const ref = referenceOutputs([1], [1, -0.5], xs);
  f.getOutputs().forEach((o, i) => {
    assertClose(o.value, ref[i], `索引 ${o.index}`);
    assert.equal(o.version, i + 1);
    assert.equal(o.origin, 'real');
  });
  assert.deepEqual(f.segments(), [{ start: 0, end: 4 }]);
});

test('短缺口：线性插值补齐且滤波状态连续', () => {
  // 两点滑动平均，阈值 2，缺口 2 → 插值
  const f = new GapTolerantFilter({ b: [0.5, 0.5], a: [1], gapThreshold: 2 });
  f.addSample(0, 1);
  const cs = f.addSample(3, 4); // 缺 1、2 两个样本 → 插值出 2、3
  assert.equal(cs.cause, 'append');
  assert.match(cs.summary, /线性插值/);

  const outs = f.getOutputs();
  assert.deepEqual(outs.map((o) => o.index), [0, 1, 2, 3]);
  assert.deepEqual(outs.map((o) => o.origin), ['real', 'interpolated', 'interpolated', 'real']);
  // 插值输入序列 [1, 2, 3, 4] 的滑动平均
  const ref = referenceOutputs([0.5, 0.5], [1], [1, 2, 3, 4]);
  outs.forEach((o, i) => assertClose(o.value, ref[i], `索引 ${o.index}`));
  assert.deepEqual(f.segments(), [{ start: 0, end: 3 }]);
});

test('长缺口：断开滤波状态，后段从零状态重启', () => {
  const f = new GapTolerantFilter({ b: [0.5, 0.5], a: [1], gapThreshold: 1 });
  f.addSample(0, 1);
  const cs = f.addSample(5, 1); // 缺 4 个样本 > 阈值 1 → 断开
  assert.equal(cs.cause, 'append');
  assert.match(cs.summary, /断开/);

  // 缺口 void 内无输出
  assert.equal(f.getOutput(2), undefined);
  // 后段首点 = 零状态输出：0.5*1 + 0.5*0
  assertClose(f.getOutput(5)!.value, 0.5);
  assert.deepEqual(f.segments(), [{ start: 0, end: 0 }, { start: 5, end: 5 }]);
});

test('迟到样本把长缺口拆短：两段桥接，后段历史输出全部重算', () => {
  // y[n] = x[n] + 0.5·y[n−1]，阈值 2
  const f = new GapTolerantFilter({ b: [1], a: [1, -0.5], gapThreshold: 2 });
  [1, 2, 3, 4].forEach((x, i) => f.addSample(i, x)); // 版本 1–4，索引 0–3
  [5, 6, 7, 8].forEach((x, k) => f.addSample(8 + k, x)); // 版本 5–8，索引 8–11（缺口 4 > 2，断开）
  assert.equal(f.segments().length, 2);
  const before8 = f.getOutput(8)!.value; // 零状态：y8 = 5
  assertClose(before8, 5);

  // 迟到样本落在缺口中间：3→5 缺 1、5→8 缺 2，均 ≤ 2 → 桥接
  const cs = f.addSample(5, 10);
  assert.equal(cs.version, 9);
  assert.equal(cs.cause, 'gap-bridged');
  assert.match(cs.summary, /桥接/);
  assert.match(cs.summary, /3→8/);

  // 新增：插值点 4、6、7 + 真实点 5；更新：后段 8–11
  assert.deepEqual(cs.changes.filter((c) => c.kind === 'added').map((c) => c.index), [4, 5, 6, 7]);
  assert.deepEqual(cs.changes.filter((c) => c.kind === 'updated').map((c) => c.index), [8, 9, 10, 11]);
  const upd8 = cs.changes.find((c) => c.index === 8)!;
  assert.equal(upd8.oldValue, before8);
  assert.equal(upd8.oldVersion, 5);
  assert.equal(upd8.newVersion, 9);

  // 全段与参考值一致：输入 [1,2,3,4, 7,10, 25/3,20/3, 5,6,7,8]
  const inputs = [1, 2, 3, 4, 7, 10, 25 / 3, 20 / 3, 5, 6, 7, 8];
  const ref = referenceOutputs([1], [1, -0.5], inputs);
  f.getOutputs().forEach((o, i) => assertClose(o.value, ref[i], `索引 ${o.index}`));

  // 分段合并；版本历史记录了“因补点而改变”
  assert.deepEqual(f.segments(), [{ start: 0, end: 11 }]);
  const hist8 = f.historyOf(8);
  assert.equal(hist8.length, 2);
  assert.equal(hist8[0].version, 5);
  assert.equal(hist8[1].version, 9);
  assertClose(f.getOutput(8)!.value, ref[8]);
  assert.equal(f.getOutput(8)!.version, 9);
  // 索引 0–3 未受影响，保留旧版本
  assert.equal(f.getOutput(3)!.version, 4);
});

test('迟到样本落在短缺口内：仅插值结果改变（FIR 的局部性）', () => {
  // 恒等滤波 y[n] = x[n]，阈值 3
  const f = new GapTolerantFilter({ b: [1], a: [1], gapThreshold: 3 });
  f.addSample(0, 0); // v1
  f.addSample(4, 8); // v2：插值 1,2,3 = 2,4,6
  assertClose(f.getOutput(1)!.value, 2);
  assertClose(f.getOutput(3)!.value, 6);

  const cs = f.addSample(2, 100); // v3：迟到样本，插值曲线 0→2→4 重算
  assert.equal(cs.cause, 'late-sample');
  // 索引 2 原本是插值输出，现在被真实样本取代 → 记为 updated（origin 变化）
  assert.deepEqual(cs.changes.filter((c) => c.kind === 'added').map((c) => c.index), []);
  assert.deepEqual(cs.changes.filter((c) => c.kind === 'updated').map((c) => c.index), [1, 2, 3]);
  const at2 = cs.changes.find((c) => c.index === 2)!;
  assert.equal(at2.origin, 'real');
  assertClose(at2.oldValue!, 4); // 旧的插值结果
  assertClose(f.getOutput(1)!.value, 50); // (0+100)/2
  assertClose(f.getOutput(3)!.value, 54); // (100+8)/2
  // 恒等滤波下索引 4 的输入未变 → 保留旧版本，体现局部性
  assert.equal(f.getOutput(4)!.version, 2);
  assert.equal(f.getOutput(1)!.version, 3);
});

test('迟到样本只缩短长缺口的一侧：分段维持断开', () => {
  const f = new GapTolerantFilter({ b: [1], a: [1, -0.5], gapThreshold: 1 });
  [1, 2].forEach((x, i) => f.addSample(i, x)); // 索引 0,1
  [3, 4].forEach((x, k) => f.addSample(6 + k, x)); // 索引 6,7（缺口 4 > 1，断开）
  const cs = f.addSample(3, 10); // 1→3 缺 1 ≤ 1 相连；3→6 缺 2 > 1 仍断开
  assert.equal(cs.cause, 'late-sample');
  assert.match(cs.summary, /维持断开/);
  assert.deepEqual(f.segments(), [{ start: 0, end: 3 }, { start: 6, end: 7 }]);
  // 后段未受影响
  assert.equal(cs.changes.filter((c) => c.kind === 'updated').length, 0);
  assert.equal(f.getOutput(6)!.version, 3);
});

test('样本修正：从修正点局部重算到段尾', () => {
  const f = new GapTolerantFilter({ b: [1], a: [1, -0.5], gapThreshold: 2 });
  [1, 1, 1, 1, 1].forEach((x, i) => f.addSample(i, x)); // v1–5
  const cs = f.addSample(1, 3); // v6：修正
  assert.equal(cs.cause, 'correction');
  assert.match(cs.summary, /修正/);
  assert.deepEqual(cs.changes.map((c) => c.index), [1, 2, 3, 4]);
  const ref = referenceOutputs([1], [1, -0.5], [1, 3, 1, 1, 1]);
  f.getOutputs().forEach((o, i) => assertClose(o.value, ref[i], `索引 ${o.index}`));
  assert.equal(f.getOutput(0)!.version, 1); // 修正点之前不受影响
  assert.equal(f.getOutput(4)!.version, 6);
});

test('版本化查询：changeSetsSince / getChangeSet / historyOf', () => {
  const f = new GapTolerantFilter({ b: [1], a: [1, -0.5], gapThreshold: 2 });
  f.addSample(0, 1); // v1
  f.addSample(5, 2); // v2：长缺口断开
  f.addSample(2, 1.5); // v3：迟到样本桥接（0→2 缺1、2→5 缺2）
  assert.equal(f.version, 3);
  assert.equal(f.changeSetsSince(1).length, 2);
  assert.equal(f.getChangeSet(3)!.cause, 'gap-bridged');
  assert.equal(f.historyOf(5).length, 2); // v2 零状态值 + v3 桥接后的值
  // 每个变更集都带中文说明
  for (const cs of f.changeSetsSince(0)) assert.ok(cs.summary.length > 0);
});

test('参数校验', () => {
  assert.throws(() => new GapTolerantFilter({ b: [], a: [1], gapThreshold: 1 }), /b 不能为空/);
  assert.throws(() => new GapTolerantFilter({ b: [1], a: [0, 1], gapThreshold: 1 }), /a₀ 不能为 0/);
  assert.throws(() => new GapTolerantFilter({ b: [1], a: [1], gapThreshold: -1 }), /非负整数/);
  const f = new GapTolerantFilter({ b: [1], a: [1], gapThreshold: 1 });
  assert.throws(() => f.addSample(1.5, 0), /整数/);
  assert.throws(() => f.addSample(0, NaN), /有限数/);
});

test('a₀ 归一化：a = [2, -1] 等价于 a = [1, -0.5]', () => {
  const f1 = new GapTolerantFilter({ b: [2], a: [2, -1], gapThreshold: 0 });
  const f2 = new GapTolerantFilter({ b: [1], a: [1, -0.5], gapThreshold: 0 });
  [1, 2, 3].forEach((x, i) => {
    f1.addSample(i, x);
    f2.addSample(i, x);
  });
  f1.getOutputs().forEach((o, i) => assertClose(o.value, f2.getOutput(i)!.value));
});
