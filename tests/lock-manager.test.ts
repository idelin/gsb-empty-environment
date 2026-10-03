import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LockManager } from '../src/lock-manager.ts';
import { DeadlockError } from '../src/errors.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

test('shared locks are compatible with each other', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'r', 'shared');
  await lm.acquire(2, 'r', 'shared');
  assert.equal(lm.holdersOf('r').size, 2);
});

test('exclusive lock blocks others until releaseAll', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'r', 'exclusive');
  let granted = false;
  const pending = lm.acquire(2, 'r', 'exclusive').then(() => {
    granted = true;
  });
  await sleep(20);
  assert.equal(granted, false);
  assert.equal(lm.queueLength('r'), 1);
  const released = lm.releaseAll(1);
  assert.deepEqual(released, [{ resource: 'r', mode: 'exclusive' }]);
  await pending;
  assert.equal(granted, true);
  assert.deepEqual([...lm.holdersOf('r').keys()], [2]);
});

test('FIFO fairness: shared request queued behind an exclusive waiter still waits', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'r', 'exclusive');
  const order: number[] = [];
  const p2 = lm.acquire(2, 'r', 'exclusive').then(() => void order.push(2));
  const p3 = lm.acquire(3, 'r', 'shared').then(() => void order.push(3));
  await sleep(20);
  lm.releaseAll(1);
  await sleep(20);
  assert.deepEqual(order, [2]);
  assert.equal(lm.holdersOf('r').get(2), 'exclusive');
  lm.releaseAll(2);
  await Promise.all([p2, p3]);
  assert.deepEqual(order, [2, 3]);
});

test('compatible shared waiters are granted together', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'r', 'exclusive');
  const p2 = lm.acquire(2, 'r', 'shared');
  const p3 = lm.acquire(3, 'r', 'shared');
  await sleep(20);
  lm.releaseAll(1);
  await Promise.all([p2, p3]);
  assert.equal(lm.holdersOf('r').size, 2);
});

test('lock upgrade succeeds when the transaction is the sole holder', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'r', 'shared');
  await lm.acquire(1, 'r', 'exclusive');
  assert.equal(lm.holdersOf('r').get(1), 'exclusive');
});

test('mutual upgrade deadlock is detected and the cycle-closing request is rejected', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'r', 'shared');
  await lm.acquire(2, 'r', 'shared');
  const p1 = lm.acquire(1, 'r', 'exclusive');
  await assert.rejects(lm.acquire(2, 'r', 'exclusive'), DeadlockError);
  lm.releaseAll(2);
  await p1;
  assert.equal(lm.holdersOf('r').get(1), 'exclusive');
});

test('cross-resource deadlock is detected via the wait-for graph', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'a', 'exclusive');
  await lm.acquire(2, 'b', 'exclusive');
  const p1 = lm.acquire(1, 'b', 'exclusive');
  await assert.rejects(lm.acquire(2, 'a', 'exclusive'), DeadlockError);
  lm.releaseAll(2);
  await p1;
  assert.equal(lm.holdersOf('b').get(1), 'exclusive');
});

test('releaseAll drops every held resource in one shot', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'a', 'exclusive');
  await lm.acquire(1, 'b', 'shared');
  await lm.acquire(1, 'c', 'exclusive');
  const released = lm.releaseAll(1);
  assert.equal(released.length, 3);
  assert.equal(lm.heldResources(1).size, 0);
  assert.equal(lm.holdersOf('a').size, 0);
  assert.equal(lm.holdersOf('b').size, 0);
  assert.equal(lm.holdersOf('c').size, 0);
});

test('cancelWaiting rejects queued requests of a transaction', async () => {
  const lm = new LockManager();
  await lm.acquire(1, 'r', 'exclusive');
  const pending = lm.acquire(2, 'r', 'shared');
  const assertion = assert.rejects(pending, /cancelled/);
  await sleep(10);
  lm.cancelWaiting(2, new Error('cancelled'));
  await assertion;
  assert.equal(lm.queueLength('r'), 0);
});
