import assert from 'node:assert/strict';
import test from 'node:test';
import { Barrier, delay } from '../src/barrier.ts';
import { Container, CycleError, UnknownServiceError } from '../src/container.ts';

test('barrier releases all parties together', async () => {
  const barrier = new Barrier(3);
  const order: string[] = [];
  const party = async (id: string) => {
    order.push(`arrive:${id}`);
    await barrier.wait();
    order.push(`leave:${id}`);
  };
  await Promise.all([party('a'), party('b'), party('c')]);
  const arrivals = order.filter((entry) => entry.startsWith('arrive:'));
  const leavings = order.filter((entry) => entry.startsWith('leave:'));
  assert.equal(arrivals.length, 3);
  assert.equal(leavings.length, 3);
  assert.ok(order.indexOf('leave:' + 'a') >= 3, 'no one leaves before all arrive');
});

test('independent concurrent requests share one construction', async () => {
  const container = new Container();
  let builds = 0;
  container.register<number>('counter', {
    factory: async () => {
      builds += 1;
      await delay(10);
      return builds;
    },
  });

  const [first, second, third] = await Promise.all([
    container.get<number>('counter'),
    container.get<number>('counter'),
    container.get<number>('counter'),
  ]);

  assert.equal(builds, 1);
  assert.equal(first, 1);
  assert.equal(second, first);
  assert.equal(third, first);
});

test('nested concurrent requests share the dependency construction', async () => {
  const container = new Container();
  let dbBuilds = 0;
  container.register<string>('db', {
    factory: async () => {
      dbBuilds += 1;
      await delay(10);
      return 'db-conn';
    },
  });
  container.register<string>('userRepo', {
    factory: async (r) => `userRepo(${await r.get<string>('db')})`,
  });
  container.register<string>('orderRepo', {
    factory: async (r) => `orderRepo(${await r.get<string>('db')})`,
  });

  const [users, orders] = await Promise.all([
    container.get<string>('userRepo'),
    container.get<string>('orderRepo'),
  ]);

  assert.equal(users, 'userRepo(db-conn)');
  assert.equal(orders, 'orderRepo(db-conn)');
  assert.equal(dbBuilds, 1);
});

test('single request chain reports the closed loop', async () => {
  const container = new Container();
  container.register('a', { factory: async (r) => ({ b: await r.get('b') }) });
  container.register('b', { factory: async (r) => ({ c: await r.get('c') }) });
  container.register('c', { factory: async (r) => ({ a: await r.get('a') }) });

  await assert.rejects(container.get('a'), (error: unknown) => {
    assert.ok(error instanceof CycleError);
    assert.equal(error.message, 'circular dependency detected: a -> b -> c -> a');
    assert.deepEqual(error.chain, ['a', 'b', 'c', 'a']);
    return true;
  });
});

test('concurrent cycle across request chains is detected via barriers', async () => {
  const container = new Container();
  const barrier = new Barrier(2);
  let aAttempts = 0;
  let bAttempts = 0;

  container.register<string>('a', {
    factory: async (r) => {
      aAttempts += 1;
      if (aAttempts === 1) {
        await barrier.wait();
        await r.get('b');
      }
      return 'A';
    },
  });
  container.register<string>('b', {
    factory: async (r) => {
      bAttempts += 1;
      if (bAttempts === 1) {
        await barrier.wait();
        await r.get('a');
      }
      return 'B';
    },
  });

  const [resultA, resultB] = await Promise.allSettled([
    container.get<string>('a'),
    container.get<string>('b'),
  ]);

  assert.equal(resultA.status, 'rejected');
  assert.equal(resultB.status, 'rejected');
  const reasons = [resultA, resultB].map((result) =>
    result.status === 'rejected' ? result.reason : undefined,
  );
  assert.ok(
    reasons.every((reason) => reason instanceof CycleError),
    `both requests fail with CycleError, got: ${reasons}`,
  );
  const loops = reasons.map((reason) => (reason as CycleError).chain);
  assert.ok(
    loops.some(
      (chain) =>
        chain.join(' -> ') === 'a -> b -> a' || chain.join(' -> ') === 'b -> a -> b',
    ),
    `closed loop reported, got: ${loops.map((chain) => chain.join(' -> '))}`,
  );

  assert.equal(container.isReady('a'), false);
  assert.equal(container.isReady('b'), false);

  assert.equal(await container.get<string>('a'), 'A');
  assert.equal(await container.get<string>('b'), 'B');
  assert.equal(aAttempts, 2);
  assert.equal(bAttempts, 2);
});

test('failed construction rejects all waiters and allows a new generation', async () => {
  const container = new Container();
  let attempts = 0;
  container.register<string>('flaky', {
    factory: async () => {
      attempts += 1;
      await delay(5);
      if (attempts === 1) {
        throw new Error('boom');
      }
      return `ok-${attempts}`;
    },
  });

  const [first, second] = await Promise.allSettled([
    container.get<string>('flaky'),
    container.get<string>('flaky'),
  ]);
  assert.equal(first.status, 'rejected');
  assert.equal(second.status, 'rejected');
  assert.equal(attempts, 1, 'concurrent waiters share the failed generation');

  const value = await container.get<string>('flaky');
  assert.equal(value, 'ok-2');
  assert.equal(attempts, 2, 'retry starts a new generation');

  assert.equal(await container.get<string>('flaky'), 'ok-2');
  assert.equal(attempts, 2, 'ready instance is cached');
});

test('dispose releases instances in reverse dependency order exactly once', async () => {
  const container = new Container();
  const disposed: string[] = [];
  const track = (name: string) => () => {
    disposed.push(name);
  };

  container.register<string>('c', { factory: async () => 'C', dispose: track('c') });
  container.register<string>('b', {
    factory: async (r) => {
      await r.get('c');
      return 'B';
    },
    dispose: track('b'),
  });
  container.register<string>('a', {
    factory: async (r) => {
      await r.get('b');
      return 'A';
    },
    dispose: track('a'),
  });

  await container.get('a');
  await container.dispose();
  assert.deepEqual(disposed, ['a', 'b', 'c']);

  await container.dispose();
  assert.deepEqual(disposed, ['a', 'b', 'c'], 'second dispose is a no-op');
});

test('failed generations are never disposed', async () => {
  const container = new Container();
  const disposed: string[] = [];
  let attempts = 0;

  container.register<string>('unstable', {
    factory: async () => {
      attempts += 1;
      if (attempts < 3) {
        throw new Error(`failure ${attempts}`);
      }
      return 'stable';
    },
    dispose: () => {
      disposed.push('unstable');
    },
  });

  await assert.rejects(container.get('unstable'), /failure 1/);
  await assert.rejects(container.get('unstable'), /failure 2/);
  assert.equal(await container.get<string>('unstable'), 'stable');

  await container.dispose();
  assert.deepEqual(disposed, ['unstable'], 'only the successful instance is disposed');
});

test('unknown services are rejected', async () => {
  const container = new Container();
  await assert.rejects(container.get('missing'), UnknownServiceError);
});
