// 一致性：分批 vs 逐条、乱序到达、checkpoint 恢复、变更流重复消费幂等、随机对拍。

import { test } from 'node:test';
import { strict as assert } from 'node:assert';
import { PatternEngine } from '../src/engine.ts';
import { referenceAlerts } from '../src/reference.ts';
import { AlertView } from '../src/view.ts';
import type { Change, InputRecord } from '../src/types.ts';
import { assertSameAlerts, generateLog, shuffle } from './helpers.ts';

const W = 15;
const L = 10;

function gen(seed: number, count = 400): InputRecord[] {
  return generateLog({
    seed,
    count,
    idPool: 30,
    maxTimeStep: 4,
    latenessSpread: 25,
    watermarkEvery: 17,
    watermarkLag: 12,
  });
}

test('分批与逐条消费产生相同的最终有效告警集合', () => {
  const log = gen(1);
  const oneByOne = new PatternEngine(W, L);
  const changes1 = oneByOne.applyAll(log);

  const batched = new PatternEngine(W, L);
  const changes2: Change[] = [];
  for (let i = 0; i < log.length; i += 32) batched.applyAll(log.slice(i, i + 32)).forEach((c) => changes2.push(c));
  // 注意：applyAll 本身就是逐条应用；这里再验证大批次切片等价
  const allAtOnce = new PatternEngine(W, L);
  allAtOnce.applyAll(log);

  assertSameAlerts(batched.effectiveAlerts(), oneByOne.effectiveAlerts(), '分批 vs 逐条');
  assertSameAlerts(allAtOnce.effectiveAlerts(), oneByOne.effectiveAlerts(), '整批 vs 逐条');
  assert.deepStrictEqual(changes2, changes1, '变更流应逐条一致');
});

test('乱序到达：同一乱序日志下流式与参考一致', () => {
  const log = gen(2);
  for (const seed of [11, 22, 33]) {
    const shuffled = shuffle(log, seed);
    const e = new PatternEngine(W, L);
    e.applyAll(shuffled);
    const ref = referenceAlerts(shuffled, W, L);
    assertSameAlerts(e.effectiveAlerts(), ref.alerts, `乱序 seed=${seed}`);
    assert.deepStrictEqual(e.stats, ref.stats, `乱序统计 seed=${seed}`);
  }
});

test('同 tick 冲突簇：流式与参考一致', () => {
  const log = generateLog({
    seed: 7,
    count: 300,
    idPool: 12,
    maxTimeStep: 1, // 大量同时间事件
    latenessSpread: 8,
    watermarkEvery: 23,
    watermarkLag: 6,
  });
  const e = new PatternEngine(4, 3);
  e.applyAll(log);
  const ref = referenceAlerts(log, 4, 3);
  assertSameAlerts(e.effectiveAlerts(), ref.alerts, '同 tick 冲突簇');
  assert.deepStrictEqual(e.stats, ref.stats);
  assert.ok(e.stats.conflicts > 0, '应覆盖同 rev 冲突');
  assert.ok(e.stats.idempotentDuplicates > 0, '应覆盖幂等重复');
});

test('checkpoint 恢复：快照后继续与不间断运行一致', () => {
  const log = gen(3);
  const cut = Math.floor(log.length / 2);

  const uninterrupted = new PatternEngine(W, L);
  uninterrupted.applyAll(log);

  const first = new PatternEngine(W, L);
  const changes1 = first.applyAll(log.slice(0, cut));
  const snapshot = first.snapshot();

  const restored = PatternEngine.restore(snapshot);
  const changes2 = restored.applyAll(log.slice(cut));

  assertSameAlerts(restored.effectiveAlerts(), uninterrupted.effectiveAlerts(), '恢复后最终集合');
  assert.deepStrictEqual(restored.stats, uninterrupted.stats, '恢复后统计');
  assert.equal(restored.currentWatermark, uninterrupted.currentWatermark);

  // 恢复后的变更流拼接与不间断运行的变更流一致
  const allChanges = new PatternEngine(W, L).applyAll(log);
  assert.deepStrictEqual([...changes1, ...changes2], allChanges, '变更流在恢复点前后拼接一致');

  // 双重恢复：从快照恢复两次互不影响
  const again = PatternEngine.restore(snapshot);
  again.applyAll(log.slice(cut));
  assertSameAlerts(again.effectiveAlerts(), uninterrupted.effectiveAlerts(), '重复恢复');
});

test('变更流重复消费不重复计数', () => {
  const log = gen(4);
  const e = new PatternEngine(W, L);
  const changes = e.applyAll(log);

  const view = new AlertView();
  view.apply(changes);
  const sizeAfterFirst = view.size;
  view.apply(changes); // 整体重放
  view.apply(changes.slice(0, 10)); // 局部重放
  assert.equal(view.size, sizeAfterFirst, '重放后计数不变');
  assertSameAlerts(view.entries(), e.effectiveAlerts(), '物化视图 == 引擎有效集合');

  const ref = referenceAlerts(log, W, L);
  assertSameAlerts(view.entries(), ref.alerts, '物化视图 == 参考集合');
});

test('随机对拍：多种子最终有效集合与统计一致', () => {
  for (const seed of [101, 202, 303, 404, 505, 606]) {
    const log = gen(seed, 500);
    const e = new PatternEngine(W, L);
    const changes = e.applyAll(log);
    const ref = referenceAlerts(log, W, L);

    assertSameAlerts(e.effectiveAlerts(), ref.alerts, `seed=${seed} 有效集合`);
    assert.deepStrictEqual(e.stats, ref.stats, `seed=${seed} 统计`);

    const view = new AlertView();
    view.apply(changes);
    assertSameAlerts(view.entries(), ref.alerts, `seed=${seed} 变更流物化`);

    // 中途 checkpoint 恢复后再继续，也应收敛到同一集合
    const cut = Math.floor(log.length / 2);
    const half = new PatternEngine(W, L);
    half.applyAll(log.slice(0, cut));
    const restored = PatternEngine.restore(half.snapshot());
    restored.applyAll(log.slice(cut));
    assertSameAlerts(restored.effectiveAlerts(), ref.alerts, `seed=${seed} 恢复后集合`);
  }
});

test('热状态随水位线有界，注册表只随不同 ID 增长', () => {
  const log = gen(8, 2000);
  const e = new PatternEngine(W, L);
  e.applyAll(log);
  // 热索引只保留 time >= wm - L - W 的事件
  const horizon = e.currentWatermark - L - W;
  void horizon;
  assert.ok(e.hotEventCount <= 30 * 3, '热状态有界（远小于总事件量）');
  assert.ok(e.registrySize <= 30, '注册表以 ID 池为界');
  assert.ok(e.archivedAlertCount > 0, '应产生归档');
});
