import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { LogManager } from '../src/wal.ts';
import type { LogRecord } from '../src/wal.ts';
import { PageStore } from '../src/storage.ts';
import { RecoveryManager } from '../src/recovery.ts';

let dir: string;
let walPath: string;
let pagePath: string;

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'aries-'));
  walPath = join(dir, 'wal.log');
  pagePath = join(dir, 'pages.jsonl');
});

afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

/**
 * Acceptance scenario: 5 transactions, checkpoint, then crash.
 *
 *  LSN 1  T1 update P1 init1  -> T1-data      (committed)
 *  LSN 2  T2 update P2 init2  -> T2-data      (committed)
 *  LSN 3  T1 commit
 *  LSN 4  T1 end
 *  LSN 5  T3 update P1 T1-data -> T3-a        (loser)
 *  LSN 6  T2 commit
 *  LSN 7  T2 end
 *  LSN 8  CHECKPOINT  TT={T3@5} DPT={P1:1, P2:2}
 *  LSN 9  T4 update P3 init3  -> T4-data      (loser)
 *  LSN 10 T5 update P2 T2-data -> T5-a        (loser)
 *  LSN 11 T3 update P4 init4  -> T3-b         (loser)
 *  LSN 12 T4 update P1 T3-a   -> T4-x         (loser)
 *  --- CRASH ---
 *
 * P2 is pre-flushed to disk with pageLSN=2 to prove redo skips
 * already-persisted updates.
 */
async function buildCrashedSystem(): Promise<void> {
  const wal = new LogManager(walPath);
  wal.append({ type: 'update', txnId: 1, pageId: 'P1', prevLSN: 0, before: 'init1', after: 'T1-data' });
  wal.append({ type: 'update', txnId: 2, pageId: 'P2', prevLSN: 0, before: 'init2', after: 'T2-data' });
  wal.append({ type: 'commit', txnId: 1, prevLSN: 1 });
  wal.append({ type: 'end', txnId: 1, prevLSN: 3 });
  wal.append({ type: 'update', txnId: 3, pageId: 'P1', prevLSN: 0, before: 'T1-data', after: 'T3-a' });
  wal.append({ type: 'commit', txnId: 2, prevLSN: 2 });
  wal.append({ type: 'end', txnId: 2, prevLSN: 6 });
  wal.append({
    type: 'checkpoint',
    transactions: { '3': { status: 'running', lastLSN: 5 } },
    dirtyPages: { P1: 1, P2: 2 },
  });
  wal.append({ type: 'update', txnId: 4, pageId: 'P3', prevLSN: 0, before: 'init3', after: 'T4-data' });
  wal.append({ type: 'update', txnId: 5, pageId: 'P2', prevLSN: 0, before: 'T2-data', after: 'T5-a' });
  wal.append({ type: 'update', txnId: 3, pageId: 'P4', prevLSN: 5, before: 'init4', after: 'T3-b' });
  wal.append({ type: 'update', txnId: 4, pageId: 'P1', prevLSN: 9, before: 'T3-a', after: 'T4-x' });
  await wal.flush();

  // Only P2 (committed, pageLSN=2) reached disk before the crash.
  const pages = new PageStore(pagePath);
  pages.apply('P2', 'T2-data', 2);
  await pages.flush();
}

test('analysis rebuilds TT and DPT from checkpoint forward', async () => {
  await buildCrashedSystem();
  const rmgr = new RecoveryManager(walPath, new PageStore(pagePath));
  const report = await rmgr.recover();

  const { transactionTable, dirtyPageTable, losers, checkpointLSN } = report.analysis;
  assert.equal(checkpointLSN, 8);
  assert.deepEqual(losers, [3, 4, 5]);
  assert.deepEqual(
    [...transactionTable.keys()].sort(),
    [3, 4, 5],
    'ended transactions T1/T2 must not appear in the TT',
  );
  assert.equal(transactionTable.get(3)!.lastLSN, 11);
  assert.equal(transactionTable.get(4)!.lastLSN, 12);
  assert.equal(transactionTable.get(5)!.lastLSN, 10);
  assert.deepEqual(
    Object.fromEntries(dirtyPageTable),
    { P1: 1, P2: 2, P3: 9, P4: 11 },
  );
});

test('redo repeats history but skips pages already persisted (pageLSN)', async () => {
  await buildCrashedSystem();
  const rmgr = new RecoveryManager(walPath, new PageStore(pagePath));
  const report = await rmgr.recover();

  // LSN 2 is skipped: P2 was flushed with pageLSN=2 before the crash.
  assert.deepEqual(report.redoneLSNs, [1, 5, 9, 10, 11, 12]);
});

