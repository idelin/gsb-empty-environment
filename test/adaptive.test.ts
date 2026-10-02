import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AdaptiveThreshold,
  LoadAwareLimiter,
  ShedLoadError,
  type PressureMetrics,
} from '../src/adaptive.ts';

const calm: PressureMetrics = { cpuLoad: 0.1, memoryUsage: 0.1, errorRate: 0, queueDepth: 0 };
const hot: PressureMetrics = { cpuLoad: 0.95, memoryUsage: 0.5, errorRate: 0.2, queueDepth: 10 };

function makeClock(start = 0) {
  let t = start;
  return {
    now: () => t,
    advance(ms: number) {
      t += ms;
    },
  };
}

test('阈值: 构造参数校验', () => {
  assert.throws(() => new AdaptiveThreshold({ min: 0, max: 10 }), RangeError);
  assert.throws(() => new AdaptiveThreshold({ min: 10, max: 5 }), RangeError);
  assert.throws(
    () => new AdaptiveThreshold({ min: 1, max: 10, lowWatermark: 0.8, highWatermark: 0.2 }),
    RangeError,
  );
  assert.throws(() => new AdaptiveThreshold({ min: 1, max: 10, decreaseFactor: 1.5 }), RangeError);
  assert.throws(() => new AdaptiveThreshold({ min: 1, max: 10, increaseStep: 0 }), RangeError);
  // initial 越界时被钳制
  assert.equal(new AdaptiveThreshold({ min: 2, max: 8, initial: 100 }).threshold, 8);
  assert.equal(new AdaptiveThreshold({ min: 2, max: 8, initial: 1 }).threshold, 2);
});

test('阈值: 压力评分取各维度最大值并钳制到 [0,1]', () => {
  const ctl = new AdaptiveThreshold({ min: 1, max: 16, queueDepthScale: 10 });
  assert.equal(ctl.pressureOf({ cpuLoad: 0.2, memoryUsage: 0.9, errorRate: 0.1, queueDepth: 0 }), 0.9);
  assert.equal(ctl.pressureOf({ cpuLoad: 0, memoryUsage: 0, errorRate: 0, queueDepth: 15 }), 1);
  assert.equal(ctl.pressureOf({ cpuLoad: -5, memoryUsage: 0, errorRate: 0, queueDepth: 0 }), 0);
  // 非法输入按最大压力处理（安全兜底）
  assert.equal(
    ctl.pressureOf({ cpuLoad: NaN, memoryUsage: 0, errorRate: 0, queueDepth: 0 }),
    1,
  );
});

test('阈值: 高压乘性收缩并钳制到下限', () => {
  const clock = makeClock();
  const ctl = new AdaptiveThreshold({
    min: 2, max: 64, initial: 64, decreaseFactor: 0.5, cooldownMs: 10, now: clock.now,
  });
  const s1 = ctl.adjust(hot);
  assert.equal(s1.direction, 'down');
  assert.equal(s1.threshold, 32);
  clock.advance(10);
  assert.equal(ctl.adjust(hot).threshold, 16);
  // 连续高压最终钳制到 min，不会跌破
  for (let i = 0; i < 10; i++) {
    clock.advance(10);
    ctl.adjust(hot);
  }
  assert.equal(ctl.threshold, 2);
});

test('阈值: 低压加性扩张并钳制到上限', () => {
  const clock = makeClock();
  const ctl = new AdaptiveThreshold({
    min: 1, max: 8, initial: 1, increaseStep: 3, cooldownMs: 10, now: clock.now,
  });
  assert.equal(ctl.adjust(calm).threshold, 4);
  clock.advance(10);
  assert.equal(ctl.adjust(calm).threshold, 7);
  clock.advance(10);
  assert.equal(ctl.adjust(calm).threshold, 8); // clamp 到 max
  clock.advance(10);
  assert.equal(ctl.adjust(calm).threshold, 8);
});

test('阈值: 滞回区间内保持不动（防抖）', () => {
  const ctl = new AdaptiveThreshold({
    min: 1, max: 16, initial: 8, lowWatermark: 0.3, highWatermark: 0.7, cooldownMs: 0,
  });
  const mid: PressureMetrics = { cpuLoad: 0.5, memoryUsage: 0.5, errorRate: 0, queueDepth: 0 };
  const snap = ctl.adjust(mid);
  assert.equal(snap.direction, 'hold');
  assert.equal(snap.threshold, 8);
});

test('阈值: 冷却期内拒绝调整，规避振荡雪崩', () => {
  const clock = makeClock();
  const ctl = new AdaptiveThreshold({
    min: 1, max: 64, initial: 64, cooldownMs: 100, now: clock.now,
  });
  assert.equal(ctl.adjust(hot).threshold, 32); // 第一次生效
  // 冷却期内：即使压力仍高也不再收缩
  const snap = ctl.adjust(hot);
  assert.equal(snap.direction, 'hold');
  assert.equal(snap.threshold, 32);
  clock.advance(99);
  assert.equal(ctl.adjust(hot).threshold, 32); // 仍在冷却期
  clock.advance(1);
  assert.equal(ctl.adjust(hot).threshold, 16); // 冷却结束
});

