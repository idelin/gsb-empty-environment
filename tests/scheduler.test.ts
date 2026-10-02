import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';

import {
  TenantScheduler,
  QuotaExceededError,
  UnknownTenantError,
} from '../src/scheduler.ts';

/** Deferred promise for precise control over task completion. */
function deferred<T = void>(): {
  promise: Promise<T>;
  resolve: (v: T | PromiseLike<T>) => void;
  reject: (e: unknown) => void;
} {
  let resolve!: (v: T | PromiseLike<T>) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test('rejects tasks for unknown tenants', async () => {
  const s = new TenantScheduler([{ id: 'a', weight: 1, maxConcurrent: 1 }]);
  await assert.rejects(() => s.submit('ghost', async () => 1), UnknownTenantError);
  await assert.rejects(() => s.submit('ghost', async () => 1), (e: unknown) => {
    return e instanceof UnknownTenantError && e.tenantId === 'ghost';
  });
});

test('validates tenant configuration at construction', () => {
  assert.throws(() => new TenantScheduler([{ id: 'a', weight: 0, maxConcurrent: 1 }]));
  assert.throws(() => new TenantScheduler([{ id: 'a', weight: 1, maxConcurrent: 0 }]));
  assert.throws(
    () =>
      new TenantScheduler([
        { id: 'a', weight: 1, maxConcurrent: 1 },
        { id: 'a', weight: 1, maxConcurrent: 1 },
      ]),
  );
  assert.throws(() => new TenantScheduler([], 0));
});

test('DRR dispatch order follows tenant weights (2:1 => AAB pattern)', async () => {
  const s = new TenantScheduler([
    { id: 'A', weight: 2, maxConcurrent: 100 },
    { id: 'B', weight: 1, maxConcurrent: 100 },
  ]);
  const order: string[] = [];
  const mk = (id: string) => async () => {
    order.push(id);
  };
  // Interleave submissions so both tenants are backlogged before pumping.
  const promises: Promise<void>[] = [];
  for (let i = 0; i < 6; i++) {
    promises.push(s.submit('A', mk('A')));
    promises.push(s.submit('B', mk('B')));
  }
  await Promise.all(promises);
  // Each DRR round: A gets 2 credits, B gets 1. A drains in 3 rounds
  // (A,A,B)x3, then B finishes its remaining 3 tasks alone.
  assert.deepEqual(
    order,
    ['A', 'A', 'B', 'A', 'A', 'B', 'A', 'A', 'B', 'B', 'B', 'B'],
  );
  // Fair share: within the first 9 dispatches the ratio is exactly 2:1.
  const first9 = order.slice(0, 9);
  assert.equal(first9.filter((x) => x === 'A').length, 6);
  assert.equal(first9.filter((x) => x === 'B').length, 3);
});

test('equal weights interleave fairly (ABAB)', async () => {
  const s = new TenantScheduler([
    { id: 'A', weight: 1, maxConcurrent: 100 },
    { id: 'B', weight: 1, maxConcurrent: 100 },
  ]);
  const order: string[] = [];
  const promises: Promise<void>[] = [];
  for (let i = 0; i < 4; i++) {
    promises.push(s.submit('A', async () => void order.push('A')));
    promises.push(s.submit('B', async () => void order.push('B')));
  }
  await Promise.all(promises);
  assert.deepEqual(order, ['A', 'B', 'A', 'B', 'A', 'B', 'A', 'B']);
});

test('per-tenant concurrency cap is never exceeded under load', async () => {
  const s = new TenantScheduler([
    { id: 'small', weight: 1, maxConcurrent: 2 },
    { id: 'big', weight: 1, maxConcurrent: 10 },
  ]);
  const running = { small: 0, big: 0 };
  const maxSeen = { small: 0, big: 0 };
  const mk = (id: 'small' | 'big') => async () => {
    running[id] += 1;
    maxSeen[id] = Math.max(maxSeen[id], running[id]);
    await delay(5);
    running[id] -= 1;
  };
  const promises: Promise<void>[] = [];
  for (let i = 0; i < 20; i++) {
    promises.push(s.submit('small', mk('small')));
    promises.push(s.submit('big', mk('big')));
  }
  await Promise.all(promises);
  assert.ok(maxSeen.small <= 2, `small tenant peaked at ${maxSeen.small}`);
  assert.ok(maxSeen.big <= 10, `big tenant peaked at ${maxSeen.big}`);
  assert.ok(maxSeen.small >= 1 && maxSeen.big >= 1, 'both tenants actually ran');
  assert.deepEqual(running, { small: 0, big: 0 }, 'no leaked permits');
});

test('tenant isolation: a slow tenant does not block a fast tenant', async () => {
  const s = new TenantScheduler([
    { id: 'slow', weight: 1, maxConcurrent: 1 },
    { id: 'fast', weight: 1, maxConcurrent: 1 },
  ]);
  const gate = deferred();
  const slowDone = s.submit('slow', () => gate.promise);
  const fastDone = s.submit('fast', async () => 'fast-result');
  // Fast tenant completes even though slow tenant's task never resolved yet.
  assert.equal(await fastDone, 'fast-result');
  gate.resolve();
  await slowDone;
});

test('queue quota rejects overflow with QuotaExceededError, others unaffected', async () => {
  const s = new TenantScheduler([
    { id: 'capped', weight: 1, maxConcurrent: 1, maxQueue: 2 },
    { id: 'other', weight: 1, maxConcurrent: 1 },
  ]);
  const gate = deferred();
  const first = s.submit('capped', () => gate.promise); // occupies the slot
  await delay(0); // let the pump dispatch `first` into the running slot
  const q1 = s.submit('capped', () => gate.promise); // queued
  const q2 = s.submit('capped', () => gate.promise); // queued
  await assert.rejects(() => s.submit('capped', async () => 0), (e: unknown) => {
    return e instanceof QuotaExceededError && e.tenantId === 'capped' && e.limit === 2;
  });
  // Quota of one tenant never limits another tenant.
  const otherResult = await s.submit('other', async () => 'ok');
  assert.equal(otherResult, 'ok');
  gate.resolve();
  await Promise.all([first, q1, q2]);
  assert.deepEqual(s.stats('capped'), { queued: 0, running: 0, deficit: 0 });
});

test('task failures propagate and never leak permits', async () => {
  const s = new TenantScheduler([{ id: 'a', weight: 1, maxConcurrent: 1 }]);
  const boom = new Error('boom');
  await assert.rejects(
    () => s.submit('a', async () => { throw boom; }),
    (e: unknown) => e === boom,
  );
  // The failed task's permit was released: the next task runs immediately.
  assert.equal(await s.submit('a', async () => 'recovered'), 'recovered');
  assert.deepEqual(s.stats('a'), { queued: 0, running: 0, deficit: 0 });
});

test('deficit resets to zero when a tenant drains its queue', async () => {
  const s = new TenantScheduler([
    { id: 'a', weight: 3, maxConcurrent: 100 },
    { id: 'b', weight: 1, maxConcurrent: 100 },
  ]);
  // A has fewer tasks than its weight*quantum credit; leftover deficit
  // must not be banked for a later round.
  await s.submit('a', async () => {});
  assert.equal(s.stats('a').deficit, 0);
  await Promise.all([s.submit('b', async () => {}), s.submit('b', async () => {})]);
  assert.equal(s.stats('b').deficit, 0);
});

test('stress: 8 tenants x 200 tasks, all settle, zero leakage', async () => {
  const ids = Array.from({ length: 8 }, (_, i) => `t${i}`);
  const s = new TenantScheduler(
    ids.map((id, i) => ({
      id,
      weight: (i % 3) + 1,
      maxConcurrent: (i % 4) + 1,
    })),
  );
  const counts = new Map(ids.map((id) => [id, 0]));
  const promises: Promise<unknown>[] = [];
  for (let i = 0; i < 200; i++) {
    for (const id of ids) {
      promises.push(
        s.submit(id, async () => {
          await delay(Math.random() < 0.2 ? 1 : 0);
          counts.set(id, counts.get(id)! + 1);
          if (Math.random() < 0.05) throw new Error('transient');
        }).catch((e: Error) => e),
      );
    }
  }
  const results = await Promise.all(promises);
  const failures = results.filter((r) => r instanceof Error);
  assert.ok(failures.length > 0, 'some tasks failed as injected');
  assert.ok(failures.length < results.length, 'not all tasks failed');
  for (const id of ids) {
    const st = s.stats(id);
    assert.equal(st.queued, 0, `${id}: queue drained`);
    assert.equal(st.running, 0, `${id}: no leaked permits`);
    assert.equal(st.deficit, 0, `${id}: deficit settled`);
  }
  const total = [...counts.values()].reduce((a, b) => a + b, 0);
  assert.equal(total, 1600, 'every submitted task executed exactly once');
});

test('weighted throughput: heavier tenant finishes proportionally more early', async () => {
  const s = new TenantScheduler([
    { id: 'heavy', weight: 3, maxConcurrent: 100 },
    { id: 'light', weight: 1, maxConcurrent: 100 },
  ]);
  const completionOrder: string[] = [];
  const promises: Promise<void>[] = [];
  for (let i = 0; i < 12; i++) {
    promises.push(s.submit('heavy', async () => void completionOrder.push('heavy')));
    promises.push(s.submit('light', async () => void completionOrder.push('light')));
  }
  await Promise.all(promises);
  const first8 = completionOrder.slice(0, 8);
  const heavyInFirst8 = first8.filter((x) => x === 'heavy').length;
  // With 3:1 weights, at most 2 of the first 8 completions may be light.
  assert.ok(heavyInFirst8 >= 6, `expected heavy dominance, got ${heavyInFirst8}/8`);
});
