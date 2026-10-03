import { test } from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, rmSync, unlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/engine.ts';
import { IncrementalSnapshotManager } from '../src/snapshot.ts';
import { DiskStorage } from '../src/storage.ts';
import { LogManager } from '../src/wal.ts';

function makeDir(): string {
  return mkdtempSync(join(tmpdir(), 'snap-'));
}

function committedPage(db: Database, pageId: number, value: number): void {
  const tx = db.begin();
  db.write(tx, pageId, value);
  db.commit(tx);
}

test('first snapshot is a full base; later snapshots are incremental deltas', async () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);
    committedPage(db, 1, 10);
    committedPage(db, 2, 20);
    const s1 = await snaps.createSnapshot();
    assert.equal(s1.parentId, null);
    assert.equal(s1.pageCount, 2);
    assert.equal(s1.baseLSN, 0);
    committedPage(db, 2, 22);
    committedPage(db, 3, 30);
    const s2 = await snaps.createSnapshot();
    assert.equal(s2.parentId, s1.id);
    assert.equal(s2.pageCount, 2, 'delta holds only pages changed since the base');
    assert.equal(s2.baseLSN, s1.horizonLSN);
    const restored = snaps.restore();
    assert.equal(restored.get(1)?.value, 10);
    assert.equal(restored.get(2)?.value, 22, 'latest image wins across the chain');
    assert.equal(restored.get(3)?.value, 30);
    const atS1 = snaps.restore(s1.id);
    assert.equal(atS1.get(2)?.value, 20);
    assert.equal(atS1.has(3), false);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('empty delta when nothing changed between snapshots', async () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);
    committedPage(db, 1, 1);
    await snaps.createSnapshot();
    const s2 = await snaps.createSnapshot();
    assert.equal(s2.pageCount, 0);
    assert.equal(snaps.restore().get(1)?.value, 1);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('repeated updates between snapshots capture only the newest image', async () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);
    await snaps.createSnapshot();
    for (let i = 1; i <= 50; i++) committedPage(db, 7, i);
    const s = await snaps.createSnapshot();
    assert.equal(s.pageCount, 1);
    assert.equal(snaps.restore().get(7)?.value, 50);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('broken chain and unknown snapshot ids raise errors', async () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);
    committedPage(db, 1, 1);
    const s1 = await snaps.createSnapshot();
    committedPage(db, 2, 2);
    await snaps.createSnapshot();
    assert.throws(() => snaps.restore(999), /unknown snapshot/);
    unlinkSync(join(dir, 'archive', s1.file));
    assert.throws(() => snaps.restore(), /missing delta file/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('concurrent snapshot creation is rejected and state stays clean', async () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);
    for (let i = 0; i < 10; i++) committedPage(db, i, i);
    const first = snaps.createSnapshot({ yieldEvery: 1 });
    await assert.rejects(snaps.createSnapshot(), /already in progress/);
    await first;
    assert.equal(snaps.isCapturing, false);
    assert.equal(snaps.pendingCaptureCount, 0);
    assert.equal(db.pool.cowHookCount, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('checkpoint archiving prunes the WAL and snapshot+tail recover fully', async () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);
    committedPage(db, 1, 100);
    committedPage(db, 2, 200);
    const ckpt = db.checkpoint();
    committedPage(db, 3, 300);
    const loser = db.begin();
    db.write(loser, 1, 999);
    db.wal.flush();
    const keepFrom = db.oldestActiveTxnBeginLSN() ?? ckpt.lsn + 1;
    const meta = await snaps.archiveCheckpoint(ckpt.lsn, keepFrom);
    assert.ok(meta.pageCount >= 2);
    assert.ok(existsSync(join(dir, 'archive', `wal-upto-${ckpt.lsn}.log`)));
    // Committed after archiving: lives only in the live WAL tail.
    committedPage(db, 4, 400);
    db.wal.flush();
    const liveLog = new LogManager(join(dir, 'wal.log')).scan();
    assert.ok(liveLog.every((r) => r.lsn >= keepFrom), 'live WAL pruned at the safe horizon');
    assert.ok(liveLog.every((r) => r.lsn > ckpt.lsn), 'archived prefix fully pruned');
    assert.ok(liveLog.some((r) => r.type === 'update' && (r as { pageId: number }).pageId === 4));
    db.crash();
    snaps.restoreIntoStorage(db.storage);
    const db2 = new Database(dir);
    const report = db2.recover();
    assert.deepEqual(report.undo.loserTxIds, [loser]);
    const pages = new DiskStorage(join(dir, 'data.json')).load();
    assert.equal(pages.get(1)?.value, 100, 'loser rolled back to snapshot value');
    assert.equal(pages.get(2)?.value, 200);
    assert.equal(pages.get(3)?.value, 300, 'committed pre-archive write restored from snapshot');
    assert.equal(pages.get(4)?.value, 400, 'committed post-archive write redone from WAL tail');
    assert.equal(db2.transactionTable.size, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
