import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSystem } from '../src/system.ts';
import { FaultInjector } from '../src/fault.ts';
import { runSchedule } from '../src/scheduler.ts';
import type { TxnOp } from '../src/scheduler.ts';
import { assertLogIntegrity } from './helpers.ts';

test('concurrency: interleaved increments lose no updates', () => {
  const system = new DatabaseSystem();
  const txnCount = 5;
  const incrementsPerTxn = 4;
  const scripts = Array.from({ length: txnCount }, () => ({
    ops: [
      ...Array.from({ length: incrementsPerTxn }, () => ({ op: 'add', pageId: 1, delta: 1 }) as TxnOp),
      { op: 'commit' } as TxnOp,
    ],
  }));
  const result = runSchedule(system, scripts);

  assert.equal(result.committed.length, txnCount);
  assert.equal(result.aborted.length, 0);
  system.flushAllPages();
  assert.equal(
    system.disk.pageValue(1),
    txnCount * incrementsPerTxn,
    'every committed increment is visible exactly once',
  );
  assert.equal(system.lockManager.lockCount, 0, 'no lock leaks');
  assert.equal(system.activeTxnCount, 0, 'no txn leaks');
});

test('concurrency: deadlock cycle is broken by aborting the youngest txn', () => {
  const system = new DatabaseSystem();
  // T1: lock P1, then P2. T2: lock P2, then P1. Classic cycle.
  const scripts = [
    {
      ops: [
        { op: 'set', pageId: 1, value: 11 },
        { op: 'set', pageId: 2, value: 12 },
        { op: 'commit' },
      ] as TxnOp[],
    },
    {
      ops: [
        { op: 'set', pageId: 2, value: 22 },
        { op: 'set', pageId: 1, value: 21 },
        { op: 'commit' },
      ] as TxnOp[],
    },
  ];
  const result = runSchedule(system, scripts);

  assert.equal(result.committed.length, 1, 'exactly one txn wins');
  assert.equal(result.aborted.length, 1, 'the other is chosen as deadlock victim');
  const [winner] = result.committed;
  assert.equal(winner, result.txnIds[0], 'the older txn (T1) survives; youngest dies');
  system.flushAllPages();
  // Winner's writes are all visible; victim's writes are fully rolled back.
  assert.equal(system.disk.pageValue(1), 11);
  assert.equal(system.disk.pageValue(2), 12);
  assert.equal(system.lockManager.lockCount, 0, 'victim released its locks');
  assert.equal(system.activeTxnCount, 0);
});

test('concurrency: disjoint pages commit independently without conflicts', () => {
  const system = new DatabaseSystem();
  const scripts = [1, 2, 3].map((i) => ({
    ops: [
      { op: 'set', pageId: i * 10, value: i },
      { op: 'add', pageId: i * 10, delta: i * 100 },
      { op: 'commit' },
    ] as TxnOp[],
  }));
  const result = runSchedule(system, scripts);
  assert.equal(result.committed.length, 3);
  system.flushAllPages();
  assert.equal(system.disk.pageValue(10), 101);
  assert.equal(system.disk.pageValue(20), 202);
  assert.equal(system.disk.pageValue(30), 303);
});

test('concurrency: explicit abort rolls back its own updates only', () => {
  const system = new DatabaseSystem();
  const scripts = [
    {
      ops: [
        { op: 'set', pageId: 1, value: 100 },
        { op: 'commit' },
      ] as TxnOp[],
    },
    {
      ops: [
        { op: 'set', pageId: 2, value: 200 },
        { op: 'abort' },
      ] as TxnOp[],
    },
  ];
  const result = runSchedule(system, scripts);
  assert.deepEqual(result.aborted, [result.txnIds[1]]);
  system.flushAllPages();
  assert.equal(system.disk.pageValue(1), 100);
  assert.equal(system.disk.pageValue(2), 0, 'aborted txn leaves no trace');
  assert.equal(system.lockManager.lockCount, 0);
});

test('concurrency: crash after an interleaved schedule recovers exactly the committed set', () => {
  const system = new DatabaseSystem();
  const faults = new FaultInjector(system);
  // 4 txns on 2 hot pages; T4 never commits (its script is cut short by the crash).
  const scripts = [
    { ops: [{ op: 'add', pageId: 1, delta: 1 }, { op: 'add', pageId: 2, delta: 10 }, { op: 'commit' }] as TxnOp[] },
    { ops: [{ op: 'add', pageId: 1, delta: 2 }, { op: 'add', pageId: 2, delta: 20 }, { op: 'commit' }] as TxnOp[] },
    { ops: [{ op: 'add', pageId: 1, delta: 4 }, { op: 'add', pageId: 2, delta: 40 }, { op: 'commit' }] as TxnOp[] },
  ];
  const result = runSchedule(system, scripts);
  assert.equal(result.committed.length, 3);

  // A 4th txn starts, dirties both pages, then the power dies mid-flight.
  const t4 = system.begin();
  system.update(t4, 1, -999);
  system.update(t4, 2, -999);
  system.logManager.flushAll();
  system.bufferPool.flushPage(1); // steal uncommitted data to disk
  faults.powerLoss();

  const report = system.recover();
  assert.deepEqual(report.undo.losers, [t4]);
  assert.equal(system.disk.pageValue(1), 1 + 2 + 4, 'committed increments survive');
  assert.equal(system.disk.pageValue(2), 10 + 20 + 40);
  assert.equal(system.lockManager.lockCount, 0);
  assert.equal(system.activeTxnCount, 0);
  assertLogIntegrity(system);
});

test('concurrency: read-modify-write under locks is atomic (no dirty reads)', () => {
  const system = new DatabaseSystem();
  // Two txns both read-modify-write the same counter; serialization via 2PL
  // must make the final value exactly the sum of both deltas.
  const scripts = [
    { ops: [{ op: 'add', pageId: 7, delta: 50 }, { op: 'commit' }] as TxnOp[] },
    { ops: [{ op: 'add', pageId: 7, delta: 70 }, { op: 'commit' }] as TxnOp[] },
  ];
  runSchedule(system, scripts);
  system.flushAllPages();
  assert.equal(system.disk.pageValue(7), 120);
});
