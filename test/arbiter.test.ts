import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Arbiter, AsyncMutex, KeyedLockManager } from '../src/arbiter.ts';
import { checkRealTimeConsistency, isLinearizable } from './lin.ts';

const tick = () => new Promise((resolve) => setTimeout(resolve, Math.random() * 3));

test('AsyncMutex grants access in FIFO order', async () => {
  const mutex = new AsyncMutex();
  const order: string[] = [];
  const first = await mutex.acquire();
  const waiters = ['a', 'b', 'c'].map((id) =>
    mutex.acquire().then((release) => {
      order.push(id);
      release();
    }),
  );
  assert.equal(mutex.waiters, 3);
  first();
  await Promise.all(waiters);
  assert.deepEqual(order, ['a', 'b', 'c']);
  assert.equal(mutex.isLocked, false);
  assert.equal(mutex.waiters, 0);
});

test('AsyncMutex serializes critical sections under contention', async () => {
  const mutex = new AsyncMutex();
  let active = 0;
  let maxActive = 0;
  const workers = Array.from({ length: 20 }, () =>
    mutex.withLock(async () => {
      active++;
      maxActive = Math.max(maxActive, active);
      await tick();
      active--;
    }),
  );
  await Promise.all(workers);
  assert.equal(maxActive, 1);
  assert.equal(mutex.isLocked, false);
  assert.equal(mutex.waiters, 0);
});

test('AsyncMutex rejects double release', async () => {
  const mutex = new AsyncMutex();
  const release = await mutex.acquire();
  release();
  assert.throws(() => release(), /twice/);
});

test('KeyedLockManager: overlapping lock sets in opposite order never deadlock', async () => {
  const manager = new KeyedLockManager();
  const keys = ['k1', 'k2', 'k3', 'k4'];
  const activePerKey = new Map<string, number>(keys.map((k) => [k, 0]));
  let violation = false;
  const worker = async (ids: string[]) => {
    for (let i = 0; i < 25; i++) {
      const picked = ids.filter(() => Math.random() < 0.75);
      await manager.withLocks(picked, async () => {
        for (const k of picked) {
          activePerKey.set(k, (activePerKey.get(k) as number) + 1);
          if ((activePerKey.get(k) as number) > 1) violation = true;
        }
        await tick();
        for (const k of picked) {
          activePerKey.set(k, (activePerKey.get(k) as number) - 1);
        }
      });
    }
  };
  const forward = Array.from({ length: 4 }, (_, i) => worker([...keys].slice(i % 2 === 0 ? 0 : 1)));
  const backward = Array.from({ length: 4 }, () => worker([...keys].reverse()));
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error('deadlock detected')), 20000);
  });
  try {
    await Promise.race([Promise.all([...forward, ...backward]), timeout]);
  } finally {
    clearTimeout(timer);
  }
  assert.equal(violation, false, 'no key may be held by two critical sections at once');
  assert.equal(manager.heldCount, 0);
  assert.equal(manager.waiterCount, 0);
});

test('KeyedLockManager releases all locks when critical section throws', async () => {
  const manager = new KeyedLockManager();
  await assert.rejects(
    manager.withLocks(['a', 'b'], () => {
      throw new Error('boom');
    }),
    /boom/,
  );
  assert.equal(manager.heldCount, 0);
  assert.equal(manager.waiterCount, 0);
});

test('Arbiter: concurrent increments from many nodes lose no update', async () => {
  const arbiter = new Arbiter();
  const nodes = 16;
  const incrementsPerNode = 25;
  const workers = Array.from({ length: nodes }, (_, i) => {
    const nodeId = `node-${i}`;
    return (async () => {
      for (let n = 0; n < incrementsPerNode; n++) {
        await arbiter.update(nodeId, 'counter', (v) => ((v as number | undefined) ?? 0) + 1);
      }
    })();
  });
  const results = await Promise.allSettled(workers);
  assert.ok(results.every((r) => r.status === 'fulfilled'));
  assert.equal(arbiter.valueOf('counter'), nodes * incrementsPerNode);
  assert.equal(arbiter.versionOf('counter'), BigInt(nodes * incrementsPerNode));
  assert.equal(arbiter.pendingCount, 0);
  checkRealTimeConsistency(arbiter.history);
  assert.ok(arbiter.history.every((e) => e.respondedAt > e.invokedAt));
});

test('Arbiter: CAS conflict arbitration is deterministic', async () => {
  const arbiter = new Arbiter();
  await arbiter.execute('seed', { kind: 'write', key: 'doc', value: 'v1' });
  const stale = arbiter.versionOf('doc');
  const [a, b] = await Promise.all([
    arbiter.execute('node-a', { kind: 'cas', key: 'doc', expectedVersion: stale, value: 'a' }),
    arbiter.execute('node-b', { kind: 'cas', key: 'doc', expectedVersion: stale, value: 'b' }),
  ]);
  assert.notEqual(a.ok, b.ok, 'exactly one contender wins');
  const loser = a.ok ? b : a;
  assert.equal(loser.conflict, true);
  assert.equal(loser.version, 2n);
  assert.equal(arbiter.versionOf('doc'), 2n);
  assert.equal(arbiter.pendingCount, 0);
});

