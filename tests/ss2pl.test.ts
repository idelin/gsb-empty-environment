import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DeadlockError } from '../src/errors.ts';
import { LockMonitor } from '../src/lock-monitor.ts';
import { ShrinkingPhaseManager } from '../src/shrinking-phase-manager.ts';
import { DataStore } from '../src/store.ts';
import { TransactionManager } from '../src/transaction-manager.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

const newManager = () => {
  const tm = new TransactionManager();
  const monitor = new LockMonitor(tm.lockManager, (id) => tm.get(id)?.phase);
  return { tm, monitor };
};

test('growing phase: no lock is released before commit', async () => {
  const { tm, monitor } = newManager();
  const txn = tm.begin();
  await txn.write('x', 1);
  await txn.write('y', 2);
  await txn.read('x');
  // Still growing: every acquired lock must still be held.
  assert.deepEqual([...tm.lockManager.heldResources(txn.id).keys()].sort(), ['x', 'y']);
  assert.equal(txn.phase, 'growing');
  txn.commit();
  // Shrinking phase: everything is dropped at once.
  assert.equal(tm.lockManager.heldResources(txn.id).size, 0);
  monitor.assertNoEarlyRelease();
});

test('shrinking-phase manager rejects any release while the transaction is growing', async () => {
  const { tm } = newManager();
  const txn = tm.begin();
  await txn.write('x', 1);
  const gatekeeper = new ShrinkingPhaseManager(tm.lockManager, () => 'growing');
  assert.throws(() => gatekeeper.enterShrinkingPhase(txn.id), /forbids releasing any lock/);
  // The failed attempt must not have released anything.
  assert.deepEqual([...tm.lockManager.heldResources(txn.id).keys()], ['x']);
  txn.commit();
});

test('strictness: uncommitted writes stay invisible until commit', async () => {
  const { tm, monitor } = newManager();
  tm.store.write('balance', 100);
  const t1 = tm.begin();
  await t1.write('balance', 50);
  const t2 = tm.begin();
  let observed: number | undefined;
  let resolved = false;
  const pending = t2.read('balance').then((value) => {
    observed = value;
    resolved = true;
  });
  await sleep(30);
  assert.equal(resolved, false, 'read must block behind the uncommitted write');
  t1.commit();
  await pending;
  assert.equal(observed, 50, 'reader sees the committed value');
  t2.commit();
  monitor.assertNoEarlyRelease();
});

test('abort rolls back writes and releases all locks at once', async () => {
  const { tm, monitor } = newManager();
  tm.store.write('k', 1);
  const t1 = tm.begin();
  await t1.write('k', 999);
  const t2 = tm.begin();
  const pending = t2.read('k');
  await sleep(20);
  t1.abort();
  assert.equal(await pending, 1, 'reader observes the rolled-back value');
  assert.equal(tm.store.read('k'), 1);
  assert.equal(tm.lockManager.heldResources(t1.id).size, 0);
  t2.commit();
  monitor.assertNoEarlyRelease();
});

test('concurrent schedule is equivalent to commit-timestamp order', async () => {
  const { tm, monitor } = newManager();
  const keys = ['a', 'b', 'c', 'd'];
  for (const key of keys) tm.store.write(key, 0);
  const seed = tm.store.snapshot();

  const txns = Array.from({ length: 8 }, () => tm.begin());
  await Promise.all(
    txns.map(async (txn, i) => {
      try {
        const k1 = keys[i % keys.length];
        const k2 = keys[(i + 3) % keys.length];
        const v1 = (await txn.read(k1)) ?? 0;
        await sleep((i * 7) % 13);
        await txn.write(k1, v1 + 1);
        const v2 = (await txn.read(k2)) ?? 0;
        await sleep((i * 5) % 11);
        await txn.write(k2, v2 + 10);
        txn.commit();
      } catch (err) {
        // A deadlock victim is acceptable concurrency control behaviour:
        // it is rolled back and simply does not appear in the commit order.
        if (!(err instanceof DeadlockError)) throw err;
      }
    }),
  );

  monitor.assertNoEarlyRelease();
  assert.ok(tm.commitOrder().length > 0, 'at least one transaction committed');

  // Commit timestamps form a strict total order.
  const stamps = tm.commitOrder().map((id) => tm.get(id)!.commitTimestamp!);
  for (let i = 1; i < stamps.length; i++) assert.ok(stamps[i] > stamps[i - 1]);

  // Replaying the committed transactions' writes in commit-timestamp order
  // from the seed must reproduce the actual final database state.
  const replay = new DataStore();
  for (const [key, value] of Object.entries(seed)) replay.write(key, value);
  for (const id of tm.commitOrder()) {
    for (const [key, value] of tm.get(id)!.writes()) replay.write(key, value);
  }
  assert.deepEqual(tm.store.snapshot(), replay.snapshot());
});
