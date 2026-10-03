import { test } from 'node:test';
import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { rmSync } from 'node:fs';
import { Database } from '../src/aries/engine.ts';
import { RecoveryManager } from '../src/aries/recovery.ts';
import { WalWriter, readWal } from '../src/aries/wal.ts';
import type { ClrRecord, LogRecord } from '../src/aries/types.ts';

let counter = 0;
function walPath(tag: string): string {
  return join(tmpdir(), `aries-test-${process.pid}-${counter++}-${tag}.wal`);
}

function cleanup(path: string): void {
  rmSync(path, { force: true });
}

function recordsOfType(log: LogRecord[], type: LogRecord['type']): LogRecord[] {
  return log.filter((record) => record.type === type);
}

test('acceptance: 3 uncommitted + 2 committed txns, checkpoint, crash, 3-phase recovery', (t) => {
  const path = walPath('acceptance');
  t.after(() => cleanup(path));

  const db = Database.create(path);
  db.seedPage('A', 100);
  db.seedPage('B', 200);
  db.seedPage('C', 300);
  db.seedPage('D', 400);
  db.seedPage('E', 500);

  const t1 = db.begin();
  db.write(t1, 'A', 150);
  const t2 = db.begin();
  db.write(t2, 'B', 250);
  const checkpointLSN = db.checkpoint();
  db.write(t1, 'C', 350);
  db.commit(t1);
  const t3 = db.begin();
  db.write(t3, 'D', 450);
  db.write(t3, 'A', 999);
  db.write(t2, 'E', 550);
  db.commit(t2);
  const t4 = db.begin();
  db.write(t4, 'B', 777);
  const t5 = db.begin();
  db.write(t5, 'C', 888);
  db.write(t5, 'E', 111);

  db.flushPage('A');
  assert.equal(db.diskValue('A'), 999, 'steal: uncommitted A=999 reached disk before crash');
  db.crash();
  assert.equal(readWal(path).length, 19);

  const recovery = new RecoveryManager(path, db.diskPages);
  const result = recovery.recover();

  assert.equal(result.analysis.checkpointLSN, checkpointLSN);
  assert.deepEqual(
    [...result.analysis.txnTable.values()].map((e) => [e.txId, e.status, e.lastLSN]),
    [
      [t3, 'RUNNING', 11],
      [t4, 'RUNNING', 16],
      [t5, 'RUNNING', 19],
    ],
  );
  assert.deepEqual(
    [...result.analysis.dirtyPageTable.entries()].sort(),
    [
      ['A', 2],
      ['B', 4],
      ['C', 6],
      ['D', 10],
      ['E', 12],
    ],
  );

  assert.equal(result.redo.startLSN, 2);
  assert.deepEqual(result.redo.applied, [4, 6, 10, 12, 16, 18, 19]);
  assert.deepEqual(result.redo.skipped, [2, 11], 'page A was flushed at LSN 11, redo must skip older images');

  assert.deepEqual([...result.undo.losers].sort(), [t3, t4, t5]);
  assert.equal(result.undo.clrLSNs.length, 5);
  assert.deepEqual([...result.undo.endedTxIds].sort(), [t3, t4, t5]);
  assert.equal(result.undo.flushedPages, 5);

  assert.equal(db.diskValue('A'), 150, 'T1 committed value restored, T3 rolled back');
  assert.equal(db.diskValue('B'), 250, 'T2 committed value restored, T4 rolled back');
  assert.equal(db.diskValue('C'), 350, 'T1 committed value restored, T5 rolled back');
  assert.equal(db.diskValue('D'), 400, 'T3 uncommitted write undone');
  assert.equal(db.diskValue('E'), 550, 'T2 committed value restored, T5 rolled back');

  const finalLog = readWal(path);
  assert.equal(finalLog.length, 27);
  const clrs = recordsOfType(finalLog, 'CLR') as ClrRecord[];
  assert.deepEqual(
    clrs.map((c) => [c.txId, c.pageId, c.after, c.undoNextLSN]),
    [
      [t5, 'E', 550, 18],
      [t5, 'C', 350, 17],
      [t4, 'B', 250, 15],
      [t3, 'A', 150, 10],
      [t3, 'D', 400, 9],
    ],
  );
  assert.equal(recordsOfType(finalLog, 'END').length, 5);
  assert.equal(recovery.closed, true, 'recovery must close its WAL handle (no fd leak)');

  const again = new RecoveryManager(path, db.diskPages);
  const second = again.recover();
  assert.equal(second.analysis.txnTable.size, 0, 'recovery is idempotent: no losers remain');
  assert.equal(second.undo.clrLSNs.length, 0);
  assert.equal(second.redo.applied.length, 0);
  assert.equal(db.diskValue('A'), 150);
  assert.equal(db.diskValue('E'), 550);
});

