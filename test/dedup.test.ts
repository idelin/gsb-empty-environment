import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RequestDeduplicator, UNDETERMINED } from '../src/dedup.ts';
import { ManualClock } from '../src/clock.ts';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function setup(ttl: { pendingTtlMs?: number; resultTtlMs?: number } = {}) {
  const clock = new ManualClock(0);
  const dedup = new RequestDeduplicator({
    pendingTtlMs: ttl.pendingTtlMs ?? 1_000,
    resultTtlMs: ttl.resultTtlMs ?? 5_000,
    clock,
  });
  return { clock, dedup };
}

test('首次执行运行 fn 并缓存结果，窗口内重试直接返回缓存', async () => {
  const { clock, dedup } = setup();
  const g = dedup.begin('k');
  let calls = 0;

  const first = await dedup.execute('k', g, async () => {
    calls += 1;
    return 'v1';
  });
  assert.deepEqual(first, { status: 'ok', value: 'v1' });

  clock.advance(100);
  const retry = await dedup.execute('k', g, async () => {
    calls += 1;
    return 'v2';
  });
  assert.deepEqual(retry, { status: 'ok', value: 'v1' });
  assert.equal(calls, 1);
});

test('结果未产生时并发请求合并等待，fn 只执行一次', async () => {
  const { dedup } = setup();
  const g = dedup.begin('k');
  const gate = deferred<string>();
  let calls = 0;

  const fn = async () => {
    calls += 1;
    return gate.promise;
  };
  const p1 = dedup.execute('k', g, fn);
  const p2 = dedup.execute('k', g, fn);
  const p3 = dedup.execute('k', g, fn);

  gate.resolve('shared');
  const [r1, r2, r3] = await Promise.all([p1, p2, p3]);
  assert.equal(calls, 1);
  for (const r of [r1, r2, r3]) {
    assert.deepEqual(r, { status: 'ok', value: 'shared' });
  }
});

test('窗口过期后的重试返回无法判定，且绝不作为新请求执行', async () => {
  const { clock, dedup } = setup();
  const g = dedup.begin('k');
  let calls = 0;

  await dedup.execute('k', g, async () => {
    calls += 1;
    return 'done';
  });
  assert.equal(calls, 1);

  clock.advance(10_000); // 超出 resultTtlMs
  const late = await dedup.execute('k', g, async () => {
    calls += 1;
    return 're-executed';
  });
  assert.deepEqual(late, { status: 'undetermined' });
  assert.equal(calls, 1, '过期请求不得触发再次执行');
});

test('未知代次（从未 begin 或代次不符）返回无法判定且不执行', async () => {
  const { dedup } = setup();
  const g = dedup.begin('k');
  let calls = 0;
  const fn = async () => {
    calls += 1;
    return 'x';
  };

  assert.deepEqual(await dedup.execute('k', g + 100, fn), UNDETERMINED);
  assert.deepEqual(await dedup.execute('never-begun', 1, fn), UNDETERMINED);
  assert.equal(calls, 0);
});

test('记录过期后 begin 签发新代次：旧代次仍无法判定，新代次可显式执行', async () => {
  const { clock, dedup } = setup();
  const g1 = dedup.begin('k');
  await dedup.execute('k', g1, async () => 'first');

  clock.advance(10_000);
  const g2 = dedup.begin('k');
  assert.notEqual(g2, g1);

  let calls = 0;
  const stale = await dedup.execute('k', g1, async () => {
    calls += 1;
    return 'stale';
  });
  assert.deepEqual(stale, { status: 'undetermined' });
  assert.equal(calls, 0);

  const fresh = await dedup.execute('k', g2, async () => {
    calls += 1;
    return 'fresh';
  });
  assert.deepEqual(fresh, { status: 'ok', value: 'fresh' });
  assert.equal(calls, 1);
});

test('记录存活期间 begin 幂等返回同一代次', async () => {
  const { clock, dedup } = setup();
  const g1 = dedup.begin('k');
  clock.advance(500);
  assert.equal(dedup.begin('k'), g1);
  clock.advance(600); // 超过 pendingTtlMs=1000
  assert.notEqual(dedup.begin('k'), g1);
});

