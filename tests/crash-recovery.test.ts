import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TransactionAbortedError } from '../src/errors.ts';
import { LockMonitor } from '../src/lock-monitor.ts';
import { TransactionManager } from '../src/transaction-manager.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

const newManager = () => {
  const tm = new TransactionManager();
  const monitor = new LockMonitor(tm.lockManager, (id) => tm.get(id)?.phase);
  return { tm, monitor };
};

test('injected crash undoes all modifications and revokes every lock', async () => {
  const { tm, monitor } = newManager();
  tm.store.write('a', 10);
  tm.store.write('b', 20);
  const t1 = tm.begin();
  await t1.write('a', -1);
  await t1.write('b', -2);
  await t1.write('c', 30); // brand new key
  assert.equal(tm.lockManager.heldResources(t1.id).size, 3);

  const t2 = tm.begin();
  let observed: number | undefined;
  const pending = t2.read('a').then((value) => {
    observed = value;
  });
  await sleep(20);

  tm.injectCrash(t1.id);

  assert.equal(t1.phase, 'aborted');
  assert.equal(t1.abortReason, 'crash');
  assert.equal(tm.lockManager.heldResources(t1.id).size, 0, 'all locks revoked');
  assert.equal(tm.store.read('a'), 10, 'a rolled back');
  assert.equal(tm.store.read('b'), 20, 'b rolled back');
  assert.equal(tm.store.has('c'), false, 'uncommitted insert removed');

  await pending;
  assert.equal(observed, 10, 'blocked reader proceeds and sees the restored value');
  t2.commit();
  monitor.assertNoEarlyRelease();
});

test('crashing a waiting transaction removes it from lock queues', async () => {
  const { tm, monitor } = newManager();
  tm.store.write('r', 1);
  const t1 = tm.begin();
  await t1.write('r', 2);

  const t2 = tm.begin();
  const pending = t2.read('r');
  const assertion = assert.rejects(pending, TransactionAbortedError);
  await sleep(20);
  assert.equal(tm.lockManager.queueLength('r'), 1);

  tm.injectCrash(t2.id);
  await assertion;
  assert.equal(tm.lockManager.queueLength('r'), 0, 'crashed waiter dequeued');

  // The survivor is unaffected and can commit normally.
  t1.commit();
  assert.equal(tm.store.read('r'), 2);
  monitor.assertNoEarlyRelease();
});

test('deadlock victim is safely aborted and the survivor commits', async () => {
  const { tm, monitor } = newManager();
  tm.store.write('x', 1);
  tm.store.write('y', 1);
  const t1 = tm.begin();
  const t2 = tm.begin();
  await t1.write('x', 10);
  await t2.write('y', 20);

  // t1 waits for y (held by t2); t2 then closes the cycle by requesting x.
  const p1 = t1.read('y');
  await assert.rejects(t2.write('x', 21), /deadlock/);

  assert.equal(t2.phase, 'aborted');
  assert.equal(t2.abortReason, 'deadlock');
  assert.equal(tm.lockManager.heldResources(t2.id).size, 0, 'victim locks revoked');
  assert.equal(tm.store.read('y'), 1, 'victim write on y rolled back');
  assert.equal(tm.store.read('x'), 10, 'survivor write untouched');
  assert.equal(await p1, 1, 'survivor read unblocks and sees the restored value');

  t1.commit();
  assert.equal(tm.store.read('x'), 10);
  assert.equal(tm.store.read('y'), 1);
  assert.deepEqual(tm.commitOrder(), [t1.id]);
  monitor.assertNoEarlyRelease();
});
