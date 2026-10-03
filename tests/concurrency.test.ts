import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/engine.ts';
import { IncrementalSnapshotManager } from '../src/snapshot.ts';
import { DiskStorage } from '../src/storage.ts';
import { LogManager } from '../src/wal.ts';

const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

function makeDir(): string {
  return mkdtempSync(join(tmpdir(), 'conc-'));
}

test('copy-on-write keeps an in-flight snapshot pinned to its horizon LSN', async () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);
    for (let i = 0; i < 20; i++) {
      const tx = db.begin();
      db.write(tx, i, i);
      db.commit(tx);
    }
    const horizon = db.wal.flushedLSN;

    // Concurrent writer flips every page mid-snapshot, after the first batch.
    const snapshotPromise = snaps.createSnapshot({
      yieldEvery: 1,
      onBatch: (captured) => {
        if (captured !== 1) return;
        for (let i = 0; i < 20; i++) {
          const tx = db.begin();
          db.write(tx, i, 1000 + i);
          db.commit(tx);
        }
      },
    });
    const meta = await snapshotPromise;
    assert.equal(meta.horizonLSN, horizon);

    const restored = snaps.restore(meta.id);
    for (let i = 0; i < 20; i++) {
      assert.equal(restored.get(i)?.value, i, `page ${i} pinned to pre-write image`);
      assert.ok((restored.get(i)?.pageLSN ?? 0) <= horizon);
    }
    // Live pool moved on; the snapshot did not.
    assert.equal(db.pool.readPage(0).value, 1000);
    assert.equal(snaps.pendingCaptureCount, 0);
    assert.equal(db.pool.cowHookCount, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('concurrent transactions with interleaved snapshots: consistency and no leaks', async () => {
  const dir = makeDir();
  try {
    const db = new Database(dir);
    const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);
    const expected = new Map<number, number>();
    const workers = 8;
    const txnsPerWorker = 25;

    const runWorker = async (workerId: number): Promise<void> => {
      for (let seq = 0; seq < txnsPerWorker; seq++) {
        const pageId = workerId * 10000 + seq;
        const tx = db.begin();
        db.write(tx, pageId, workerId * 1000 + seq);
        if (seq % 5 === 4) {
          db.abort(tx); // every 5th transaction aborts
        } else {
          db.commit(tx);
          expected.set(pageId, workerId * 1000 + seq);
        }
        await tick();
      }
    };

    const snapshotter = async (): Promise<void> => {
      for (let i = 0; i < 6; i++) {
        await snaps.createSnapshot({ yieldEvery: 3 });
        await tick();
      }
    };

    await Promise.all([
      ...Array.from({ length: workers }, (_, id) => runWorker(id)),
      snapshotter(),
    ]);

    // Every snapshot in the chain is internally consistent with its horizon.
    for (const meta of snaps.snapshots) {
      const restored = snaps.restore(meta.id);
      for (const image of restored.values()) {
        assert.ok(image.pageLSN <= meta.horizonLSN, 'no page newer than the horizon leaked in');
      }
    }

    db.wal.flush();
    db.crash();
    const report = db.recover();
    assert.equal(report.undo.loserTxIds.length, 0, 'no losers: all txns ended before the crash');

    const pages = new DiskStorage(join(dir, 'data.json')).load();
    assert.equal(pages.size, workers * txnsPerWorker);
    for (let workerId = 0; workerId < workers; workerId++) {
      for (let seq = 0; seq < txnsPerWorker; seq++) {
        const pageId = workerId * 10000 + seq;
        const want = expected.get(pageId) ?? 0;
        assert.equal(pages.get(pageId)?.value ?? 0, want, `page ${pageId}`);
      }
    }

    // Strict leak assertions.
    assert.equal(db.transactionTable.size, 0, 'transaction table drained');
    assert.equal(db.wal.bufferedCount, 0, 'log buffer fully flushed');
    assert.equal(db.pool.dirtyCount, 0, 'no dirty pages left behind');
    assert.equal(snaps.isCapturing, false);
    assert.equal(snaps.pendingCaptureCount, 0, 'COW staging area released');
    assert.equal(db.pool.cowHookCount, 0, 'COW hooks unregistered');
    const archiveFiles = readdirSync(join(dir, 'archive'));
    assert.ok(archiveFiles.every((f) => !f.endsWith('.tmp')), 'no leftover temp files');

    // Every transaction in the WAL reached an end record.
    const log = new LogManager(join(dir, 'wal.log')).scan();
    const begun = new Set<number>();
    const ended = new Set<number>();
    for (const rec of log) {
      if (rec.type === 'checkpoint') continue;
      if (rec.type === 'begin') begun.add(rec.txId);
      if (rec.type === 'end') ended.add(rec.txId);
    }
    assert.deepEqual([...begun].sort((a, b) => a - b), [...ended].sort((a, b) => a - b));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
