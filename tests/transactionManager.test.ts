import assert from 'node:assert/strict';
import { test } from 'node:test';
import { DataStore } from '../src/store.ts';
import { TransactionManager } from '../src/transactionManager.ts';
import { DeadlockError } from '../src/types.ts';

test('commit releases all locks atomically; blocked tx then proceeds', async () => {
  const tm = new TransactionManager(new DataStore({ balance: 100 }));
  const t1 = tm.begin();
  await tm.write(t1, 'balance', 80);

  const t2 = tm.begin();
  let t2Done = false;
  const pending = tm.read(t2, 'balance').then((value) => {
    t2Done = true;
    return value;
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(t2Done, false, 't2 blocked by t1 X lock');
  assert.deepEqual([...tm.monitor.heldLocks(t1.id).keys()], ['balance']);

  tm.commit(t1);
  const seen = await pending;
  assert.equal(seen, 80, 't2 sees t1 committed value');
  assert.equal(tm.monitor.heldLocks(t1.id).size, 0);
  assert.ok(tm.monitor.verify(), 'no shrink-phase violations');
  tm.commit(t2);
});

test('abort rolls back modifications and releases every lock', async () => {
  const tm = new TransactionManager(new DataStore({ a: 1, b: 2 }));
  const tx = tm.begin();
  await tm.write(tx, 'a', 100);
  await tm.write(tx, 'b', 200);
  await tm.write(tx, 'c', 300); // brand-new key
  assert.equal(tm.store.get('a'), 100);

  tm.abort(tx);
  assert.equal(tm.store.get('a'), 1);
  assert.equal(tm.store.get('b'), 2);
  assert.equal(tm.store.get('c'), undefined, 'new key removed by undo');
  assert.equal(tm.locks.heldBy(tx.id).size, 0);
  assert.ok(tm.monitor.verify());
});

test('concurrent contenders on multiple resources: serializable by commit order', async () => {
  const tm = new TransactionManager(new DataStore({ x: 0, y: 0, z: 0 }));
  const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

  // 6 transactions, each touches a random subset of shared resources.
  // Deadlock victims are aborted and retried, as a real engine would do.
  const workers = Array.from({ length: 6 }, () => (async () => {
    for (;;) {
      const tx = tm.begin();
      try {
        const keys = ['x', 'y', 'z'].filter(() => Math.random() < 0.8);
        for (const key of keys.sort()) {
          const current = (await tm.read(tx, key)) ?? 0;
          await sleep(Math.random() * 5);
          await tm.write(tx, key, current + 1);
        }
        tm.commit(tx);
        return tx.id;
      } catch (error) {
        if (!(error instanceof DeadlockError)) throw error;
        tm.abort(tx); // victim: rollback + atomic release, then retry
      }
    }
  })());

  await Promise.all(workers);
  assert.ok(tm.monitor.verify(), `violations: ${tm.monitor.violations}`);
  const { ok, conflicts } = tm.history.verifyCommitOrderEquivalence();
  assert.ok(ok, `commit-order equivalence broken: ${conflicts}`);
  // Every committed increment is preserved: no lost updates.
  const committedTxs = new Set(
    tm.history.ops.filter((op) => op.kind === 'commit').map((op) => (op as { txId: number }).txId),
  );
  const committedWrites = tm.history.ops.filter(
    (op) => op.kind === 'write' && committedTxs.has(op.txId),
  );
  for (const key of ['x', 'y', 'z']) {
    const count = committedWrites.filter((op) => op.kind === 'write' && op.key === key).length;
    assert.equal(tm.store.get(key), count, `lost update on ${key}`);
  }
});

test('schedule equals commit-timestamp order for a classic 2-tx interleaving', async () => {
  const tm = new TransactionManager(new DataStore({ p: 10, q: 20 }));
  const t1 = tm.begin();
  const t2 = tm.begin();

  await tm.write(t1, 'p', 11);          // t1 locks p
  const t2Read = tm.read(t2, 'p');      // t2 blocks on p
  await new Promise((resolve) => setImmediate(resolve));
  await tm.write(t1, 'q', 21);          // t1 keeps growing (still holds p!)
  assert.deepEqual(
    [...tm.monitor.heldLocks(t1.id).keys()].sort(),
    ['p', 'q'],
    't1 must still hold p while acquiring q',
  );
  tm.commit(t1);                        // shrinking point: all locks at once
  assert.equal(await t2Read, 11);       // t2 unblocks, reads committed value
  await tm.write(t2, 'q', 22);
  tm.commit(t2);

  assert.ok(tm.monitor.verify());
  const { ok } = tm.history.verifyCommitOrderEquivalence();
  assert.ok(ok);
  assert.ok(tm.history.ops.find((op) => op.kind === 'commit' && op.txId === t1.id)!);
});
