import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  KeyedExecutor,
  CancelledError,
  ExecutorClosedError,
} from '../src/keyed-executor.ts';

/** 让出事件循环，足以冲刷所有排队的微任务（settle 链）。 */
const flush = () => new Promise<void>((resolve) => setImmediate(resolve));

function gate() {
  let resolve!: () => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test('相同 key 的作业严格串行执行', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 4 });
  const events: string[] = [];
  const g1 = gate();
  const g2 = gate();
  const g3 = gate();
  const h1 = ex.submit('k', async () => { events.push('start1'); await g1.promise; events.push('end1'); });
  const h2 = ex.submit('k', async () => { events.push('start2'); await g2.promise; events.push('end2'); });
  const h3 = ex.submit('k', async () => { events.push('start3'); await g3.promise; events.push('end3'); });

  await flush();
  assert.deepEqual(events, ['start1']);
  assert.equal(h1.state, 'running');
  assert.equal(h2.state, 'queued');
  assert.equal(h3.state, 'queued');

  g1.resolve();
  await flush();
  assert.deepEqual(events, ['start1', 'end1', 'start2']);

  g2.resolve();
  await flush();
  g3.resolve();
  await flush();

  await Promise.all([h1.promise, h2.promise, h3.promise]);
  assert.deepEqual(events, ['start1', 'end1', 'start2', 'end2', 'start3', 'end3']);
  assert.equal(h3.state, 'completed');
  assert.equal(ex.keyCount, 0, '空闲 key 的状态应被释放');
});

test('不同 key 共享有限并发额度', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 2 });
  const events: string[] = [];
  const gA = gate();
  const gB = gate();
  const gC = gate();
  const hA = ex.submit('A', async () => { events.push('startA'); await gA.promise; events.push('endA'); });
  const hB = ex.submit('B', async () => { events.push('startB'); await gB.promise; events.push('endB'); });
  const hC = ex.submit('C', async () => { events.push('startC'); await gC.promise; events.push('endC'); });

  await flush();
  assert.deepEqual(events, ['startA', 'startB']);
  assert.equal(hC.state, 'queued');
  assert.equal(ex.runningCount, 2);

  gA.resolve(); // 释放一个额度，C 应立即补上
  await flush();
  assert.deepEqual(events, ['startA', 'startB', 'endA', 'startC']);
  assert.equal(hC.state, 'running');

  gB.resolve();
  gC.resolve();
  await flush();
  await Promise.all([hA.promise, hB.promise, hC.promise]);
  assert.equal(ex.runningCount, 0);
  assert.equal(ex.keyCount, 0);
});

test('并发额度为 1 时跨 key 按就绪顺序轮转', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const gates = [gate(), gate(), gate()];
  const hA1 = ex.submit('A', async () => { events.push('A1'); await gates[0]!.promise; });
  const hB1 = ex.submit('B', async () => { events.push('B1'); await gates[1]!.promise; });
  const hA2 = ex.submit('A', async () => { events.push('A2'); await gates[2]!.promise; });

  await flush();
  assert.deepEqual(events, ['A1']);
  gates[0]!.resolve();
  await flush();
  assert.deepEqual(events, ['A1', 'B1'], 'A 的后续作业应排在已就绪的 B 之后');
  gates[1]!.resolve();
  await flush();
  assert.deepEqual(events, ['A1', 'B1', 'A2']);
  gates[2]!.resolve();
  await flush();
  await Promise.all([hA1.promise, hB1.promise, hA2.promise]);
});

test('作业失败不阻塞同 key 后继，也不阻塞其他 key', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const failure = new Error('boom');
  const hA1 = ex.submit('A', async () => { events.push('A1'); throw failure; });
  const hB1 = ex.submit('B', async () => { events.push('B1'); });
  const hA2 = ex.submit('A', async () => { events.push('A2'); });

  await flush();
  await assert.rejects(hA1.promise, (e: unknown) => e === failure);
  assert.equal(hA1.state, 'failed');
  await hB1.promise;
  await hA2.promise;
  assert.deepEqual(events, ['A1', 'B1', 'A2'], '失败后 A2 与 B1 都应继续执行');
  assert.equal(hA2.state, 'completed');
  assert.equal(ex.keyCount, 0);
});

