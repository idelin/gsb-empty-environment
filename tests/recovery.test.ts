import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LogManager } from '../src/log-manager.ts';
import { StorageEngine } from '../src/storage.ts';
import { AriesRecovery } from '../src/recovery.ts';
import { buildCrashScenario } from '../src/scenario.ts';
import type { LogRecord } from '../src/log-record.ts';

function freshDir(): string {
  return mkdtempSync(join(tmpdir(), 'aries-test-'));
}

function writeWal(walPath: string, records: LogRecord[]): void {
  writeFileSync(walPath, records.map((r) => JSON.stringify(r)).join('\n') + '\n');
}

async function recoverScenario() {
  const dir = freshDir();
  const scenario = await buildCrashScenario(dir);
  const log = new LogManager(scenario.walPath);
  const storage = new StorageEngine(log, { pagesFile: scenario.pagesPath });
  const recovery = new AriesRecovery(log, storage);
  const report = recovery.recover(scenario.walPath);
  return { dir, scenario, log, storage, report };
}

test('analysis: reconstructs txn table and DirtyPageTable from checkpoint', async () => {
  const { dir, scenario, report } = await recoverScenario();
  try {
    const { txnTable, dirtyPageTable, checkpointLSN } = report.analysis;
    assert.equal(checkpointLSN, scenario.checkpointLSN);

    // Winners wrote END before the crash, so only the 3 losers remain.
    assert.deepEqual(
      [...txnTable.keys()].sort((a, b) => a - b),
      [scenario.loserTxns.t3, scenario.loserTxns.t4, scenario.loserTxns.t5],
    );
    for (const entry of txnTable.values()) {
      assert.equal(entry.status, 'RUNNING');
    }

    // DPT covers all three pages; recLSNs match the first update to each.
    const wal = LogManager.readFile(scenario.walPath);
    const firstUpdate = (pageId: number) =>
      wal.find(
        (r) => (r.type === 'UPDATE' || r.type === 'CLR') && r.pageId === pageId,
      )!.lsn;
    assert.deepEqual([...dirtyPageTable.keys()].sort((a, b) => a - b), [1, 2, 3]);
    assert.equal(dirtyPageTable.get(1), firstUpdate(1));
    assert.equal(dirtyPageTable.get(2), firstUpdate(2));
    assert.equal(dirtyPageTable.get(3), firstUpdate(3));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('redo: repeats history, including loser updates, only where pageLSN lags', async () => {
  const { dir, scenario, report } = await recoverScenario();
  try {
    const wal = LogManager.readFile(scenario.walPath);
    const updatesOn = (pageId: number) =>
      wal.filter((r) => r.type === 'UPDATE' && r.pageId === pageId).map((r) => r.lsn);

    // Page 1 was flushed after its last update: nothing to redo there.
    for (const lsn of updatesOn(1)) {
      assert.ok(!report.redoneLSNs.includes(lsn), `page1 LSN ${lsn} must not be redone`);
    }
    // Pages 2 and 3 were lost with the buffer pool: every update replayed,
    // including the uncommitted ones (history is repeated).
    for (const lsn of [...updatesOn(2), ...updatesOn(3)]) {
      assert.ok(report.redoneLSNs.includes(lsn), `LSN ${lsn} must be redone`);
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('undo: rolls back the 3 losers with CLRs and persists the 2 winners', async () => {
  const { dir, scenario, storage, log, report } = await recoverScenario();
  try {
    const { t3, t4, t5 } = scenario.loserTxns;
    assert.deepEqual(report.undo.losers, [t3, t4, t5]);

    // T3 made 2 updates, T4 made 2, T5 made 1 -> 5 CLRs, 3 ENDs.
    assert.equal(report.undo.clrLSNs.length, 5);
    assert.equal(report.undo.endLSNs.length, 3);

    // Durable state matches the expectation exactly.
    for (const [pageId, slots] of Object.entries(scenario.expectedDisk)) {
      for (const [offset, value] of Object.entries(slots)) {
        assert.equal(
          storage.readDiskSlot(Number(pageId), Number(offset)),
          value,
          `page ${pageId} slot ${offset}`,
        );
      }
    }

    // The WAL file itself now carries the compensation records.
    const wal = LogManager.readFile(scenario.walPath);
    const clrs = wal.filter((r) => r.type === 'CLR');
    const ends = wal.filter(
      (r) => r.type === 'END' && [t3, t4, t5].includes(r.txnId),
    );
    assert.equal(clrs.length, 5);
    assert.equal(ends.length, 3);
    for (const clr of clrs) {
      assert.equal(clr.undoNextLSN !== undefined, true);
    }

    // Nothing left unflushed: no log-buffer leak after recovery.
    assert.equal(log.flushedUpTo, log.lastLSN);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('recovery is idempotent: a second restart undoes nothing new', async () => {
  const { dir, scenario } = await recoverScenario();
  try {
    const log2 = new LogManager(scenario.walPath);
    const storage2 = new StorageEngine(log2, { pagesFile: scenario.pagesPath });
    const report2 = new AriesRecovery(log2, storage2).recover(scenario.walPath);

    assert.equal(report2.analysis.txnTable.size, 0, 'no losers remain');
    assert.equal(report2.undo.clrLSNs.length, 0);
    assert.equal(report2.undo.endLSNs.length, 0);
    // The first recovery flushed everything, so every page's pageLSN is
    // current and Redo correctly has nothing to replay.
    assert.equal(report2.redoneLSNs.length, 0);
    for (const [pageId, slots] of Object.entries(scenario.expectedDisk)) {
      for (const [offset, value] of Object.entries(slots)) {
        assert.equal(storage2.readDiskSlot(Number(pageId), Number(offset)), value);
      }
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
test('undo follows CLR undoNextLSN and never re-undoes compensated work', () => {
  const dir = freshDir();
  try {
    const walPath = join(dir, 'wal.log');
    // Hand-crafted WAL: T1 updates a slot twice, aborts, undoes the second
    // update (CLR), then the system crashes mid-rollback. T2 commits but
    // crashes before writing END.
    const records: LogRecord[] = [
      { lsn: 1, prevLSN: 0, txnId: 1, type: 'BEGIN' },
      { lsn: 2, prevLSN: 1, txnId: 1, type: 'UPDATE', pageId: 7, offset: 0, beforeImage: 'orig', afterImage: 'v1' },
      { lsn: 3, prevLSN: 2, txnId: 1, type: 'UPDATE', pageId: 7, offset: 0, beforeImage: 'v1', afterImage: 'v2' },
      { lsn: 4, prevLSN: 3, txnId: 1, type: 'ABORT' },
      { lsn: 5, prevLSN: 4, txnId: 1, type: 'CLR', pageId: 7, offset: 0, beforeImage: 'v2', afterImage: 'v1', undoNextLSN: 2 },
      { lsn: 6, prevLSN: 0, txnId: 2, type: 'BEGIN' },
      { lsn: 7, prevLSN: 6, txnId: 2, type: 'UPDATE', pageId: 7, offset: 1, beforeImage: '', afterImage: 'committed' },
      { lsn: 8, prevLSN: 7, txnId: 2, type: 'COMMIT' },
    ];
    writeWal(walPath, records);

    const log = new LogManager(walPath);
    const storage = new StorageEngine(log);
    const report = new AriesRecovery(log, storage).recover(walPath);

    // T1 is ABORTING (loser); T2 is COMMITTING (winner, no undo needed).
    assert.equal(report.analysis.txnTable.get(1)?.status, 'ABORTING');
    assert.equal(report.analysis.txnTable.get(2)?.status, 'COMMITTING');
    assert.deepEqual(report.undo.losers, [1]);

    // Exactly one new CLR: undoing LSN 2 only. The CLR at LSN 5 is
    // followed via undoNextLSN, not re-undone.
    assert.equal(report.undo.clrLSNs.length, 1);
    const newClr = log.getRecord(report.undo.clrLSNs[0]);
    assert.equal(newClr.afterImage, 'orig');
    assert.equal(newClr.undoNextLSN, 1);

    assert.equal(storage.readDiskSlot(7, 0), 'orig');
    assert.equal(storage.readDiskSlot(7, 1), 'committed');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('recovery without any checkpoint scans the whole log', () => {
  const dir = freshDir();
  try {
    const walPath = join(dir, 'wal.log');
    const records: LogRecord[] = [
      { lsn: 1, prevLSN: 0, txnId: 1, type: 'BEGIN' },
      { lsn: 2, prevLSN: 1, txnId: 1, type: 'UPDATE', pageId: 1, offset: 0, beforeImage: '', afterImage: 'x' },
      { lsn: 3, prevLSN: 0, txnId: 2, type: 'BEGIN' },
      { lsn: 4, prevLSN: 3, txnId: 2, type: 'UPDATE', pageId: 1, offset: 1, beforeImage: '', afterImage: 'y' },
      { lsn: 5, prevLSN: 4, txnId: 2, type: 'COMMIT' },
      { lsn: 6, prevLSN: 5, txnId: 2, type: 'END' },
    ];
    writeWal(walPath, records);

    const log = new LogManager(walPath);
    const storage = new StorageEngine(log);
    const report = new AriesRecovery(log, storage).recover(walPath);

    assert.equal(report.analysis.checkpointLSN, 0);
    assert.deepEqual(report.undo.losers, [1]);
    assert.equal(storage.readDiskSlot(1, 0), '');
    assert.equal(storage.readDiskSlot(1, 1), 'y');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('empty WAL recovers to an empty database', () => {
  const dir = freshDir();
  try {
    const walPath = join(dir, 'wal.log');
    const log = new LogManager(walPath);
    const storage = new StorageEngine(log);
    const report = new AriesRecovery(log, storage).recover(walPath);
    assert.equal(report.analysis.txnTable.size, 0);
    assert.equal(report.analysis.dirtyPageTable.size, 0);
    assert.equal(report.redoneLSNs.length, 0);
    assert.equal(report.undo.clrLSNs.length, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
