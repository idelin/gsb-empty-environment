import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  GapAwareFilter,
  formatReport,
  type FilterSpec,
  type VersionedOutput,
} from '../src/gap-aware-filter.ts';

const SPEC: FilterSpec = {
  b: [0.5, 0.3, 0.2],
  a: [1, -0.4],
  sampleInterval: 1,
  maxInterpolationGap: 3,
};

/** 参考实现：直接 I 型，逐样本计算，用于校验滤波数学正确性。 */
function referenceIIR(b: number[], a: number[], xs: number[]): number[] {
  const a0 = a[0];
  const bn = b.map((c) => c / a0);
  const an = a.map((c) => c / a0);
  const ys: number[] = [];
  for (let n = 0; n < xs.length; n += 1) {
    let y = 0;
    for (let i = 0; i < bn.length && i <= n; i += 1) y += bn[i] * xs[n - i];
    for (let i = 1; i < an.length && i <= n; i += 1) y -= an[i] * ys[n - i];
    ys.push(y);
  }
  return ys;
}

/** 把样本按时间排序后依次写入新滤波器，得到“全量重算”的基准输出。 */
function fullRecompute(
  spec: FilterSpec,
  samples: { t: number; v: number }[],
): VersionedOutput[] {
  const f = new GapAwareFilter(spec);
  for (const s of [...samples].sort((x, y) => x.t - y.t)) f.addSample(s.t, s.v);
  return f.getOutputs();
}

test('无缺口时输出与参考 IIR 实现一致', () => {
  const f = new GapAwareFilter(SPEC);
  const xs = [1, 2, 3, 4, 5];
  xs.forEach((v, i) => f.addSample(i, v));
  const expected = referenceIIR(SPEC.b, SPEC.a, xs);
  const outs = f.getOutputs();
  assert.equal(outs.length, 5);
  outs.forEach((o, i) => {
    assert.ok(Math.abs(o.y - expected[i]) < 1e-12, `t=${o.t}`);
    assert.equal(o.interpolated, false);
  });
});

test('a[0] 非 1 时自动归一化', () => {
  const spec: FilterSpec = { ...SPEC, b: [1, 0.6, 0.4], a: [2, -0.8] };
  const f = new GapAwareFilter(spec);
  [1, 2, 3].forEach((v, i) => f.addSample(i, v));
  const expected = referenceIIR(spec.b, spec.a, [1, 2, 3]);
  f.getOutputs().forEach((o, i) => assert.ok(Math.abs(o.y - expected[i]) < 1e-12));
});

test('短缺口线性插值补点，且插值参与滤波', () => {
  const f = new GapAwareFilter(SPEC);
  f.addSample(0, 0);
  f.addSample(3, 6); // 缺口 3 <= 阈值 3：在 t=1,2 插值
  const outs = f.getOutputs();
  assert.deepEqual(outs.map((o) => o.t), [0, 1, 2, 3]);
  assert.deepEqual(outs.map((o) => o.interpolated), [false, true, true, false]);
  // 插值序列 [0, 2, 4, 6] 经过滤波器
  const expected = referenceIIR(SPEC.b, SPEC.a, [0, 2, 4, 6]);
  outs.forEach((o, i) => assert.ok(Math.abs(o.y - expected[i]) < 1e-12));
});

test('长缺口断开滤波状态：新段从零状态重启，缺口内无插值', () => {
  const f = new GapAwareFilter(SPEC);
  f.addSample(0, 1);
  f.addSample(1, 2);
  f.addSample(10, 5); // 缺口 9 > 阈值 3：断状态
  f.addSample(11, 6);
  const outs = f.getOutputs();
  assert.deepEqual(outs.map((o) => o.t), [0, 1, 10, 11]);
  // 第二段等价于从零状态对 [5, 6] 滤波
  const seg2 = referenceIIR(SPEC.b, SPEC.a, [5, 6]);
  assert.ok(Math.abs(outs[2].y - seg2[0]) < 1e-12);
  assert.ok(Math.abs(outs[3].y - seg2[1]) < 1e-12);
});

