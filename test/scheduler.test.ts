import { test } from 'node:test';
import assert from 'node:assert/strict';

import { MultiTenantScheduler, QuotaExceededError } from '../src/scheduler/scheduler.ts';

const flush = (): Promise<void> => new Promise((resolve) => setImmediate(resolve));

function makeClock(start = 0): { now: () => number; advance: (ms: number) => void } {
  let t = start;
  return {
    now: () => t,
    advance: (ms: number) => {
      t += ms;
    },
  };
}

test('租户注册与参数校验', () => {
  const s = new MultiTenantScheduler();
  s.registerTenant({ id: 'a' });
  assert.throws(() => s.registerTenant({ id: 'a' }), /已注册/);
  assert.throws(() => s.registerTenant({ id: 'b', weight: 0 }), /weight/);
  assert.throws(() => s.registerTenant({ id: 'c', quotaTokens: 0 }), /quotaTokens/);
  assert.throws(() => s.registerTenant({ id: 'd', maxConcurrency: 0 }), /maxConcurrency/);
  assert.throws(() => s.submit('ghost', 1, () => 1), /未注册的租户/);
  assert.throws(() => s.submit('a', 0, () => 1), /size/);
  assert.throws(() => s.submit('a', Number.NaN, () => 1), /size/);
  s.assertNoLeaks();
});

test('DRR 加权公平：权重 1:2 的精确派发序列', async () => {
  const s = new MultiTenantScheduler({ baseQuantum: 1, maxInflight: 3 });
  s.registerTenant({ id: 'A', weight: 1 });
  s.registerTenant({ id: 'B', weight: 2 });

  const order: string[] = [];
  const gates: Array<() => void> = [];
  const makeJob = (label: string) => () => {
    order.push(label);
    return new Promise<void>((resolve) => gates.push(resolve));
  };

  const promises: Promise<unknown>[] = [];
  for (let i = 1; i <= 6; i++) promises.push(s.submit('A', 1, makeJob('A' + i)));
  for (let i = 1; i <= 6; i++) promises.push(s.submit('B', 1, makeJob('B' + i)));

  await flush();
  assert.deepEqual(order, ['A1', 'A2', 'A3']);

  for (let i = 0; i < 12; i++) {
    gates[i]();
    await flush();
  }
  await Promise.all(promises);

  assert.deepEqual(order, ['A1', 'A2', 'A3', 'A4', 'B1', 'A5', 'B2', 'A6', 'B3', 'B4', 'B5', 'B6']);
  assert.equal(s.stats('A').completed, 6);
  assert.equal(s.stats('B').completed, 6);
  await s.drain();
  s.assertNoLeaks();
});

test('配额限流：耗尽即暂停，窗口推进后恢复，其他租户不受阻塞', async () => {
  const clock = makeClock();
  const s = new MultiTenantScheduler({ now: clock.now });
  s.registerTenant({ id: 'X', quotaTokens: 2, refillIntervalMs: 1000 });
  s.registerTenant({ id: 'Y', quotaTokens: 100, refillIntervalMs: 1000 });

  const done: string[] = [];
  const job = (label: string) => () => {
    done.push(label);
    return label;
  };
  const promises: Promise<unknown>[] = [];
  for (let i = 1; i <= 5; i++) promises.push(s.submit('X', 1, job('X' + i)));
  for (let i = 1; i <= 5; i++) promises.push(s.submit('Y', 1, job('Y' + i)));
  await flush();

  assert.deepEqual(done, ['X1', 'X2', 'Y1', 'Y2', 'Y3', 'Y4', 'Y5']);
  assert.equal(s.stats('X').tokens, 0);
  assert.equal(s.stats('X').queued, 3);

  clock.advance(1000);
  s.tick();
  await flush();
  assert.deepEqual(done.slice(7), ['X3', 'X4']);
  assert.equal(s.stats('X').queued, 1);

  clock.advance(1000);
  s.tick();
  await flush();
  assert.deepEqual(done.slice(9), ['X5']);

  await Promise.all(promises);
  await s.drain();
  s.assertNoLeaks();
});

test('配额不累积超发：补充上限为窗口配额', async () => {
  const clock = makeClock();
  const s = new MultiTenantScheduler({ now: clock.now });
  s.registerTenant({ id: 'X', quotaTokens: 2, refillIntervalMs: 1000 });
  clock.advance(10_000);
  s.tick();
  assert.equal(s.stats('X').tokens, 2);
  s.assertNoLeaks();
});

test('per-tenant 并发上限隔离：打满自身额度不影响其他租户', async () => {
  const s = new MultiTenantScheduler();
  s.registerTenant({ id: 'slow', maxConcurrency: 2 });
  s.registerTenant({ id: 'fast' });

  const gates: Array<() => void> = [];
  const gated = () => () => new Promise<void>((resolve) => gates.push(resolve));
  const promises: Promise<unknown>[] = [];
  for (let i = 0; i < 5; i++) promises.push(s.submit('slow', 1, gated()));

  const fastDone: number[] = [];
  for (let i = 0; i < 5; i++) {
    promises.push(s.submit('fast', 1, () => {
      fastDone.push(i);
    }));
  }
  await flush();

  assert.equal(s.stats('slow').inflight, 2);
  assert.equal(s.stats('slow').queued, 3);
  assert.equal(fastDone.length, 5);

  while (gates.length > 0) {
    const pending = gates.splice(0);
    for (const g of pending) g();
    await flush();
  }
  await Promise.all(promises);
  await s.drain();
  s.assertNoLeaks();
});