test('阈值: 历史记录有界（无内存泄漏）', () => {
  const ctl = new AdaptiveThreshold({ min: 1, max: 4, maxHistory: 10, cooldownMs: 0 });
  for (let i = 0; i < 1000; i++) ctl.adjust(calm);
  assert.ok(ctl.snapshots.length <= 10);
});

test('限流器: 并发上限被严格遵守', async () => {
  const ctl = new AdaptiveThreshold({ min: 1, max: 4, initial: 3, cooldownMs: 0 });
  const limiter = new LoadAwareLimiter({ threshold: ctl, sampleMetrics: () => ({ ...calm, cpuLoad: 0.5 }) });
  let active = 0;
  let maxActive = 0;
  const tasks = Array.from({ length: 20 }, () =>
    limiter.run(async () => {
      active++;
      maxActive = Math.max(maxActive, active);
      await new Promise((r) => setTimeout(r, 5));
      active--;
    }),
  );
  await Promise.all(tasks);
  assert.ok(maxActive <= 3, `峰值并发 ${maxActive} 应 <= 3`);
  assert.equal(limiter.stats.completed, 20);
  limiter.assertDrained(); // 无泄漏
});

test('限流器: 动态扩限后等待者被唤醒', async () => {
  const clock = makeClock();
  const ctl = new AdaptiveThreshold({
    min: 1, max: 8, initial: 1, increaseStep: 4, cooldownMs: 0, now: clock.now,
  });
  const limiter = new LoadAwareLimiter({ threshold: ctl, sampleMetrics: () => calm });
  let active = 0;
  let maxActive = 0;
  const tasks = Array.from({ length: 6 }, () =>
    limiter.run(async () => {
      active++;
      maxActive = Math.max(maxActive, active);
      await new Promise((r) => setTimeout(r, 20));
      active--;
    }),
  );
  await Promise.all(tasks);
  assert.ok(maxActive > 1, '低压下阈值应扩张，允许更高并发');
  limiter.assertDrained();
});

test('限流器: 队列满时快速失败卸载（ShedLoadError）', async () => {
  const ctl = new AdaptiveThreshold({ min: 1, max: 1, initial: 1, cooldownMs: 0 });
  const limiter = new LoadAwareLimiter({
    threshold: ctl,
    maxQueue: 2,
    sampleMetrics: () => ({ ...hot }),
  });
  let releaseFirst!: () => void;
  const blocker = new Promise<void>((r) => (releaseFirst = r));
  const first = limiter.run(() => blocker); // 占住唯一槽位
  const queued = [limiter.run(() => {}), limiter.run(() => {})]; // 填满队列
  await assert.rejects(limiter.run(() => {}), ShedLoadError); // 第 4 个被卸载
  assert.equal(limiter.stats.shed, 1);
  releaseFirst();
  await Promise.all([first, ...queued]);
  limiter.assertDrained();
});

test('限流器: AbortSignal 取消排队请求且无泄漏', async () => {
  const ctl = new AdaptiveThreshold({ min: 1, max: 1, initial: 1, cooldownMs: 0 });
  const limiter = new LoadAwareLimiter({ threshold: ctl, sampleMetrics: () => hot });
  let releaseFirst!: () => void;
  const blocker = new Promise<void>((r) => (releaseFirst = r));
  const first = limiter.run(() => blocker);

  const ac = new AbortController();
  const pending = limiter.run(() => {}, ac.signal);
  ac.abort(new Error('用户取消'));
  await assert.rejects(pending, /用户取消/);
  assert.equal(limiter.pending, 0); // 已从队列摘除

  // 已中止的 signal 立即拒绝
  await assert.rejects(limiter.run(() => {}, ac.signal), /用户取消/);

  releaseFirst();
  await first;
  limiter.assertDrained();
});

test('限流器: 任务抛异常时槽位仍被释放（无泄漏）', async () => {
  const ctl = new AdaptiveThreshold({ min: 1, max: 2, initial: 2, cooldownMs: 0 });
  const limiter = new LoadAwareLimiter({ threshold: ctl, sampleMetrics: () => calm });
  await assert.rejects(
    limiter.run(async () => {
      throw new Error('boom');
    }),
    /boom/,
  );
  assert.equal(limiter.active, 0);
  // 后续任务可正常执行
  assert.equal(await limiter.run(() => 42), 42);
  limiter.assertDrained();
});

test('限流器: 并发风暴下 completed 计数精确且状态归零', async () => {
  const ctl = new AdaptiveThreshold({ min: 1, max: 16, initial: 8, cooldownMs: 0 });
  const limiter = new LoadAwareLimiter({ threshold: ctl, sampleMetrics: () => calm });
  const N = 500;
  const results = await Promise.all(
    Array.from({ length: N }, (_, i) => limiter.run(async () => i * 2)),
  );
  assert.equal(results.length, N);
  assert.equal(limiter.stats.completed, N);
  assert.equal(limiter.active, 0);
  assert.equal(limiter.pending, 0);
  limiter.assertDrained();
});

test('限流器: release 多于 acquire 抛错', () => {
  const ctl = new AdaptiveThreshold({ min: 1, max: 2 });
  const limiter = new LoadAwareLimiter({ threshold: ctl, sampleMetrics: () => calm });
  assert.throws(() => limiter.release(), /release 次数多于 acquire/);
});
