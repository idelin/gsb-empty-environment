import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  InMemoryJournal,
  InMemoryVersionStore,
  PublishQueue,
  ScriptedExecutor,
  StoreBackedExecutor,
} from '../src/index.ts';
import type { PublishExecutor } from '../src/index.ts';

/** 单调递增的确定性时钟 */
function makeClock(): () => number {
  let tick = 0;
  return () => {
    tick += 1;
    return tick;
  };
}

function tick(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

test('同路径串行：版本链式推进，过期期望版本冲突', async () => {
  const store = new InMemoryVersionStore(makeClock());
  const queue = new PublishQueue({ executor: new StoreBackedExecutor(store), store });

  const h1 = queue.submit({ path: '/a', expectedVersion: null, digest: 'd1', operationId: 'op-1' });
  const h2 = queue.submit({ path: '/a', expectedVersion: 1, digest: 'd2', operationId: 'op-2' });
  const h3 = queue.submit({ path: '/a', expectedVersion: null, digest: 'd3', operationId: 'op-3' });
  const h4 = queue.submit({ path: '/a', expectedVersion: 1, digest: 'd4', operationId: 'op-4' });

  const o1 = await h1.promise;
  assert(o1.status === 'committed');
  assert.equal(o1.receipt.version, 1);

  const o2 = await h2.promise;
  assert(o2.status === 'committed');
  assert.equal(o2.receipt.version, 2);
  // 串行保证：op-2 在 op-1 提交之后才提交
  assert(o2.receipt.committedAt > o1.receipt.committedAt);

  // 版本竞争：期望“不存在”，但已推进到 v2
  const o3 = await h3.promise;
  assert(o3.status === 'conflict');
  assert.equal(o3.currentVersion, 2);

  // 版本竞争：期望旧版本 v1，但已推进到 v2
  const o4 = await h4.promise;
  assert(o4.status === 'conflict');
  assert.equal(o4.currentVersion, 2);

  // 冲突的发布未落盘
  assert.equal(store.get('/a')?.digest, 'd2');
  assert.equal(store.get('/a')?.version, 2);
});

test('跨路径并行：慢路径不阻塞其他路径独立推进', async () => {
  const store = new InMemoryVersionStore(makeClock());
  const executor = new ScriptedExecutor(new StoreBackedExecutor(store));
  executor.setScriptForPath('/slow', { delayMs: 80 });
  const queue = new PublishQueue({ executor, store });

  const slow = queue.submit({ path: '/slow', expectedVersion: null, digest: 's', operationId: 'op-slow' });
  const fast = queue.submit({ path: '/fast', expectedVersion: null, digest: 'f', operationId: 'op-fast' });

  const fastOutcome = await fast.promise;
  assert(fastOutcome.status === 'committed');
  // 快路径已完成时，慢路径仍在执行
  assert.equal(slow.status(), 'running');

  const slowOutcome = await slow.promise;
  assert(slowOutcome.status === 'committed');
  assert(fastOutcome.receipt.committedAt < slowOutcome.receipt.committedAt);
});

test('确认回执丢失：按幂等键对账恢复提交，重启后保持幂等', async () => {
  const store = new InMemoryVersionStore(makeClock());
  const journal = new InMemoryJournal();
  const executor = new ScriptedExecutor(new StoreBackedExecutor(store));
  executor.setScriptForOperation('op-lost', { failure: 'receipt-loss' });
  const queue = new PublishQueue({ executor, store, journal });

  const handle = queue.submit({ path: '/doc', expectedVersion: null, digest: 'v1', operationId: 'op-lost' });
  const outcome = await handle.promise;
  // 回执丢失但提交已生效：队列对账后仍给出已提交回执
  assert(outcome.status === 'committed');
  assert.equal(outcome.receipt.version, 1);
  assert.equal(outcome.receipt.digest, 'v1');

  // 模拟重启：新队列共享同一存储与日志
  const calls: string[] = [];
  const probingExecutor: PublishExecutor = {
    deliver: (command) => {
      calls.push(command.operationId);
      return Promise.reject(new Error('executor must not be called'));
    },
  };
  const restarted = new PublishQueue({ executor: probingExecutor, store, journal });

  const recovered = restarted.recover('op-lost');
  assert(recovered?.status === 'committed');
  assert.equal(recovered.receipt.version, 1);

  // 幂等重放：相同 operationId 直接返回已记录结果，不重新执行
  const replay = restarted.submit({ path: '/doc', expectedVersion: null, digest: 'v1', operationId: 'op-lost' });
  const replayOutcome = await replay.promise;
  assert(replayOutcome.status === 'committed');
  assert.equal(replayOutcome.receipt.version, 1);
  assert.deepEqual(calls, []);
});

test('取消：确认点前可取消，确认后返回已提交', async () => {
  const store = new InMemoryVersionStore(makeClock());
  const executor = new ScriptedExecutor(new StoreBackedExecutor(store));
  const queue = new PublishQueue({ executor, store });

  // 排队中取消：前面有慢操作占着同路径的执行链
  executor.setScriptForOperation('op-head', { delayMs: 50 });
  const head = queue.submit({ path: '/c', expectedVersion: null, digest: 'h', operationId: 'op-head' });
  const queued = queue.submit({ path: '/c', expectedVersion: 1, digest: 'q', operationId: 'op-queued' });
  assert.equal(queued.status(), 'queued');
  assert.equal(queued.cancel(), 'cancelled');
  const queuedOutcome = await queued.promise;
  assert(queuedOutcome.status === 'cancelled');

  const headOutcome = await head.promise;
  assert(headOutcome.status === 'committed');
  // 被取消的操作没有落盘，链也未中断
  assert.equal(store.get('/c')?.version, 1);
  assert.equal(store.get('/c')?.digest, 'h');

  // 执行中取消（确认点前）：注入延迟，取消信号中断投递
  executor.setScriptForOperation('op-run', { delayMs: 50 });
  const running = queue.submit({ path: '/r', expectedVersion: null, digest: 'r', operationId: 'op-run' });
  await tick();
  assert.equal(running.status(), 'running');
  assert.equal(running.cancel(), 'running');
  const runningOutcome = await running.promise;
  assert(runningOutcome.status === 'cancelled');
  assert.equal(store.get('/r'), undefined);

  // 确认后取消：返回已提交，不能宣称撤销
  const done = queue.submit({ path: '/d', expectedVersion: null, digest: 'd', operationId: 'op-done' });
  const doneOutcome = await done.promise;
  assert(doneOutcome.status === 'committed');
  assert.equal(done.cancel(), 'committed');
  assert.equal(done.status(), 'committed');
  assert.equal(store.get('/d')?.digest, 'd');
});

test('失败注入：确认点前失败无副作用，后续同路径发布继续推进', async () => {
  const store = new InMemoryVersionStore(makeClock());
  const executor = new ScriptedExecutor(new StoreBackedExecutor(store));
  executor.setScriptForOperation('op-fail', { failure: 'before-commit' });
  const queue = new PublishQueue({ executor, store });

  const failed = queue.submit({ path: '/f', expectedVersion: null, digest: 'x', operationId: 'op-fail' });
  const next = queue.submit({ path: '/f', expectedVersion: null, digest: 'y', operationId: 'op-next' });

  const failedOutcome = await failed.promise;
  assert(failedOutcome.status === 'failed');
  assert.equal(store.get('/f'), undefined);

  const nextOutcome = await next.promise;
  assert(nextOutcome.status === 'committed');
  assert.equal(nextOutcome.receipt.version, 1);
});
