import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import {
  signal,
  computed,
  effect,
  configureGuard,
  CircularSignalUpdate,
  DEFAULT_MAX_ITERATIONS,
} from '../src/index.ts';

afterEach(() => {
  configureGuard({ maxIterations: DEFAULT_MAX_ITERATIONS });
});

test('正常场景：Effect 读取 A 写入 B，有限次迭代后收敛，不触发守护', () => {
  const a = signal(0, { name: 'A' });
  const b = signal(0, { name: 'B' });
  effect(() => {
    const value = a.get();
    if (b.get() !== value * 2) {
      b.set(value * 2);
    }
  }, { name: 'sync-b' });
  a.set(21);
  assert.equal(b.get(), 42);
});

test('正常场景：条件自增在阈值内收敛，不触发守护', () => {
  const a = signal(0, { name: 'A' });
  effect(() => {
    const value = a.get();
    if (value < 10) {
      a.set(value + 1);
    }
  }, { name: 'counter' });
  assert.equal(a.get(), 10);
});

test('验收场景：Effect 读取 Signal A 同时写入 A，在第 50 次迭代被拦截并指出触发源', () => {
  const a = signal(0, { name: 'A' });
  let caught: unknown = null;
  try {
    effect(() => {
      a.set(a.get() + 1);
    }, { name: 'self-increment' });
  } catch (error) {
    caught = error;
  }

  assert.ok(caught instanceof CircularSignalUpdate, '应抛出 CircularSignalUpdate');
  const error = caught as CircularSignalUpdate;
  assert.equal(error.kind, 'iteration-overflow');
  assert.equal(error.iterations, 50, '应在第 50 次迭代拦截');
  assert.equal(error.maxIterations, 50);

  // 前 49 次迭代已执行，第 50 次被阻断
  assert.equal(a.get(), 49);

  // 触发源：Effect 直接写入自身依赖 Signal A
  assert.ok(error.source, '报告应包含触发源');
  assert.equal(error.source?.relation, '直接');
  assert.match(error.source?.signalLabel ?? '', /Signal#\d+ "A"/);
  assert.match(error.source?.observerLabel ?? '', /Effect#\d+ "self-increment"/);

  // 完整报告文本
  assert.match(error.message, /第 50 次迭代/);
  assert.match(error.message, /触发源/);
  assert.match(error.message, /Signal#\d+ "A"/);
  assert.match(error.message, /因果链/);
  assert.ok(error.chain.length > 0, '因果链不应为空');
});

test('间接自依赖：Effect 读取 Computed（依赖 A）却写入 A，被识别为间接自依赖', () => {
  const a = signal(1, { name: 'A' });
  const doubled = computed(() => a.get() * 2, { name: 'Doubled' });
  let caught: unknown = null;
  try {
    effect(() => {
      const value = doubled.get();
      a.set(value);
    }, { name: 'indirect-writer' });
  } catch (error) {
    caught = error;
  }

  assert.ok(caught instanceof CircularSignalUpdate);
  const error = caught as CircularSignalUpdate;
  assert.equal(error.kind, 'iteration-overflow');
  assert.equal(error.iterations, 50);
  assert.equal(error.source?.relation, '间接');
  assert.match(error.message, /间接自依赖/);
});

test('调用栈环路：两个 Computed 互相读取，立即抛出 call-stack-cycle', () => {
  const c1: { current?: { get(): number } } = {};
  const c2: { current?: { get(): number } } = {};
  c1.current = computed(() => (c2.current as { get(): number }).get() + 1, { name: 'C1' });
  c2.current = computed(() => (c1.current as { get(): number }).get() + 1, { name: 'C2' });

  assert.throws(
    () => (c1.current as { get(): number }).get(),
    (error: unknown) => {
      assert.ok(error instanceof CircularSignalUpdate);
      assert.equal((error as CircularSignalUpdate).kind, 'call-stack-cycle');
      assert.match((error as CircularSignalUpdate).message, /调用栈环路/);
      return true;
    },
  );
});

test('跨节点环路：两个 Effect 互相触发，迭代突破阈值被拦截', () => {
  const a = signal(0, { name: 'A' });
  const b = signal(0, { name: 'B' });
  effect(() => {
    b.set(a.get() + 1);
  }, { name: 'EA' });

  let caught: unknown = null;
  try {
    effect(() => {
      a.set(b.get() + 1);
    }, { name: 'EB' });
  } catch (error) {
    caught = error;
  }

  assert.ok(caught instanceof CircularSignalUpdate);
  const error = caught as CircularSignalUpdate;
  assert.equal(error.kind, 'iteration-overflow');
  assert.equal(error.iterations, 50);
  assert.match(error.message, /跨节点环路/);
});

test('迭代阈值可配置：maxIterations = 5 时在第 5 次迭代拦截', () => {
  configureGuard({ maxIterations: 5 });
  const a = signal(0, { name: 'A' });
  let caught: unknown = null;
  try {
    effect(() => {
      a.set(a.get() + 1);
    }, { name: 'self-increment' });
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof CircularSignalUpdate);
  assert.equal((caught as CircularSignalUpdate).iterations, 5);
  assert.equal(a.get(), 4);
});

test('守护器阻断后系统仍可正常使用（状态已复位）', () => {
  const a = signal(0, { name: 'A' });
  assert.throws(() => {
    effect(() => {
      a.set(a.get() + 1);
    }, { name: 'loop' });
  }, CircularSignalUpdate);

  const x = signal(1, { name: 'X' });
  const y = signal(0, { name: 'Y' });
  effect(() => {
    y.set(x.get() * 10);
  }, { name: 'healthy' });
  x.set(7);
  assert.equal(y.get(), 70);
});

test('dispose 后 Effect 不再被调度', () => {
  const a = signal(0, { name: 'A' });
  let runs = 0;
  const handle = effect(() => {
    a.get();
    runs += 1;
  }, { name: 'watcher' });
  a.set(1);
  assert.equal(runs, 2);
  handle.dispose();
  a.set(2);
  assert.equal(runs, 2);
});
