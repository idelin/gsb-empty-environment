import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Store,
  delay,
  isAbortError,
  type Cell,
  type RunContext,
  type SnapshotEntry,
} from '../src/store.ts';

async function waitUntil(cond: () => boolean, timeoutMs = 3000): Promise<void> {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitUntil 超时');
    await new Promise(resolve => setTimeout(resolve, 5));
  }
}

/** 订阅并记录每一次状态通知（订阅即收到当前快照）。 */
function track<T>(cell: Cell<T>): string[] {
  const seen: string[] = [];
  cell.subscribe(s => seen.push(`${s.status}:${String(s.value)}`));
  return seen;
}

test('派生任务：pending → success，状态可订阅', async () => {
  const store = new Store();
  const q = store.source('q', 'cat');
  const search = store.derived('search', [q], async (ctx, query) => {
    await delay(10, ctx.signal);
    return `结果:${query}`;
  });
  const seen = track(search);
  await store.whenSettled();
  assert.equal(search.get().status, 'success');
  assert.equal(search.get().value, '结果:cat');
  assert.deepEqual(seen, ['pending:undefined', 'success:结果:cat']);
});

test('先慢请求后快请求：旧代际结果被拒绝，只显示快结果', async () => {
  const store = new Store();
  const q = store.source('q', 'cat');
  const latency: Record<string, number> = { cat: 120, dog: 15 };
  let calls = 0;
  const search = store.derived('search', [q], async (ctx, query) => {
    calls++;
    await delay(latency[query]!, ctx.signal);
    return `结果:${query}`;
  });
  const seen = track(search);
  await waitUntil(() => calls === 1); // 慢任务 cat(120ms) 已启动
  q.set('dog'); // 依赖变化：取消慢任务，启动快任务 dog(15ms)
  await store.whenSettled();
  assert.equal(search.get().status, 'success');
  assert.equal(search.get().value, '结果:dog');
  // 慢任务的结果从未出现，且取消没有产生 error 状态
  assert.deepEqual(seen, ['pending:undefined', 'success:结果:dog']);
  assert.equal(calls, 2);
});

test('取消不被当作业务错误', async () => {
  const store = new Store();
  const q = store.source('q', 'a');
  let calls = 0;
  const cell = store.derived('r', [q], async (ctx, query) => {
    calls++;
    await delay(query === 'a' ? 100 : 10, ctx.signal);
    return `ok:${query}`;
  });
  const seen = track(cell);
  await waitUntil(() => calls === 1);
  q.set('b'); // 取消 a 的任务
  await store.whenSettled();
  assert.equal(cell.get().status, 'success');
  assert.equal(cell.get().error, undefined);
  assert.ok(seen.every(s => !s.startsWith('error'))); // 从未出现 error 通知
});

test('过期任务的业务错误同样按代际拒绝', async () => {
  const store = new Store();
  const q = store.source('q', 'a');
  let calls = 0;
  const cell = store.derived('r', [q], async (_ctx, query) => {
    calls++;
    if (query === 'a') {
      await delay(80); // 不响应取消的慢任务，最终以业务错误失败
      throw new Error('过期的慢错误');
    }
    await delay(10);
    return 'ok:b';
  });
  const seen = track(cell);
  await waitUntil(() => calls === 1);
  q.set('b');
  await store.whenSettled(); // 会等慢任务也落定（inflight 统计）
  assert.equal(cell.get().status, 'success');
  assert.equal(cell.get().value, 'ok:b');
  assert.ok(seen.every(s => !s.startsWith('error'))); // 过期错误被代际拒绝
});

test('依赖变化回到 pending，随后恢复 success', async () => {
  const store = new Store();
  const q = store.source('q', 'a');
  const cell = store.derived('r', [q], async (ctx, query) => {
    await delay(10, ctx.signal);
    return `ok:${query}`;
  });
  const statuses: string[] = [];
  cell.subscribe(s => statuses.push(s.status));
  await store.whenSettled();
  q.set('b');
  await store.whenSettled();
  assert.deepEqual(statuses, ['pending', 'success', 'pending', 'success']);
  assert.equal(cell.get().value, 'ok:b');
});

