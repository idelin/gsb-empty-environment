import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Executor,
  MemoryFileStore,
  MemoryJournal,
  PublishQueue,
  type PublishOutcome,
} from '../src/index.ts';

const tick = (): Promise<void> => new Promise((resolve) => setImmediate(resolve));

async function waitFor(cond: () => boolean, tries = 200): Promise<void> {
  for (let i = 0; i < tries; i += 1) {
    if (cond()) return;
    await tick();
  }
  throw new Error('condition not met');
}

test('same path: serialized, second writer with stale version conflicts', async () => {
  const store = new MemoryFileStore();
  store.seed('/a.txt', { version: 0, digest: 'v0' });
  const queue = new PublishQueue({ executor: new Executor(store), store });

  const first = queue.publish({ path: '/a.txt', expectedVersion: 0, digest: 'v1' });
  const second = queue.publish({ path: '/a.txt', expectedVersion: 0, digest: 'vX' });
  const third = queue.publish({ path: '/a.txt', expectedVersion: 1, digest: 'v2' });

  const o1 = await first.done;
  const o2 = await second.done;
  const o3 = await third.done;

  assert.equal(o1.kind, 'committed');
  assert.equal(o1.kind === 'committed' && o1.receipt.version, 1);
  assert.deepEqual(o2, { kind: 'conflict', currentVersion: 1 });
  assert.equal(o3.kind, 'committed');
  assert.equal(o3.kind === 'committed' && o3.receipt.version, 2);
  assert.equal(store.commitCount, 2);
  assert.deepEqual(await store.read('/a.txt'), { version: 2, digest: 'v2' });
});

test('cross path: independent progress, slow path does not block fast path', async () => {
  const store = new MemoryFileStore();
  store.seed('/slow', { version: 0, digest: 's0' });
  store.seed('/fast', { version: 0, digest: 'f0' });
  const executor = new Executor(store, {
    delays: { beforeCommit: (req) => (req.path === '/slow' ? 80 : 0) },
  });
  const queue = new PublishQueue({ executor, store });

  const order: string[] = [];
  const slow = queue.publish({ path: '/slow', expectedVersion: 0, digest: 's1' });
  const fast = queue.publish({ path: '/fast', expectedVersion: 0, digest: 'f1' });
  void slow.done.then(() => order.push('slow'));
  void fast.done.then(() => order.push('fast'));

  const [oSlow, oFast] = await Promise.all([slow.done, fast.done]);
  assert.equal(oSlow.kind, 'committed');
  assert.equal(oFast.kind, 'committed');
  assert.deepEqual(order, ['fast', 'slow']);
});

test('cancel while queued behind another task on the same path', async () => {
  const store = new MemoryFileStore();
  store.seed('/a', { version: 0, digest: 'v0' });
  const executor = new Executor(store, { delays: { beforeCommit: 50 } });
  const queue = new PublishQueue({ executor, store });

  const blocker = queue.publish({ path: '/a', expectedVersion: 0, digest: 'v1' });
  const queued = queue.publish({ path: '/a', expectedVersion: 1, digest: 'v2' });

  const res = queued.cancel();
  assert.deepEqual(res, { ok: true, status: 'cancelled' });
  assert.deepEqual(await queued.done, { kind: 'cancelled' });
  assert.equal((await blocker.done).kind, 'committed');
  assert.deepEqual(await store.read('/a'), { version: 1, digest: 'v1' });
  assert.equal(store.commitCount, 1);
});

test('cancel during in-flight check phase', async () => {
  const store = new MemoryFileStore();
  store.seed('/a', { version: 0, digest: 'v0' });
  const executor = new Executor(store, { delays: { check: 50 } });
  const queue = new PublishQueue({ executor, store });

  const handle = queue.publish({ path: '/a', expectedVersion: 0, digest: 'v1' });
  await tick();
  const res = handle.cancel();
  assert.deepEqual(res, { ok: true, status: 'cancelled' });
  assert.deepEqual(await handle.done, { kind: 'cancelled' });
  assert.deepEqual(await store.read('/a'), { version: 0, digest: 'v0' });
  assert.equal(store.commitCount, 0);
});