test('Arbiter: multi-key transactions are atomic and preserve invariant', async () => {
  const arbiter = new Arbiter();
  const accounts = ['alice', 'bob', 'carol', 'dave'];
  for (const account of accounts) {
    await arbiter.execute('seed', { kind: 'write', key: account, value: 100 });
  }
  const totalBefore = accounts.reduce((sum, a) => sum + (arbiter.valueOf(a) as number), 0);

  const transfer = async (nodeId: string, from: string, to: string, amount: number) => {
    await arbiter.transactFn(nodeId, (tx) => {
      tx.set(from, (tx.get(from).value as number) - amount);
      tx.set(to, (tx.get(to).value as number) + amount);
    });
  };

  const workers = Array.from({ length: 12 }, (_, i) =>
    (async () => {
      for (let n = 0; n < 10; n++) {
        const from = accounts[Math.floor(Math.random() * accounts.length)];
        let to = accounts[Math.floor(Math.random() * accounts.length)];
        if (to === from) to = accounts[(accounts.indexOf(to) + 1) % accounts.length];
        await transfer(`node-${i}`, from, to, 1);
      }
    })(),
  );
  const results = await Promise.allSettled(workers);
  assert.ok(results.every((r) => r.status === 'fulfilled'));
  const totalAfter = accounts.reduce((sum, a) => sum + (arbiter.valueOf(a) as number), 0);
  assert.equal(totalAfter, totalBefore);
  assert.equal(arbiter.pendingCount, 0);
  checkRealTimeConsistency(arbiter.history);
});

test('Arbiter: aborted transaction leaves state untouched', async () => {
  const arb = new Arbiter();
  await arb.execute('seed', { kind: 'write', key: 'a', value: 1 });
  await assert.rejects(
    arb.transactFn('node', (tx) => {
      tx.set('a', 100);
      tx.set('b', 200);
      throw new Error('tx failed midway');
    }),
    /midway/,
  );
  assert.equal(arb.valueOf('a'), 1);
  assert.equal(arb.valueOf('b'), undefined);
  assert.equal(arb.pendingCount, 0);
});

test('Arbiter: aborted transaction leaves state untouched (cas)', async () => {
  const arbiter = new Arbiter();
  await arbiter.execute('seed', { kind: 'write', key: 'x', value: 1 });
  await arbiter.execute('seed', { kind: 'write', key: 'y', value: 2 });
  const results = await arbiter.transact('node', [
    { kind: 'cas', key: 'x', expectedVersion: 1n, value: 10 },
    { kind: 'cas', key: 'y', expectedVersion: 99n, value: 20 },
  ]);
  assert.ok(results.every((r) => !r.ok && r.conflict));
  assert.equal(arbiter.valueOf('x'), 1);
  assert.equal(arbiter.valueOf('y'), 2);
  assert.equal(arbiter.versionOf('x'), 1n);
  assert.equal(arbiter.versionOf('y'), 1n);
});

test('Arbiter: mixed read/write/cas history is linearizable', async () => {
  // optimistic CAS-retry updates under moderate contention
  const optimistic = new Arbiter();
  await Promise.all(
    Array.from({ length: 4 }, (_, i) =>
      (async () => {
        for (let n = 0; n < 10; n++) {
          await optimistic.updateOptimistic(`opt-${i}`, 'c', (v) => ((v as number | undefined) ?? 0) + 1);
        }
      })(),
    ),
  );
  assert.equal(optimistic.valueOf('c'), 40);
  assert.equal(optimistic.versionOf('c'), 40n);
  assert.equal(optimistic.pendingCount, 0);

  const arbiter = new Arbiter();
  const keys = ['p', 'q'];
  const nodes = ['n1', 'n2', 'n3'];
  const workers = nodes.map((nodeId, i) =>
    (async () => {
      for (let n = 0; n < 3; n++) {
        const key = keys[(i + n) % keys.length];
        const read = await arbiter.execute(nodeId, { kind: 'read', key });
        await arbiter.execute(nodeId, {
          kind: 'cas',
          key,
          expectedVersion: read.version,
          value: `${nodeId}:${n}`,
        });
      }
    })(),
  );
  await Promise.all(workers);
  assert.ok(isLinearizable(arbiter.history), 'history must admit a linearization');
  checkRealTimeConsistency(arbiter.history);
  assert.equal(arbiter.pendingCount, 0);
});

test('Arbiter: no leaked state after full contention run', async () => {
  const arbiter = new Arbiter();
  const manager = new KeyedLockManager();
  const workers = Array.from({ length: 8 }, (_, i) =>
    (async () => {
      const nodeId = `leak-node-${i}`;
      for (let n = 0; n < 15; n++) {
        await manager.withLocks(['shared-a', 'shared-b'], async () => {
          await arbiter.update(nodeId, 'leak-counter', (v) => ((v as number | undefined) ?? 0) + 1);
        });
      }
    })(),
  );
  const results = await Promise.allSettled(workers);
  assert.ok(results.every((r) => r.status === 'fulfilled'));
  assert.equal(arbiter.valueOf('leak-counter'), 8 * 15);
  assert.equal(arbiter.pendingCount, 0);
  assert.equal(manager.heldCount, 0);
  assert.equal(manager.waiterCount, 0);
  assert.ok(arbiter.history.every((e) => Number.isFinite(e.invokedAt) && Number.isFinite(e.respondedAt)));
});