test('业务错误进入 error 状态，依赖更新后恢复', async () => {
  const store = new Store();
  const flag = store.source('flag', true);
  const cell = store.derived('r', [flag], async (_ctx, fail) => {
    if (fail) throw new Error('服务不可用');
    return 'ok';
  });
  await store.whenSettled();
  assert.equal(cell.get().status, 'error');
  assert.equal((cell.get().error as Error).message, '服务不可用');
  flag.set(false);
  await store.whenSettled();
  assert.equal(cell.get().status, 'success');
  assert.equal(cell.get().value, 'ok');
  assert.equal(cell.get().error, undefined);
});

test('共享依赖不重复执行同一任务', async () => {
  const store = new Store();
  const user = store.source('user', 'u1');
  let runs = 0;
  const fetcher = async (ctx: RunContext, uid: string) => {
    runs++;
    await delay(10, ctx.signal);
    return [`订单-${uid}`];
  };
  const a = store.shared('orders', [user], fetcher);
  const b = store.shared('orders', [user], fetcher);
  assert.equal(a, b); // 同一 key 返回同一单元
  await store.whenSettled();
  assert.equal(runs, 1); // 两个消费者只触发一次执行
  user.set('u2');
  await store.whenSettled();
  assert.equal(runs, 2); // 依赖变化只重跑一次
  assert.deepEqual(a.get().value, ['订单-u2']);
});

test('事务：多次写入一次提交，读者看不到中间态', async () => {
  const store = new Store();
  const first = store.source('first', 'Ada');
  const last = store.source('last', 'Byron');
  let runs = 0;
  const full = store.derived('full', [first, last], async (_ctx, f, l) => {
    runs++;
    return `${f} ${l}`;
  });
  const fullSeen = track(full);
  // first 的订阅者在通知里读 last：若存在中间态会读到 'Grace+Byron'
  const observed: string[] = [];
  first.subscribe(s => observed.push(`${String(s.value)}+${String(last.get().value)}`));
  await store.whenSettled();
  store.transaction(() => {
    first.set('Grace');
    last.set('Hopper');
  });
  await store.whenSettled();
  assert.deepEqual(observed, ['Ada+Byron', 'Grace+Hopper']); // 无中间态
  assert.deepEqual(fullSeen, [
    'pending:undefined',
    'success:Ada Byron',
    'pending:Ada Byron', // stale-while-revalidate：保留旧值
    'success:Grace Hopper',
  ]);
  assert.equal(runs, 2); // 派生任务只按终态重跑一次
});

test('事务回滚：抛错时丢弃全部缓冲写入，且不支持嵌套', () => {
  const store = new Store();
  const a = store.source('a', 1);
  const b = store.source('b', 2);
  assert.throws(() =>
    store.transaction(() => {
      a.set(10);
      b.set(20);
      throw new Error('boom');
    }),
  );
  assert.equal(a.get().value, 1); // 未被污染
  assert.equal(b.get().value, 2);
  assert.equal(store.getReplayLog().length, 0); // 回滚不产生日志
  assert.throws(() => store.transaction(() => store.transaction(() => {})));
});

test('命令日志可回放：重建一致的最终状态', async () => {
  const build = (store: Store) => {
    const q = store.source('q', 'cat');
    const n = store.source('n', 1);
    store.derived('search', [q, n], async (ctx, query, num) => {
      await delay(5, ctx.signal);
      return `${query}#${num}`;
    });
    return { q, n };
  };

  const s1 = new Store();
  const { q, n } = build(s1);
  await s1.whenSettled();
  q.set('dog');
  s1.transaction(() => {
    q.set('fish');
    n.set(2);
  });
  await s1.whenSettled();

  const s2 = Store.replay(s1.getReplayLog(), build);
  await s2.whenSettled();

  const strip = (snap: SnapshotEntry[]) => snap.map(({ version, ...rest }) => rest);
  assert.deepEqual(strip(s2.snapshot()), strip(s1.snapshot())); // 最终状态一致
  assert.deepEqual(s2.getReplayLog(), s1.getReplayLog()); // 回放出的日志仍可继续回放
  assert.equal(s2.getCell('search')!.get().value, 'fish#2');
});

test('isAbortError 识别取消，delay 响应 AbortSignal', async () => {
  const controller = new AbortController();
  const p = delay(1000, controller.signal);
  controller.abort();
  await assert.rejects(p, (err: unknown) => isAbortError(err));
  assert.ok(isAbortError(new DOMException('Aborted', 'AbortError')));
  assert.ok(!isAbortError(new Error('普通错误')));
  assert.ok(!isAbortError(null));
});
