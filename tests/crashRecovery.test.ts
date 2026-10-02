import assert from 'node:assert/strict';
import { test } from 'node:test';
import { DataStore } from '../src/store.ts';
import { TransactionManager } from '../src/transactionManager.ts';
import { TransactionStateError } from '../src/types.ts';

test('crash injection: locks survive the crash, recovery rolls back and releases', async () => {
  const tm = new TransactionManager(new DataStore({ acct1: 500, acct2: 300 }));
  const tx = tm.begin();
  await tm.write(tx, 'acct1', 400); // transfer 100 out
  await tm.write(tx, 'acct2', 400); // transfer 100 in
  assert.equal(tm.store.get('acct1'), 400, 'dirty write visible pre-crash');

  tm.crash(tx); // boom: no commit, no cleanup
  assert.equal(tm.stateOf(tx.id), 'CRASHED');
  assert.equal(tm.locks.heldBy(tx.id).size, 2, 'locks still held after crash');
  assert.equal(tm.store.get('acct1'), 400, 'dirty data still in store');

  // Another transaction must NOT be able to grab the crashed tx's rows.
  const t2 = tm.begin();
  let unblocked = false;
  const pending = tm.read(t2, 'acct1').then(() => { unblocked = true; });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(unblocked, false, 'crashed tx locks still protect rows');

  const recovered = tm.recover();
  assert.deepEqual(recovered, [tx.id]);
  assert.equal(tm.stateOf(tx.id), 'ABORTED');
  assert.equal(tm.locks.heldBy(tx.id).size, 0, 'recovery released all locks');
  assert.equal(tm.store.get('acct1'), 500, 'acct1 rolled back');
  assert.equal(tm.store.get('acct2'), 300, 'acct2 rolled back');

  await pending; // t2 now proceeds and sees the restored value
  assert.equal(unblocked, true);
  assert.equal(tm.store.get('acct1'), 500);
  tm.commit(t2);
  assert.ok(tm.monitor.verify(), `violations: ${tm.monitor.violations}`);
});

test('crashed transaction can no longer operate', async () => {
  const tm = new TransactionManager(new DataStore({ k: 1 }));
  const tx = tm.begin();
  await tm.write(tx, 'k', 2);
  tm.crash(tx);
  await assert.rejects(tm.write(tx, 'k', 3), TransactionStateError);
  assert.throws(() => tm.commit(tx), TransactionStateError);
  tm.recover();
  assert.equal(tm.store.get('k'), 1);
});

test('multiple simultaneous crashes are all recovered', async () => {
  const tm = new TransactionManager(new DataStore({ r1: 1, r2: 2, r3: 3 }));
  const t1 = tm.begin();
  const t2 = tm.begin();
  await tm.write(t1, 'r1', 10);
  await tm.write(t2, 'r2', 20);
  tm.crash(t1);
  tm.crash(t2);

  const recovered = tm.recover();
  assert.deepEqual(recovered.sort(), [t1.id, t2.id].sort());
  assert.deepEqual(tm.store.entries(), { r1: 1, r2: 2, r3: 3 });
  assert.equal(tm.locks.heldBy(t1.id).size, 0);
  assert.equal(tm.locks.heldBy(t2.id).size, 0);
  assert.ok(tm.monitor.verify());
});