test('wal file round-trips every record type exactly', (t) => {
  const path = walPath('roundtrip');
  t.after(() => cleanup(path));
  const wal = WalWriter.create(path);
  wal.append({ type: 'BEGIN', txId: 1, prevLSN: 0 });
  wal.append({ type: 'UPDATE', txId: 1, prevLSN: 1, pageId: 'P', before: 0, after: 42 });
  wal.append({ type: 'CLR', txId: 1, prevLSN: 2, pageId: 'P', after: 0, undoNextLSN: 1 });
  wal.append({ type: 'COMMIT', txId: 1, prevLSN: 3 });
  wal.append({ type: 'ABORT', txId: 2, prevLSN: 0 });
  wal.append({ type: 'END', txId: 1, prevLSN: 4 });
  wal.append({ type: 'CHECKPOINT', txTable: [{ txId: 2, status: 'ABORTING', lastLSN: 5 }], dirtyPages: [{ pageId: 'P', recLSN: 2 }] });
  wal.flush();
  const read = readWal(path);
  assert.deepEqual(read, wal.records);
  assert.equal(read.length, 7);
  wal.close();
  assert.equal(wal.closed, true);
  assert.throws(() => wal.append({ type: 'END', txId: 9, prevLSN: 0 }), /closed/);
});

test('txn aborted before crash stays aborted; committed txn survives', (t) => {
  const path = walPath('abort');
  t.after(() => cleanup(path));
  const db = Database.create(path);
  db.seedPage('X', 0);
  db.seedPage('Y', 100);
  const t1 = db.begin();
  db.write(t1, 'X', 1);
  db.write(t1, 'Y', 2);
  db.abort(t1);
  const t2 = db.begin();
  db.write(t2, 'X', 10);
  db.commit(t2);
  db.crash();

  const result = new RecoveryManager(path, db.diskPages).recover();
  assert.equal(result.analysis.txnTable.size, 0, 'both txns ended before crash');
  assert.equal(result.undo.clrLSNs.length, 0);
  assert.equal(db.diskValue('X'), 10);
  assert.equal(db.diskValue('Y'), 100, 'aborted writes never survive');
});

test('crash mid-undo: ABORTING txn resumes via CLR undoNextLSN, no double undo', (t) => {
  const path = walPath('midundo');
  t.after(() => cleanup(path));
  const disk = new Map<string, { data: number; pageLSN: number }>([
    ['P', { data: 7, pageLSN: 0 }],
    ['Q', { data: 70, pageLSN: 0 }],
  ]);
  const wal = WalWriter.create(path);
  wal.append({ type: 'BEGIN', txId: 1, prevLSN: 0 });
  wal.append({ type: 'UPDATE', txId: 1, prevLSN: 1, pageId: 'P', before: 7, after: 8 });
  wal.append({ type: 'UPDATE', txId: 1, prevLSN: 2, pageId: 'Q', before: 70, after: 80 });
  wal.append({ type: 'ABORT', txId: 1, prevLSN: 3 });
  wal.append({ type: 'CLR', txId: 1, prevLSN: 4, pageId: 'Q', after: 70, undoNextLSN: 2 });
  wal.close();

  const result = new RecoveryManager(path, disk).recover();
  assert.equal(result.analysis.txnTable.get(1)?.status, 'ABORTING');
  assert.deepEqual(result.undo.losers, [1]);
  assert.equal(result.undo.clrLSNs.length, 1, 'only P still needs undo; Q was already compensated');
  const newClr = recordsOfType(readWal(path), 'CLR').at(-1) as ClrRecord;
  assert.equal(newClr.pageId, 'P');
  assert.equal(newClr.after, 7);
  assert.equal(newClr.undoNextLSN, 1);
  assert.equal(disk.get('P')?.data, 7);
  assert.equal(disk.get('Q')?.data, 70);
});

test('empty and checkpoint-only WALs recover cleanly', (t) => {
  const emptyPath = walPath('empty');
  const cpPath = walPath('checkpoint-only');
  t.after(() => {
    cleanup(emptyPath);
    cleanup(cpPath);
  });
  const disk = new Map<string, { data: number; pageLSN: number }>();
  const empty = new RecoveryManager(emptyPath, disk).recover();
  assert.equal(empty.analysis.checkpointLSN, null);
  assert.equal(empty.redo.startLSN, null);
  assert.deepEqual(empty.undo.losers, []);

  const wal = WalWriter.create(cpPath);
  wal.append({ type: 'CHECKPOINT', txTable: [], dirtyPages: [] });
  wal.close();
  const cp = new RecoveryManager(cpPath, disk).recover();
  assert.equal(cp.analysis.checkpointLSN, 1);
  assert.equal(cp.analysis.txnTable.size, 0);
  assert.equal(cp.redo.startLSN, null);
});
