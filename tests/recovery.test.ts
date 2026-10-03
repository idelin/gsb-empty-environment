import assert from 'node:assert/strict';
import test from 'node:test';
import { CrashError } from '../src/faults.ts';
import {
  assertNoLeaks,
  assertPageLSNConsistent,
  boot,
  crash,
  diskData,
  modelReplay,
  reboot,
  recover,
} from './helpers.ts';

test('recovery is idempotent: a second recovery is a no-op', async () => {
  const sys = boot({ seed: 11 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'x', 1);
  await sys.tm.commit(t1);
  const t2 = await sys.tm.begin();
  await sys.tm.set(t2, 1, 'y', 2);
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  const report1 = await recover(sys2);
  assert.deepEqual(report1.losers, [t2]);
  const stateAfterFirst = diskData(sys2);

  const sys3 = boot(crash(sys2)); // crash again with no new work
  const report2 = await recover(sys3);
  assert.deepEqual(report2.redone, [], 'nothing left to redo');
  assert.deepEqual(report2.clrs, [], 'nothing left to undo');
  assert.deepEqual(report2.losers, []);
  assert.deepEqual(diskData(sys3), stateAfterFirst, 'state must be stable');
  assertNoLeaks(sys3);
});

test('crash in the middle of undo: recovery restarts and completes', async () => {
  const sys = boot({ seed: 13 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'a', 1);
  await sys.tm.commit(t1);
  const t2 = await sys.tm.begin();
  await sys.tm.set(t2, 1, 'b', 2);
  await sys.tm.set(t2, 2, 'c', 3);
  await sys.tm.set(t2, 1, 'a', 99); // overwrites committed data -> must be undone
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  sys2.injector.arm('recovery.undo', 1); // power cut during undo
  await assert.rejects(() => recover(sys2), CrashError);

  // Reboot again onto the same durable state; recovery must resume cleanly.
  const sys3 = boot(crash(sys2));
  const report = await recover(sys3);
  assert.deepEqual(report.losers, [t2]);
  assert.equal(report.clrs.length, 3, 'each loser update compensated exactly once');
  assert.deepEqual(sys3.disk.readPage(1)!.data, { a: 1 });
  assert.deepEqual(sys3.disk.readPage(2)!.data, {});
  assertPageLSNConsistent(sys3);
  assertNoLeaks(sys3);
});

test('committed txn whose END record was lost gets END rewritten (winner)', async () => {
  const sys = boot({ seed: 17 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'k', 'v');
  await sys.tm.commit(t1); // COMMIT is flushed; END stays in the volatile buffer
  assert.ok(sys.wal.bufferedCount() > 0, 'END must still be buffered');

  const sys2 = boot(crash(sys)); // END is lost
  const report = await recover(sys2);
  assert.deepEqual(report.winners, [t1]);
  assert.deepEqual(report.losers, []);
  assert.deepEqual(sys2.disk.readPage(1)!.data, { k: 'v' });
  const ends = sys2.wal.readDurable().filter((r) => r.type === 'END' && r.txnId === t1);
  assert.equal(ends.length, 1, 'recovery must rewrite the lost END record');
  assertNoLeaks(sys2);
});

test('txn that was mid-abort at crash resumes undo via undoNextLSN', async () => {
  const sys = boot({ seed: 19 });
  const t = await sys.tm.begin();
  await sys.tm.set(t, 1, 'x', 10);
  await sys.tm.set(t, 1, 'y', 20);

  // Simulate an abort that only managed to compensate the second update
  // before the power cut (ABORT + one CLR written by hand).
  const updates = sys.wal.allRecords().filter((r) => r.txnId === t && r.type === 'UPDATE');
  const last = updates[updates.length - 1];
  const abortRec = await sys.wal.append({ type: 'ABORT', txnId: t, prevLSN: last.lsn });
  const clr = await sys.wal.append({
    type: 'CLR',
    txnId: t,
    prevLSN: abortRec.lsn,
    pageId: last.pageId,
    key: last.key,
    before: last.after,
    after: last.before,
    undoNextLSN: last.prevLSN,
  });
  sys.bp.applyDelete(last.pageId!, last.key!, clr.lsn);
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  const report = await recover(sys2);
  assert.deepEqual(report.losers, [t]);
  assert.equal(report.clrs.length, 1, 'only the remaining update is compensated');
  assert.deepEqual(sys2.disk.readPage(1)!.data, {}, 'both updates rolled back');
  assertPageLSNConsistent(sys2);
  assertNoLeaks(sys2);
});

test('empty transaction (BEGIN only) is ended without any CLR', async () => {
  const sys = boot({ seed: 23 });
  await sys.tm.begin();
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  const report = await recover(sys2);
  assert.equal(report.losers.length, 1);
  assert.deepEqual(report.clrs, []);
  assert.equal(report.txnTableAfter.size, 0);
  assertNoLeaks(sys2);
});

test('normal abort writes CLRs and leaves nothing for recovery', async () => {
  const sys = boot({ seed: 29 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'keep', 1);
  await sys.tm.commit(t1);
  const t2 = await sys.tm.begin();
  await sys.tm.set(t2, 1, 'drop', 2);
  await sys.tm.set(t2, 1, 'keep', 100);
  await sys.tm.abort(t2);
  assert.deepEqual(sys.bp.fetchPage(1).data, { keep: 1 }, 'abort restores before-images');
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  const report = await recover(sys2);
  assert.deepEqual(report.losers, [], 'aborted txn was already ENDed');
  assert.deepEqual(report.clrs, []);
  assert.deepEqual(sys2.disk.readPage(1)!.data, { keep: 1 });
  assertNoLeaks(sys2);
});

test('recovered state matches independent model replay of the durable log', async () => {
  const sys = boot({ seed: 31 });
  const t1 = await sys.tm.begin();
  const t2 = await sys.tm.begin();
  const t3 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'a', 1);
  await sys.tm.set(t2, 1, 'b', 2);
  await sys.tm.set(t3, 2, 'c', 3);
  await sys.tm.commit(t1);
  await sys.tm.set(t2, 2, 'd', 4);
  await sys.tm.commit(t2);
  await sys.tm.set(t3, 1, 'a', 999);
  await sys.tm.checkpoint();
  await sys.tm.set(t3, 2, 'c', 999);
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  await recover(sys2);
  const expected = modelReplay(sys2.wal.readDurable());
  assert.deepEqual(diskData(sys2), expected, 'disk must equal model replay of durable log');
  assertPageLSNConsistent(sys2);
  assertNoLeaks(sys2);
});

test('recovery without any checkpoint scans the whole log', async () => {
  const sys = boot({ seed: 37 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'a', 1);
  await sys.tm.commit(t1);
  const t2 = await sys.tm.begin();
  await sys.tm.set(t2, 1, 'b', 2);
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  const report = await recover(sys2);
  assert.equal(report.checkpointLSN, 0);
  assert.deepEqual(report.losers, [t2]);
  assert.deepEqual(sys2.disk.readPage(1)!.data, { a: 1 });
  assertNoLeaks(sys2);
});

test('recovery of an empty log is a clean no-op', async () => {
  const sys = boot({ seed: 41 });
  const report = await recover(sys);
  assert.deepEqual(report.redone, []);
  assert.deepEqual(report.clrs, []);
  assert.equal(report.txnTableAfter.size, 0);
  assertNoLeaks(sys);
});
