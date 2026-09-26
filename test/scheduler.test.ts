import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Scheduler } from '../src/scheduler.ts';
import type { TaskEvent } from '../src/scheduler.ts';

function makeScheduler(executor: (task: { key: string }) => unknown = (t) => `value:${t.key}`) {
  let generation = 0;
  const scheduler = new Scheduler(executor, () => generation);
  return {
    scheduler,
    bumpGeneration: () => { generation++; },
  };
}

function collect<T>() {
  const events: TaskEvent<T>[] = [];
  return { events, subscriber: (e: TaskEvent<T>) => events.push(e) };
}

test('同键合并：两个请求共享一个底层任务', () => {
  const { scheduler } = makeScheduler();
  const a = collect<string>();
  const b = collect<string>();
  scheduler.request<string>('k').subscribe(a.subscriber);
  scheduler.request<string>('k').subscribe(b.subscriber);
  scheduler.pump();
  scheduler.flush();
  assert.equal(scheduler.executorCalls, 1); // 只执行一次
  assert.deepEqual(a.events, [{ type: 'resolved', value: 'value:k' }]);
  assert.deepEqual(b.events, [{ type: 'resolved', value: 'value:k' }]);
});

test('取消一个订阅者不取消其他等待者', () => {
  const { scheduler } = makeScheduler();
  const a = collect<string>();
  const b = collect<string>();
  const handle = scheduler.request<string>('k');
  const subA = handle.subscribe(a.subscriber);
  handle.subscribe(b.subscriber);
  subA.cancel();
  scheduler.pump();
  scheduler.flush();
  assert.equal(scheduler.executorCalls, 1); // 任务仍执行
  assert.deepEqual(a.events, []);
  assert.deepEqual(b.events, [{ type: 'resolved', value: 'value:k' }]);
});

test('所有订阅者取消后任务被取消，执行器不再运行', () => {
  const { scheduler } = makeScheduler();
  const handle = scheduler.request<string>('k');
  const sub = handle.subscribe(() => {});
  sub.cancel();
  assert.equal(scheduler.pump(), 0);
  assert.equal(scheduler.executorCalls, 0);
});

test('共享依赖：取消一个消费方不影响共享的字体/簇任务', () => {
  const { scheduler } = makeScheduler();
  const font = scheduler.request('font:serif@1');
  const clusterA = scheduler.request('cluster:a', [font]);
  const clusterB = scheduler.request('cluster:b', [font]);
  const a = collect<string>();
  const b = collect<string>();
  const subA = clusterA.subscribe(a.subscriber);
  clusterB.subscribe(b.subscriber);
  subA.cancel(); // 取消 A：font 任务因 B 仍在等待而保留
  for (;;) { // 依赖链逐层就绪，驱动到静默
    const started = scheduler.pump();
    const delivered = scheduler.flush();
    if (started === 0 && delivered === 0) break;
  }
  assert.deepEqual(a.events, []);
  assert.deepEqual(b.events, [{ type: 'resolved', value: 'value:cluster:b' }]);
  // font 只执行一次（被两个簇任务共享）
  const fontCalls = scheduler.executorCalls;
  assert.equal(fontCalls, 2); // font:serif@1 + cluster:b
});

test('旧回包：代际前进后交付的结果被丢弃', () => {
  const { scheduler, bumpGeneration } = makeScheduler();
  const a = collect<string>();
  scheduler.request<string>('k').subscribe(a.subscriber);
  scheduler.pump(); // 任务在代际 0 启动
  bumpGeneration(); // 字体热更新，代际前进
  scheduler.flush(); // 旧回包到达
  assert.deepEqual(a.events, [{ type: 'stale' }]);
});

test('旧回包：complete 注入的过期值不得交付', () => {
  const { scheduler, bumpGeneration } = makeScheduler();
  const a = collect<string>();
  scheduler.request<string>('k').subscribe(a.subscriber);
  scheduler.pump();
  bumpGeneration();
  const delivered = scheduler.complete('k', 'stale-value');
  assert.equal(delivered, true);
  assert.deepEqual(a.events, [{ type: 'stale' }]);
});

test('旧回包级联：依赖失效使下游任务一并失效', () => {
  const { scheduler, bumpGeneration } = makeScheduler();
  const font = scheduler.request('font:serif@1');
  const cluster = scheduler.request('cluster:x', [font]);
  const a = collect<string>();
  cluster.subscribe(a.subscriber);
  scheduler.pump(); // 只有 font 就绪并启动
  bumpGeneration();
  scheduler.flush(); // font 旧回包 -> stale -> 级联 cluster stale
  assert.deepEqual(a.events, [{ type: 'stale' }]);
});

test('乱序回包可控：complete 按指定键交付', () => {
  const { scheduler } = makeScheduler();
  const a = collect<string>();
  const b = collect<string>();
  scheduler.request<string>('k1').subscribe(a.subscriber);
  scheduler.request<string>('k2').subscribe(b.subscriber);
  scheduler.pump();
  assert.deepEqual(scheduler.pendingKeys(), ['k1', 'k2']);
  scheduler.complete('k2'); // 先回 k2
  assert.deepEqual(b.events, [{ type: 'resolved', value: 'value:k2' }]);
  assert.deepEqual(a.events, []);
  scheduler.complete('k1');
  assert.deepEqual(a.events, [{ type: 'resolved', value: 'value:k1' }]);
});