test('窗口清理与首次完成同刻发生：完成优先，结果保留且同刻可重试', async () => {
  const pendingTtlMs = 1_000;
  const resultTtlMs = 5_000;
  const { clock, dedup } = setup({ pendingTtlMs, resultTtlMs });
  const g = dedup.begin('k'); // pending 记录 expiresAt = 1000
  const gate = deferred<string>();
  let calls = 0;

  const pending = dedup.execute('k', g, async () => {
    calls += 1;
    return gate.promise;
  });

  // 时钟推进到与 pending 过期完全同刻，此刻 fn 才完成
  clock.set(1_000);
  gate.resolve('just-in-time');
  const first = await pending;
  assert.deepEqual(first, { status: 'ok', value: 'just-in-time' });

  // 同一时刻执行窗口清理：完成已把窗口刷新为 1000+5000，记录必须存活
  const removed = dedup.sweep(1_000);
  assert.equal(removed, 0, '同刻完成的结果不得被清理');

  // 同刻到达的重试应命中缓存结果而非无法判定
  const retry = await dedup.execute('k', g, async () => {
    calls += 1;
    return 'again';
  });
  assert.deepEqual(retry, { status: 'ok', value: 'just-in-time' });
  assert.equal(calls, 1);

  // 结果窗口在同刻起算，窗口尽头才真正过期
  clock.set(6_000);
  assert.equal(dedup.sweep(), 1);
  const late = await dedup.execute('k', g, async () => 'late');
  assert.deepEqual(late, { status: 'undetermined' });
});

test('不调用 sweep 时访问路径上的惰性过期同样生效', async () => {
  const { clock, dedup } = setup();
  const g = dedup.begin('k');
  await dedup.execute('k', g, async () => 'v');

  clock.advance(10_000); // 无 sweep，仅靠惰性过期
  let calls = 0;
  const late = await dedup.execute('k', g, async () => {
    calls += 1;
    return 'x';
  });
  assert.deepEqual(late, { status: 'undetermined' });
  assert.equal(calls, 0);
  assert.equal(dedup.has('k'), false);
});

test('pending 记录超期未完成后，迟到的重试返回无法判定', async () => {
  const { clock, dedup } = setup({ pendingTtlMs: 500 });
  const g = dedup.begin('k');
  const gate = deferred<string>();
  const pending = dedup.execute('k', g, () => gate.promise);

  clock.advance(600); // 超过 pendingTtlMs，fn 仍未完成
  let calls = 0;
  const retry = await dedup.execute('k', g, async () => {
    calls += 1;
    return 'retry';
  });
  assert.deepEqual(retry, { status: 'undetermined' });
  assert.equal(calls, 0);

  // 已挂接的等待者仍能收到迟到的结果，但结果不再落库
  gate.resolve('late');
  assert.deepEqual(await pending, { status: 'ok', value: 'late' });
  assert.equal(dedup.has('k'), false);
});

test('fn 失败作为终态缓存：等待者共享错误，窗口内重试不重复执行', async () => {
  const { clock, dedup } = setup();
  const g = dedup.begin('k');
  const boom = new Error('boom');
  let calls = 0;
  const fn = async () => {
    calls += 1;
    throw boom;
  };

  const first = await dedup.execute('k', g, fn);
  assert.deepEqual(first, { status: 'error', error: boom });

  clock.advance(100);
  const retry = await dedup.execute('k', g, fn);
  assert.deepEqual(retry, { status: 'error', error: boom });
  assert.equal(calls, 1);
});

test('sweep 只清除到期记录，返回清除数量', async () => {
  const { clock, dedup } = setup({ pendingTtlMs: 1_000, resultTtlMs: 2_000 });
  const g1 = dedup.begin('a');
  const g2 = dedup.begin('b');
  await dedup.execute('a', g1, async () => 'done-a'); // expiresAt = 2000
  void g2; // b 仍为 pending，expiresAt = 1000

  clock.set(1_500);
  assert.equal(dedup.sweep(), 1); // 仅 b 到期
  assert.equal(dedup.has('a'), true);
  assert.equal(dedup.has('b'), false);
});