test('迟到样本缩短短缺口：局部重算并报告变化，插入点之前的历史不动', () => {
  const f = new GapAwareFilter(SPEC);
  f.addSample(0, 0); // v1
  f.addSample(3, 6); // v2：t=1,2 为插值点
  const before = f.getOutputs();
  const v0 = before.find((o) => o.t === 0)!.version;

  const report = f.addSample(1, 100); // 迟到样本，落在已有插值点 t=1 上
  assert.equal(report.bridgedGap, false);
  assert.deepEqual(report.affectedRange, { start: 0, end: 3 });

  const after = f.getOutputs();
  // t=0 在插入点之前，结果与版本均不变
  const p0 = after.find((o) => o.t === 0)!;
  assert.equal(p0.version, v0);
  assert.equal(p0.y, before.find((o) => o.t === 0)!.y);
  // t=1 由插值点变为真实样本（updated），下游 t=2,3 也被更新
  assert.ok(report.changes.some((c) => c.kind === 'updated' && c.t === 1));
  assert.ok(report.changes.some((c) => c.kind === 'updated' && c.t === 3));
  // 变化点的版本号为本次写入版本
  for (const c of report.changes) {
    if (c.after) assert.equal(c.after.version, report.version);
  }
  // 与全量重算结果完全一致（局部重算正确性）
  const full = fullRecompute(SPEC, [
    { t: 0, v: 0 },
    { t: 1, v: 100 },
    { t: 3, v: 6 },
  ]);
  assert.deepEqual(
    after.map((o) => [o.t, o.y, o.interpolated]),
    full.map((o) => [o.t, o.y, o.interpolated]),
  );
});

test('迟到样本把长缺口补成短缺口：两段合并，bridgedGap=true', () => {
  const f = new GapAwareFilter(SPEC);
  f.addSample(0, 1); // v1
  f.addSample(1, 2); // v2
  f.addSample(10, 5); // v3：长缺口，第二段开始
  f.addSample(11, 6); // 继续第二段
  const before = f.getOutputs();
  assert.deepEqual(before.map((o) => o.t), [0, 1, 10, 11]);

  // 迟到样本 t=4：1->4 缺口 3 <= 3（并入前段），4->10 缺口 6 > 3，尚未桥接
  const report = f.addSample(4, 3);
  assert.equal(report.bridgedGap, false);
  const report2 = f.addSample(7, 4); // 4->7 缺口 3，7->10 缺口 3：长缺口被补齐
  assert.equal(report2.bridgedGap, true);

  const after = f.getOutputs();
  // 合并后全段连续，插值点填满所有短缺口
  assert.deepEqual(
    after.map((o) => o.t),
    [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11],
  );
  // 与全量重算一致
  const samples = [
    { t: 0, v: 1 },
    { t: 1, v: 2 },
    { t: 4, v: 3 },
    { t: 7, v: 4 },
    { t: 10, v: 5 },
    { t: 11, v: 6 },
  ];
  const full = fullRecompute(SPEC, samples);
  assert.deepEqual(
    after.map((o) => [o.t, o.y, o.interpolated]),
    full.map((o) => [o.t, o.y, o.interpolated]),
  );
  // 桥接报告包含新增的插值点与更新的下游点
  assert.ok(report2.changes.some((c) => c.kind === 'added' && c.t === 8));
  assert.ok(report2.changes.some((c) => c.kind === 'updated' && c.t === 10));
  // 版本号单调：被改写的点版本号变大，未触及的点保留旧版本
  const p10 = after.find((o) => o.t === 10)!;
  assert.ok(p10.version > before.find((o) => o.t === 10)!.version);
});