test('同步抛异常的作业按失败处理且不阻塞后继', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const h1 = ex.submit('k', () => { events.push('throw'); throw new Error('sync'); });
  const h2 = ex.submit('k', () => { events.push('next'); });

  await flush();
  await assert.rejects(h1.promise, /sync/);
  await h2.promise;
  assert.deepEqual(events, ['throw', 'next']);
});

test('排队作业可取消，运行中/已结束作业取消返回 false', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const g1 = gate();
  const h1 = ex.submit('k', async () => { events.push('run1'); await g1.promise; });
  const h2 = ex.submit('k', () => { events.push('run2'); });
  const h3 = ex.submit('k', () => { events.push('run3'); });

  await flush();
  assert.equal(h2.state, 'queued');
  assert.equal(h1.cancel(), false, '运行中的作业不可取消');
  assert.equal(h2.cancel(), true);
  assert.equal(h2.state, 'cancelled');
  assert.equal(h2.cancel(), false, '重复取消返回 false');
  await assert.rejects(h2.promise, (e: unknown) => e instanceof CancelledError);

  g1.resolve();
  await flush();
  await h1.promise;
  await h3.promise;
  assert.deepEqual(events, ['run1', 'run3'], '被取消的作业不应执行');
  assert.equal(ex.keyCount, 0);
  assert.equal(ex.pendingCount, 0);
});

test('取消最后一个排队作业会释放空闲 key 状态', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const g1 = gate();
  const h1 = ex.submit('k', async () => { await g1.promise; });
  const h2 = ex.submit('k', () => {});
  await flush();
  assert.equal(ex.keyCount, 1);
  h2.cancel();
  g1.resolve();
  await flush();
  await h1.promise;
  assert.equal(ex.keyCount, 0);
});

test('空闲 key 状态释放后可重新创建', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 2 });
  const h1 = ex.submit('k', () => 1);
  await h1.promise;
  await flush();
  assert.equal(ex.keyCount, 0);

  const h2 = ex.submit('k', () => 2);
  assert.equal(ex.keyCount, 1);
  assert.equal(await h2.promise, 2);
  await flush();
  assert.equal(ex.keyCount, 0);
});

test('运行中的作业同步提交同 key 任务：串行追加而非并发', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 2 });
  const events: string[] = [];
  const h1 = ex.submit('k', () => {
    events.push('start1');
    ex.submit('k', () => { events.push('start2'); });
    events.push('end1');
  });
  await flush();
  await h1.promise;
  assert.deepEqual(events, ['start1', 'end1', 'start2'], '内层提交应在外层完成后执行');
  assert.equal(ex.keyCount, 0);
  assert.equal(ex.pendingCount, 0);
});

test('完成回调中同步提交同 key 任务：清理与重建安全交错', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const h1 = ex.submit('k', () => { events.push('a1'); });

  const chained = h1.promise.then(() => {
    // 此时 key 状态已被同步清理
    assert.equal(ex.keyCount, 0);
    const h2 = ex.submit('k', () => { events.push('a2'); });
    assert.equal(ex.keyCount, 1, '新提交应重建 key 状态');
    return h2.promise;
  });

  await chained;
  assert.deepEqual(events, ['a1', 'a2']);
  await flush();
  assert.equal(ex.keyCount, 0, '链式作业完成后状态再次释放');
  assert.equal(ex.pendingCount, 0);
});

test('清理时新任务进入：与其他 key 竞争额度时不丢作业、不重复执行', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const gB = gate();
  const hA = ex.submit('A', () => { events.push('a1'); });
  const hB = ex.submit('B', async () => { events.push('b1'); await gB.promise; });

  // A 完成的回调里立刻补一个同 key 作业；此时 B 正在占用唯一额度
  const chained = hA.promise.then(() => {
    const hA2 = ex.submit('A', () => { events.push('a2'); });
    return hA2.promise;
  });

  await flush();
  assert.deepEqual(events, ['a1', 'b1']);
  gB.resolve();
  await chained;
  await hB.promise;
  assert.deepEqual(events, ['a1', 'b1', 'a2'], 'A2 应在 B 之后恰好执行一次');
  assert.equal(ex.keyCount, 0);
});