test('cancel after the confirmation point reports committed, never revoked', async () => {
  const store = new MemoryFileStore();
  store.seed('/a', { version: 0, digest: 'v0' });
  const executor = new Executor(store, { delays: { receipt: 60 } });
  const queue = new PublishQueue({ executor, store });

  const handle = queue.publish({ path: '/a', expectedVersion: 0, digest: 'v1' });
  await waitFor(() => store.commitCount === 1); // commit applied, receipt still in flight

  const during = handle.cancel();
  assert.equal(during.ok, false);
  assert.equal(during.status, 'committing');

  const outcome = await handle.done;
  assert.equal(outcome.kind, 'committed');

  const after = handle.cancel();
  assert.equal(after.ok, false);
  assert.equal(after.status, 'committed');
  if (after.status === 'committed') {
    assert.equal(after.receipt.version, 1);
    assert.equal(after.receipt.digest, 'v1');
  }
  assert.deepEqual(await store.read('/a'), { version: 1, digest: 'v1' });
});

test('lost confirmation receipt is reconciled to committed', async () => {
  const store = new MemoryFileStore();
  store.seed('/a', { version: 0, digest: 'v0' });
  const executor = new Executor(store, { faults: { receipt: () => true } });
  const queue = new PublishQueue({ executor, store });

  const handle = queue.publish({
    path: '/a',
    expectedVersion: 0,
    digest: 'v1',
    idempotencyKey: 'k-1',
  });
  const outcome = await handle.done;
  assert.equal(outcome.kind, 'committed');
  if (outcome.kind === 'committed') {
    assert.equal(outcome.receipt.version, 1);
    assert.equal(outcome.receipt.digest, 'v1');
  }
  assert.deepEqual(await store.read('/a'), { version: 1, digest: 'v1' });
  assert.equal(store.commitCount, 1);
});

test('recovery: journaled result is replayed for resubmitted idempotency key', async () => {
  const store = new MemoryFileStore();
  store.seed('/a', { version: 0, digest: 'v0' });
  const journal = new MemoryJournal();
  const executor = new Executor(store, { faults: { receipt: () => true } });

  const queue1 = new PublishQueue({ executor, store, journal });
  const first = await queue1.publish({
    path: '/a',
    expectedVersion: 0,
    digest: 'v1',
    idempotencyKey: 'k-1',
  }).done;
  assert.equal(first.kind, 'committed');
  assert.equal(store.commitCount, 1);

  // Simulate a restart: brand-new queue over the same store and journal.
  const queue2 = new PublishQueue({ executor: new Executor(store), store, journal });
  await queue2.recover();
  const replayed = await queue2.publish({
    path: '/a',
    expectedVersion: 0,
    digest: 'v1',
    idempotencyKey: 'k-1',
  }).done;

  assert.equal(replayed.kind, 'committed');
  if (replayed.kind === 'committed' && first.kind === 'committed') {
    assert.deepEqual(replayed.receipt, first.receipt);
  }
  assert.equal(store.commitCount, 1); // not re-executed
});

test('recovery: commit without journal entry replays idempotently from store state', async () => {
  const store = new MemoryFileStore();
  // Crash window: CAS landed but the journal write never happened.
  store.seed('/a', { version: 1, digest: 'v1' });
  const queue = new PublishQueue({ executor: new Executor(store), store });

  const outcome = await queue.publish({
    path: '/a',
    expectedVersion: 0,
    digest: 'v1',
    idempotencyKey: 'k-1',
  }).done;

  assert.equal(outcome.kind, 'committed');
  if (outcome.kind === 'committed') {
    assert.equal(outcome.receipt.version, 1);
    assert.equal(outcome.receipt.digest, 'v1');
  }
  assert.equal(store.commitCount, 0); // recognized, not re-applied
});

test('injected failure rejects the task but the path queue keeps flowing', async () => {
  const store = new MemoryFileStore();
  store.seed('/a', { version: 0, digest: 'v0' });
  let failOnce = true;
  const executor = new Executor(store, {
    faults: { beforeCommit: () => failOnce && !(failOnce = false) },
  });
  const queue = new PublishQueue({ executor, store });

  const failed: PublishOutcome = await queue
    .publish({ path: '/a', expectedVersion: 0, digest: 'v1' })
    .done;
  assert.equal(failed.kind, 'failed');

  const retried = await queue.publish({ path: '/a', expectedVersion: 0, digest: 'v1' }).done;
  assert.equal(retried.kind, 'committed');
  assert.deepEqual(await store.read('/a'), { version: 1, digest: 'v1' });
});