test('重算不跨越长缺口：前段历史完全不受影响', () => {
  const f = new GapAwareFilter(SPEC);
  f.addSample(0, 1);
  f.addSample(1, 2);
  f.addSample(20, 9); // 长缺口
  const snapshot = f.getOutputs().map((o) => ({ ...o }));
  const report = f.addSample(21, 8); // 只在第二段内重算
  assert.deepEqual(report.affectedRange, { start: 20, end: 21 });
  const after = f.getOutputs();
  for (const t of [0, 1]) {
    const p = after.find((o) => o.t === t)!;
    const q = snapshot.find((o) => o.t === t)!;
    assert.equal(p.y, q.y);
    assert.equal(p.version, q.version);
  }
  assert.ok(report.changes.every((c) => c.t >= 20));
});

test('同时间戳重复写入视为修正，触发局部重算', () => {
  const f = new GapAwareFilter(SPEC);
  f.addSample(0, 1);
  f.addSample(1, 2);
  const report = f.addSample(1, 5); // 修正 t=1 的取值
  assert.equal(report.replacedExisting, true);
  const outs = f.getOutputs();
  assert.equal(outs.length, 2);
  const expected = referenceIIR(SPEC.b, SPEC.a, [1, 5]);
  assert.ok(Math.abs(outs[1].y - expected[1]) < 1e-12);
  assert.ok(report.changes.some((c) => c.kind === 'updated' && c.t === 1));
});

test('乱序批量到达与顺序到达结果一致', () => {
  const samples = [
    { t: 0, v: 0.3 },
    { t: 1, v: -1.2 },
    { t: 2, v: 0.8 },
    { t: 5, v: 2.1 },
    { t: 6, v: 0.4 },
    { t: 30, v: 1.1 },
    { t: 33, v: -0.7 },
  ];
  const inc = new GapAwareFilter(SPEC);
  // 乱序到达：先两头后中间
  for (const s of [samples[5], samples[0], samples[6], samples[2], samples[4], samples[1], samples[3]]) {
    inc.addSample(s.t, s.v);
  }
  const full = fullRecompute(SPEC, samples);
  assert.deepEqual(
    inc.getOutputs().map((o) => [o.t, o.y, o.interpolated]),
    full.map((o) => [o.t, o.y, o.interpolated]),
  );
});

test('FIR（a=[1]）与标量增益边界情形', () => {
  const fir = new GapAwareFilter({ b: [0.5, 0.5], a: [1], sampleInterval: 1, maxInterpolationGap: 2 });
  fir.addSample(0, 2);
  fir.addSample(1, 4);
  const outs = fir.getOutputs();
  assert.ok(Math.abs(outs[0].y - 1) < 1e-12);
  assert.ok(Math.abs(outs[1].y - 3) < 1e-12);

  const gain = new GapAwareFilter({ b: [2], a: [1], sampleInterval: 1, maxInterpolationGap: 2 });
  gain.addSample(0, 3);
  assert.ok(Math.abs(gain.getOutputs()[0].y - 6) < 1e-12);
});

test('非法输入抛错', () => {
  assert.throws(() => new GapAwareFilter({ b: [], a: [1], sampleInterval: 1, maxInterpolationGap: 2 }));
  assert.throws(() => new GapAwareFilter({ b: [1], a: [0], sampleInterval: 1, maxInterpolationGap: 2 }));
  assert.throws(() => new GapAwareFilter({ b: [1], a: [1], sampleInterval: 0, maxInterpolationGap: 2 }));
  const f = new GapAwareFilter(SPEC);
  assert.throws(() => f.addSample(NaN, 1));
});

test('formatReport 输出可读的变化说明', () => {
  const f = new GapAwareFilter(SPEC);
  f.addSample(0, 0);
  f.addSample(10, 5);
  const report = f.addSample(5, 2.5); // 5<=3? 否：0->5 缺口 5 > 3，未桥接
  void report;
  const r2 = f.addSample(2, 1); // 0->2 短缺口；2->5 缺口 3：桥接到 t=5 段
  const text = formatReport(r2);
  assert.match(text, /版本/);
  assert.match(text, /局部重算范围/);
  assert.match(text, /历史结果变化/);
});