test('队列上限拒绝：隔离保护返回 QuotaExceededError', async () => {
  const clock = makeClock();
  const s = new MultiTenantScheduler({ now: clock.now });
  s.registerTenant({ id: 'q', quotaTokens: 1, refillIntervalMs: 10_000, maxQueueSize: 2 });

  const gates: Array<() => void> = [];
  const gated = () => () => new Promise<void>((resolve) => gates.push(resolve));

  const p1 = s.submit('q', 1, gated());
  const p2 = s.submit('q', 1, gated());
  const p3 = s.submit('q', 1, gated());
  await assert.rejects(s.submit('q', 1, gated()), (err: unknown) => {
    assert.ok(err instanceof QuotaExceededError);
    assert.match((err as Error).message, /隔离保护/);
    return true;
  });
  assert.equal(s.stats('q').rejected, 1);
  assert.equal(s.stats('q').queued, 2);

  clock.advance(10_000);
  s.tick();
  clock.advance(10_000);
  s.tick();
  await flush();
  while (gates.length > 0) {
    const pending = gates.splice(0);
    for (const g of pending) g();
    await flush();
  }
  await Promise.all([p1, p2, p3]);
  await s.drain();
  s.assertNoLeaks();
});

test('任务异常：错误传播给调用方且状态干净', async () => {
  const s = new MultiTenantScheduler();
  s.registerTenant({ id: 'a' });
  const boom = new Error('boom');
  const ok = s.submit('a', 1, () => 'fine');
  const bad = s.submit('a', 1, () => {
    throw boom;
  });
  const rej = s.submit('a', 1, () => Promise.reject(new Error('async-boom')));
  assert.equal(await ok, 'fine');
  await assert.rejects(bad, (err: unknown) => err === boom);
  await assert.rejects(rej, /async-boom/);
  const st = s.stats('a');
  assert.equal(st.completed, 1);
  assert.equal(st.failed, 2);
  await s.drain();
  s.assertNoLeaks();
});

test('并发压力：5 租户 x 200 任务全部完成且严格无泄漏', async () => {
  const s = new MultiTenantScheduler({ baseQuantum: 4, maxInflight: 8 });
  const tenants = ['t1', 't2', 't3', 't4', 't5'];
  tenants.forEach((id, i) => s.registerTenant({ id, weight: i + 1 }));

  const promises: Promise<string>[] = [];
  for (const id of tenants) {
    for (let i = 0; i < 200; i++) {
      const size = (i % 3) + 1;
      promises.push(s.submit(id, size, () => id + ':' + i));
    }
  }
  const results = await Promise.all(promises);
  assert.equal(results.length, 1000);
  const perTenant = new Map<string, number>();
  for (const r of results) {
    const [id, idx] = r.split(':');
    perTenant.set(id, (perTenant.get(id) ?? 0) + 1);
    assert.ok(Number(idx) >= 0 && Number(idx) < 200);
  }
  for (const id of tenants) {
    assert.equal(perTenant.get(id), 200);
    const st = s.stats(id);
    assert.equal(st.submitted, 200);
    assert.equal(st.completed, 200);
    assert.equal(st.failed, 0);
    assert.equal(st.rejected, 0);
  }
  await s.drain();
  s.assertNoLeaks();
});

test('drain：空闲立即返回，挂起期间在完成后兑现', async () => {
  const s = new MultiTenantScheduler();
  s.registerTenant({ id: 'a' });
  await s.drain();

  let release!: () => void;
  const p = s.submit('a', 1, () => new Promise<void>((resolve) => {
    release = resolve;
  }));
  let drained = false;
  const draining = s.drain().then(() => {
    drained = true;
  });
  await flush();
  assert.equal(drained, false);
  release();
  await p;
  await draining;
  assert.equal(drained, true);
  s.assertNoLeaks();
});

test('大尺寸任务等待 deficit 累积后再派发', async () => {
  const s = new MultiTenantScheduler({ baseQuantum: 1, maxInflight: 1 });
  s.registerTenant({ id: 'a', weight: 1 });

  const order: string[] = [];
  const gates: Array<() => void> = [];
  const gated = (label: string) => () => {
    order.push(label);
    return new Promise<void>((resolve) => gates.push(resolve));
  };

  const p1 = s.submit('a', 1, gated('small'));
  const p2 = s.submit('a', 3, gated('big'));
  await flush();
  assert.deepEqual(order, ['small']);
  assert.equal(s.stats('a').queued, 1);

  gates[0]();
  await flush();
  assert.deepEqual(order, ['small', 'big']);
  gates[1]();
  await Promise.all([p1, p2]);
  await s.drain();
  s.assertNoLeaks();
});