test('close 排空已排队作业后 resolve，重复调用返回同一 Promise', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const g1 = gate();
  const h1 = ex.submit('k', async () => { events.push('run1'); await g1.promise; });
  const h2 = ex.submit('k', () => { events.push('run2'); });
  const h3 = ex.submit('k', () => { events.push('run3'); });

  const closing = ex.close();
  assert.equal(ex.close(), closing);
  assert.equal(ex.isClosed, true);

  let closed = false;
  void closing.then(() => { closed = true; });
  await flush();
  assert.equal(closed, false, '还有作业未排空时不应 resolve');

  g1.resolve();
  await closing;
  await Promise.all([h1.promise, h2.promise, h3.promise]);
  assert.deepEqual(events, ['run1', 'run2', 'run3'], '关闭前排队的作业应全部排空');
  assert.equal(ex.pendingCount, 0);
  assert.equal(ex.keyCount, 0);
});

test('关闭后的新提交以 ExecutorClosedError 拒绝', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  await ex.close();
  const h = ex.submit('k', () => 'never');
  assert.equal(h.state, 'rejected');
  assert.equal(h.cancel(), false);
  await assert.rejects(h.promise, (e: unknown) => e instanceof ExecutorClosedError);
  assert.equal(ex.pendingCount, 0);
  assert.equal(ex.keyCount, 0);
});

test('关闭期间运行中作业的递归提交被确定性地拒绝，close 正常收敛', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const errors: unknown[] = [];
  const g1 = gate();

  const h1 = ex.submit('k', async () => {
    events.push('run1');
    await g1.promise;
    // 关闭期间递归提交：每次提交都应立即被拒绝，且不会阻塞 close
    for (let i = 0; i < 3; i++) {
      const h = ex.submit('k', () => { events.push('should-never-run'); });
      await h.promise.catch((e: unknown) => { errors.push(e); });
    }
    events.push('end1');
  });

  const closing = ex.close();
  g1.resolve();
  await closing;
  await h1.promise;

  assert.deepEqual(events, ['run1', 'end1'], '递归提交的作业不应执行');
  assert.equal(errors.length, 3);
  assert.ok(errors.every((e) => e instanceof ExecutorClosedError));
  assert.equal(ex.pendingCount, 0);
  assert.equal(ex.keyCount, 0);
});

test('关闭期间链式递归提交（拒绝后再提交）不会无限递归', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const g1 = gate();
  let chainAttempts = 0;
  let ran = 0;

  const h1 = ex.submit('k', async () => {
    await g1.promise;
    const chain = (): void => {
      chainAttempts++;
      const h = ex.submit('k', () => { ran++; chain(); });
      void h.promise.catch(() => {});
    };
    chain();
  });

  const closing = ex.close();
  g1.resolve();
  await closing;
  await h1.promise;

  assert.equal(chainAttempts, 1, '仅尝试一次，被拒绝后链式作业体不会运行');
  assert.equal(ran, 0);
});

test('关闭期间取消排队作业，close 仍能 resolve', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 1 });
  const events: string[] = [];
  const g1 = gate();
  const h1 = ex.submit('k', async () => { events.push('run1'); await g1.promise; });
  const h2 = ex.submit('k', () => { events.push('run2'); });

  const closing = ex.close();
  assert.equal(h2.cancel(), true);
  await assert.rejects(h2.promise, (e: unknown) => e instanceof CancelledError);

  g1.resolve();
  await closing;
  await h1.promise;
  assert.deepEqual(events, ['run1']);
  assert.equal(ex.pendingCount, 0);
  assert.equal(ex.keyCount, 0);
});

test('无待处理作业时 close 立即 resolve', async () => {
  const ex = new KeyedExecutor({ maxConcurrency: 2 });
  await ex.close();
  assert.equal(ex.isClosed, true);
});

test('maxConcurrency 非法时抛 RangeError', () => {
  assert.throws(() => new KeyedExecutor({ maxConcurrency: 0 }), RangeError);
  assert.throws(() => new KeyedExecutor({ maxConcurrency: 1.5 }), RangeError);
});