test('undo rolls back losers in reverse LSN order with CLRs and ENDs', async () => {
  await buildCrashedSystem();
  const rmgr = new RecoveryManager(walPath, new PageStore(pagePath));
  const report = await rmgr.recover();

  assert.deepEqual(report.undoneLSNs, [12, 11, 10, 9, 5], 'strictly reverse LSN order');
  assert.equal(report.clrLSNs.length, 5, 'one CLR per undone update');

  const wal = await LogManager.readAll(walPath);
  const clrs = wal.filter((r) => r.type === 'clr');
  const ends = wal.filter((r) => r.type === 'end' && r.lsn > 8);
  assert.equal(clrs.length, 5);
  assert.equal(ends.length, 3);
  assert.deepEqual(
    ends.map((e) => (e.type === 'end' ? e.txnId : 0)).sort(),
    [3, 4, 5],
    'every loser gets an END record',
  );

  // CLR undoNextLSN chains follow the original prevLSN chains backwards.
  const clrFor12 = clrs.find((r) => r.type === 'clr' && r.pageId === 'P1' && r.txnId === 4)!;
  assert.equal(clrFor12.type, 'clr');
  if (clrFor12.type === 'clr') assert.equal(clrFor12.undoNextLSN, 9);
  const clrFor11 = clrs.find((r) => r.type === 'clr' && r.txnId === 3 && r.pageId === 'P4')!;
  if (clrFor11.type === 'clr') assert.equal(clrFor11.undoNextLSN, 5);
});

test('committed data persists, uncommitted data is rolled back', async () => {
  await buildCrashedSystem();
  const pages = new PageStore(pagePath);
  const rmgr = new RecoveryManager(walPath, pages);
  await rmgr.recover();

  const final = new Map(pages.all().map((p) => [p.pageId, p.data]));
  assert.equal(final.get('P1'), 'T1-data', 'T1 committed; T3/T4 updates undone');
  assert.equal(final.get('P2'), 'T2-data', 'T2 committed; T5 update undone');
  assert.equal(final.get('P3'), 'init3', 'T4 never committed');
  assert.equal(final.get('P4'), 'init4', 'T3 never committed');
});

test('recovery is idempotent: a second crash/recovery changes nothing', async () => {
  await buildCrashedSystem();
  const pages = new PageStore(pagePath);
  const first = await new RecoveryManager(walPath, pages).recover();
  const snapshotAfterFirst = JSON.stringify(pages.all());

  const pages2 = new PageStore(pagePath);
  const second = await new RecoveryManager(walPath, pages2).recover();

  assert.deepEqual(second.analysis.losers, [], 'no losers remain after first recovery');
  assert.deepEqual(second.redoneLSNs, [], 'all pageLSNs are up to date');
  assert.deepEqual(second.undoneLSNs, [], 'nothing left to undo');
  assert.equal(JSON.stringify(pages2.all()), snapshotAfterFirst);
  assert.ok(first.clrLSNs.length > 0 && second.clrLSNs.length === 0);
});

test('CLR records written during undo are themselves redoable after re-crash', async () => {
  await buildCrashedSystem();
  // Recover once (writes CLRs + ENDs, applies undos to the page file).
  await new RecoveryManager(walPath, new PageStore(pagePath)).recover();

  // Simulate a second crash mid-recovery: wipe the page file entirely,
  // keep the WAL (which now contains the CLRs). Redo must replay the CLRs.
  const wipedPages = new PageStore(pagePath);
  await wipedPages.flush(); // empty page file
  const report = await new RecoveryManager(walPath, new PageStore(pagePath)).recover();

  const pages = new PageStore(pagePath);
  await pages.load();
  const final = new Map(pages.all().map((p) => [p.pageId, p.data]));
  assert.equal(final.get('P1'), 'T1-data');
  assert.equal(final.get('P2'), 'T2-data');
  assert.equal(final.get('P3'), 'init3');
  assert.equal(final.get('P4'), 'init4');
  assert.ok(report.redoneLSNs.some((lsn) => report.clrLSNs.includes(lsn) || lsn > 12),
    'CLR records are redone to restore the undone state');
});

test('WAL file on disk is valid JSONL with strictly increasing LSNs', async () => {
  await buildCrashedSystem();
  await new RecoveryManager(walPath, new PageStore(pagePath)).recover();

  const raw = await readFile(walPath, 'utf8');
  const records: LogRecord[] = raw
    .split('\n')
    .filter((l) => l.trim())
    .map((l) => JSON.parse(l));
  for (let i = 1; i < records.length; i++) {
    assert.ok(records[i].lsn > records[i - 1].lsn, 'LSNs strictly increase');
  }
});
