import assert from 'node:assert/strict';
import test from 'node:test';
import {
  assertNoLeaks,
  assertPageLSNConsistent,
  boot,
  crash,
  recover,
} from './helpers.ts';

/**
 * Acceptance scenario from the spec:
 *  - 2 committed transactions (T1, T2) and 3 uncommitted (T3, T4, T5)
 *  - a fuzzy checkpoint taken in the middle of the workload
 *  - a crash after the checkpoint, with one stolen dirty page on disk and
 *    one WAL tail record that never reached durable storage
 *  - recovery must run analysis -> redo -> undo, persist committed data and
 *    roll back every uncommitted modification, leaking nothing.
 */
test('acceptance: 3 uncommitted + 2 committed txns, crash after checkpoint', async () => {
  const sys = boot({ seed: 7 });
  const { tm, bp } = sys;

  const t1 = await tm.begin(); // will commit
  await tm.set(t1, 1, 'a', 1);
  const t2 = await tm.begin(); // will commit
  await tm.set(t2, 2, 'b', 2);
  await tm.commit(t1);

  const t3 = await tm.begin(); // loser
  await tm.set(t3, 1, 'c', 3);
  const t4 = await tm.begin(); // loser
  await tm.set(t4, 3, 'd', 4);

  const ckptLSN = await tm.checkpoint(); // TT={t2,t3,t4}, DPT={1,2,3}

  await tm.set(t2, 2, 'e', 5);
  await tm.commit(t2);

  await tm.set(t3, 4, 'f', 6);
  const t5 = await tm.begin(); // loser
  await tm.set(t5, 3, 'g', 7);
  await tm.set(t4, 1, 'h', 8);

  await bp.flushPage(1); // steal: uncommitted c=3, h=8 reach disk

  await tm.set(t5, 4, 'i', 9); // buffered only -> lost in the crash

  // --- pre-crash sanity: steal really put uncommitted data on disk ---
  assert.deepEqual(sys.disk.readPage(1)!.data, { a: 1, c: 3, h: 8 });
  assert.equal(sys.disk.readPage(2), undefined, 'committed P2 must NOT be forced to disk');

  const durable = crash(sys); // power cut
  const sys2 = boot(durable);

  // The buffered tail record (i=9) is gone for good.
  assert.ok(!sys2.wal.readDurable().some((r) => r.after === 9), 'lost tail record must stay lost');

  const report = await recover(sys2);

  // ---- analysis ----
  assert.equal(report.checkpointLSN, ckptLSN);
  assert.deepEqual(
    [...report.txnTable.keys()].sort((a, b) => a - b),
    [t3, t4, t5],
    'analysis must find exactly the 3 uncommitted txns',
  );
  assert.deepEqual(
    [...report.dirtyPageTable.keys()].sort((a, b) => a - b),
    [1, 2, 3, 4],
    'DPT must contain every page dirtied since the checkpoint plus checkpoint DPT',
  );
  assert.deepEqual(report.losers, [t3, t4, t5]);
  assert.deepEqual(report.winners, []);

  // ---- redo repeated history ----
  assert.ok(report.redone.length > 0, 'redo must re-apply lost updates');

  // ---- undo compensated every loser update ----
  // T3: c(P1), f(P4); T4: d(P3), h(P1); T5: g(P3) -> 5 CLRs
  assert.equal(report.clrs.length, 5, 'one CLR per loser UPDATE');
  assert.equal(
    sys2.wal.readDurable().filter((r) => r.type === 'CLR').length,
    5,
    'all CLRs must be durable',
  );

  // ---- final durable state: committed persisted, uncommitted rolled back ----
  assert.deepEqual(sys2.disk.readPage(1)!.data, { a: 1 });
  assert.deepEqual(sys2.disk.readPage(2)!.data, { b: 2, e: 5 });
  assert.deepEqual(sys2.disk.readPage(3)!.data, {});
  assert.deepEqual(sys2.disk.readPage(4)!.data, {});

  // ---- strict no-leak assertions ----
  assertPageLSNConsistent(sys2);
  assertNoLeaks(sys2, () => {
    assert.equal(report.txnTableAfter.size, 0, 'transaction table must be empty after undo');
  });
});
