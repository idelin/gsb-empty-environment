import assert from 'node:assert/strict';
import { test } from 'node:test';
import { LockManager } from '../src/lockManager.ts';
import { ShrinkPhaseManager } from '../src/monitor.ts';
import { ShrinkPhaseViolation } from '../src/types.ts';

function setup() {
  const monitor = new ShrinkPhaseManager();
  const locks = new LockManager(monitor);
  // The only legal release path: inside the monitor's atomic shrinking point.
  const commitPoint = (txId: number) => {
    monitor.beginAtomicRelease(txId);
    locks.releaseAll(txId);
    monitor.endAtomicRelease(txId);
  };
  return { monitor, locks, commitPoint };
}

test('growing phase: shared locks are compatible, exclusive blocks', async () => {
  const { locks, commitPoint } = setup();
  await locks.acquire(1, 'rowA', 'S');
  await locks.acquire(2, 'rowA', 'S'); // S+S compatible
  assert.deepEqual([...locks.heldBy(1).keys()], ['rowA']);
  assert.deepEqual([...locks.heldBy(2).keys()], ['rowA']);

  let xGranted = false;
  const pending = locks.acquire(3, 'rowA', 'X').then(() => { xGranted = true; });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(xGranted, false, 'X must wait while S locks are held');

  commitPoint(1);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(xGranted, false, 'X still waits: tx2 S lock remains');
  commitPoint(2);
  await pending;
  assert.equal(xGranted, true);
});

test('lock upgrade S -> X waits for other readers, then upgrades', async () => {
  const { locks, monitor, commitPoint } = setup();
  monitor.register(1);
  monitor.register(2);
  await locks.acquire(1, 'rowB', 'S');
  await locks.acquire(2, 'rowB', 'S');

  let upgraded = false;
  const pending = locks.acquire(1, 'rowB', 'X').then(() => { upgraded = true; });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(upgraded, false);

  commitPoint(2);
  await pending;
  assert.equal(upgraded, true);
  assert.equal(locks.heldBy(1).get('rowB'), 'X');
});

test('monitor forbids ANY early release before the shrinking point', async () => {
  const { locks, monitor } = setup();
  monitor.register(1);
  await locks.acquire(1, 'rowC', 'X');
  await locks.acquire(1, 'rowD', 'S');

  // Simulate a buggy engine trying to drop one local row lock mid-flight.
  assert.throws(
    () => monitor.onRelease(1, 'rowC', 'X'),
    (error: unknown) => error instanceof ShrinkPhaseViolation,
  );
  assert.equal(monitor.earlyReleases().length, 1);
  assert.match(monitor.violations[0], /ILLEGAL early release/);
  assert.ok(monitor.isGrowing(1));
  assert.equal(locks.heldBy(1).size, 2, 'both locks still held');
});

test('no acquire allowed after shrinking point (2PL rule)', async () => {
  const { locks, monitor, commitPoint } = setup();
  monitor.register(1);
  await locks.acquire(1, 'rowE', 'X');
  commitPoint(1);
  monitor.setPhase(1, 'COMMITTED');

  monitor.onAcquire(1, 'rowF', 'S'); // engine bug: lock after commit
  assert.equal(monitor.verify(), false);
  assert.match(monitor.violations[0], /while in phase COMMITTED/);
});
