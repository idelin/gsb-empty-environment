import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/engine.ts';
import { LogManager } from '../src/wal.ts';
import { DiskStorage } from '../src/storage.ts';

function makeDir(): string {
  return mkdtempSync(join(tmpdir(), 'aries-'));
}

function diskValues(dir: string): Map<number, number> {
  const pages = new DiskStorage(join(dir, 'data.json')).load();
  return new Map([...pages].map(([id, img]) => [id, img.value]));
}

/**
 * Acceptance scenario: 2 committed (T1, T2) + 3 uncommitted (T3, T4, T5)
 * transactions, a fuzzy checkpoint in the middle, then a crash. Recovery must
 * run Analysis/Redo/Undo, persist committed data and roll back the losers.
 */
test('ARIES three-phase recovery after crash post-checkpoint', () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);

    const t1 = db.begin();          // committed
    db.write(t1, 1, 10);
    db.write(t1, 2, 20);
    db.commit(t1);

    const t2 = db.begin();          // committed (commits after checkpoint)
    db.write(t2, 3, 30);

    const t3 = db.begin();          // loser: overwrites committed page 1
    db.write(t3, 1, 100);

    const ckpt = db.checkpoint();
    assert.ok(ckpt.tt.some(([id]) => id === t2));
    assert.ok(ckpt.tt.some(([id]) => id === t3));

    db.write(t2, 4, 40);
    db.commit(t2);

    const t4 = db.begin();          // loser
    db.write(t4, 5, 50);

    const t5 = db.begin();          // loser with two updates
    db.write(t5, 2, 200);
    db.write(t5, 6, 60);

    db.wal.flush();                 // everything so far is durable
    db.pool.flushPage(1);           // steal: uncommitted T3 write hits disk
    db.pool.flushPage(3);

    db.write(t5, 7, 70);            // unflushed tail: lost in the crash
    assert.ok(db.wal.bufferedCount > 0);

    db.crash();
    assert.equal(db.transactionTable.size, 0);
    assert.equal(db.wal.bufferedCount, 0);

    const report = db.recover();

    // Analysis reconstructed exactly the three losers.
    assert.deepEqual(report.undo.loserTxIds, [t3, t4, t5]);
    const dpt = report.analysis.dirtyPageTable;
    for (const pageId of [1, 2, 3, 4, 5, 6]) assert.ok(dpt.has(pageId), `dpt has page ${pageId}`);
    assert.ok(!dpt.has(7), 'unflushed page 7 never enters the DPT');

    // Undo emitted one CLR per loser update (T3:1, T4:1, T5:2).
    assert.equal(report.undo.clrCount, 4);

    // Committed data persisted, uncommitted data rolled back, on disk.
    const values = diskValues(dir);
    assert.equal(values.get(1), 10);   // T3 undone
    assert.equal(values.get(2), 20);   // T5 undone
    assert.equal(values.get(3), 30);   // T2 committed
    assert.equal(values.get(4), 40);   // T2 committed, redone
    assert.equal(values.get(5), 0);    // T4 undone
    assert.equal(values.get(6), 0);    // T5 undone
    assert.equal(values.get(7), undefined); // lost tail never existed

    // WAL now contains CLRs and end records for every loser.
    const log = new LogManager(join(dir, 'wal.log')).scan();
    const clrs = log.filter((r) => r.type === 'clr');
    assert.equal(clrs.length, 4);
    const ends = log.filter((r) => r.type === 'end').map((r) => (r as { txId: number }).txId);
    for (const txId of [t1, t2, t3, t4, t5]) assert.ok(ends.includes(txId), `end for T${txId}`);

    // No leaks: transaction table drained, log buffer flushed, pages clean.
    assert.equal(db.transactionTable.size, 0);
    assert.equal(db.wal.bufferedCount, 0);
    assert.equal(db.pool.dirtyCount, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('recovery is idempotent: a second crash during undo changes nothing', () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const t1 = db.begin();
    db.write(t1, 1, 1);
    db.commit(t1);
    const t2 = db.begin();
    db.write(t2, 1, 2);
    db.write(t2, 2, 9);
    db.wal.flush();
    db.crash();
    const first = db.recover();
    assert.equal(first.undo.clrCount, 2);
    const afterFirst = diskValues(dir);

    // Crash again right after recovery and recover once more.
    db.crash();
    const second = db.recover();
    assert.deepEqual(diskValues(dir), afterFirst);
    assert.equal(second.undo.clrCount, 0, 'CLRs are never undone twice');
    assert.equal(afterFirst.get(1), 1);
    assert.equal(afterFirst.get(2), 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('normal-processing abort rolls back via CLRs and ends the transaction', () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const t1 = db.begin();
    db.write(t1, 1, 5);
    db.commit(t1);
    const t2 = db.begin();
    db.write(t2, 1, 50);
    db.write(t2, 2, 60);
    db.abort(t2);

    assert.equal(db.pool.readPage(1).value, 5);
    assert.equal(db.pool.readPage(2).value, 0);
    assert.equal(db.transactionTable.size, 0);

    const log = new LogManager(join(dir, 'wal.log')).scanIncludingBuffer();
    assert.equal(log.filter((r) => r.type === 'clr').length, 2);
    assert.ok(log.some((r) => r.type === 'end' && (r as { txId: number }).txId === t2));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('redo respects pageLSN and does not repeat already-persistent updates', () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const t1 = db.begin();
    db.write(t1, 1, 42);
    db.commit(t1);
    db.wal.flush();
    db.pool.flushAll(); // page 1 durable with pageLSN of the update
    db.crash();
    const report = db.recover();
    assert.equal(report.redo.redoneCount, 0, 'update already on disk is skipped');
    assert.ok(report.redo.skippedCount > 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
