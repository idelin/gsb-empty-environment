import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { readdirSync } from 'node:fs';
import { DiskStorage } from '../src/storage.ts';
import { makeEnv, reopenAfterCrash } from './helpers.ts';
import type { TestEnv } from './helpers.ts';

function pageFiles(env: TestEnv, snapshotId: number): string[] {
  return readdirSync(join(env.root, 'snapshots', `snapshot-${snapshotId}`))
    .filter((f) => f.startsWith('page-'))
    .sort();
}

function assertNoLeaks(env: TestEnv): void {
  const state = env.snapshots.debugState();
  assert.equal(state.active, false, 'no snapshot left in progress');
  assert.equal(state.stagedCount, 0, 'copy-on-write staging drained');
  assert.deepEqual(state.tmpFiles, [], 'no temporary files left behind');
  assert.equal(env.wal.bufferedCount, 0, 'WAL fully flushed');
}

function assertNoOpenTransactions(env: TestEnv): void {
  assert.equal(env.txns.activeCount, 0, 'no active transactions');
}

test('first snapshot is a full copy; manifests record baseLSN', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'a');
    env.txns.update(t1, 2, 'b');
    env.txns.commit(t1);

    const id = await env.snapshots.beginSnapshot();
    assert.equal(id, 1);
    const manifest = await env.snapshots.completeSnapshot();
    assert.equal(manifest.parent, null);
    assert.equal(manifest.baseLSN, env.wal.currentLSN);
    assert.deepEqual(Object.keys(manifest.pages).sort(), ['1', '2']);
    assert.deepEqual(pageFiles(env, 1), ['page-1.json', 'page-2.json']);
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('incremental snapshot copies only pages changed since the parent', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'a');
    env.txns.update(t1, 2, 'b');
    env.txns.update(t1, 3, 'c');
    env.txns.commit(t1);
    await env.snapshots.beginSnapshot();
    const m1 = await env.snapshots.completeSnapshot();

    const t2 = env.txns.begin();
    env.txns.update(t2, 2, 'b-changed'); // only page 2 changes
    env.txns.commit(t2);
    await env.snapshots.beginSnapshot();
    const m2 = await env.snapshots.completeSnapshot();

    assert.equal(m2.parent, 1);
    // Only page 2 gets a fresh file in snapshot-2; pages 1 and 3 are reused.
    assert.deepEqual(pageFiles(env, 2), ['page-2.json']);
    assert.equal(m2.pages['1'].file, m1.pages['1'].file);
    assert.equal(m2.pages['3'].file, m1.pages['3'].file);
    assert.notEqual(m2.pages['2'].file, m1.pages['2'].file);
    assert.ok(m2.pages['2'].file.startsWith('snapshot-2/'));
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('snapshot with zero changes since parent stores no page files', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'a');
    env.txns.commit(t1);
    await env.snapshots.beginSnapshot();
    await env.snapshots.completeSnapshot();

    await env.snapshots.beginSnapshot();
    const m2 = await env.snapshots.completeSnapshot();
    assert.deepEqual(pageFiles(env, 2), []);
    assert.equal(m2.pages['1'].file, 'snapshot-1/page-1.json');
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('snapshot of an empty database contains no pages', async () => {
  const env = makeEnv();
  try {
    await env.snapshots.beginSnapshot();
    const manifest = await env.snapshots.completeSnapshot();
    assert.deepEqual(manifest.pages, {});
    const target = new DiskStorage(join(env.root, 'restore-empty'));
    assert.equal(env.snapshots.restore(1, target), 0);
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('copy-on-write: writes during a snapshot do not leak into it', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'before');
    env.txns.update(t1, 2, 'untouched');
    env.txns.commit(t1);

    await env.snapshots.beginSnapshot();
    // Concurrent writer mutates pages while the snapshot is open.
    const t2 = env.txns.begin();
    env.txns.update(t2, 1, 'after-1');
    env.txns.update(t2, 1, 'after-2');
    env.txns.update(t2, 3, 'brand-new-page'); // created after baseLSN
    env.txns.commit(t2);

    const manifest = await env.snapshots.completeSnapshot();
    // Snapshot reflects the consistency point: old value, no new page.
    assert.deepEqual(Object.keys(manifest.pages).sort(), ['1', '2']);
    const target = new DiskStorage(join(env.root, 'restore'));
    env.snapshots.restore(1, target);
    assert.equal(target.readPage(1)!.data, 'before');
    assert.equal(target.readPage(2)!.data, 'untouched');
    assert.equal(target.readPage(3), null);
    // Live state is unaffected by the snapshot.
    assert.equal(env.pool.get(1).data, 'after-2');
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('interleaved async writers: snapshot is a consistent point-in-time image', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    for (let p = 1; p <= 10; p++) env.txns.update(t1, p, `gen0-page${p}`);
    env.txns.commit(t1);

    await env.snapshots.beginSnapshot();
    const baseLSN = env.wal.currentLSN;

    // 50 concurrent updates across 10 pages, yielding between each write.
    const writer = (async () => {
      const t2 = env.txns.begin();
      for (let i = 0; i < 50; i++) {
        env.txns.update(t2, (i % 10) + 1, `gen1-write${i}`);
        await new Promise((resolve) => setImmediate(resolve));
      }
      env.txns.commit(t2);
    })();

    const manifest = await env.snapshots.completeSnapshot();
    await writer;

    assert.equal(manifest.baseLSN, baseLSN);
    const target = new DiskStorage(join(env.root, 'restore'));
    env.snapshots.restore(manifest.id, target);
    for (let p = 1; p <= 10; p++) {
      assert.equal(target.readPage(p)!.data, `gen0-page${p}`, `page ${p} at baseLSN`);
    }
    // Live pages moved on: page 5 last written at i=44, page 6 at i=45.
    assert.equal(env.pool.get(5).data, 'gen1-write44');
    assert.equal(env.pool.get(6).data, 'gen1-write45');
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('concurrent snapshots are serialized and both stay consistent', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'v1');
    env.txns.commit(t1);

    const first = (async () => {
      await env.snapshots.beginSnapshot();
      await new Promise((resolve) => setTimeout(resolve, 20));
      return env.snapshots.completeSnapshot();
    })();
    // Give the first snapshot a head start, then open a second one.
    await new Promise((resolve) => setTimeout(resolve, 5));
    const second = (async () => {
      await env.snapshots.beginSnapshot();
      return env.snapshots.completeSnapshot();
    })();

    const [m1, m2] = await Promise.all([first, second]);
    assert.equal(m1.id, 1);
    assert.equal(m2.id, 2);
    assert.equal(m2.parent, 1);
    assert.equal(m2.pages['1'].file, m1.pages['1'].file); // unchanged -> reused
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('archive produces a self-contained bundle and keeps dependents restorable', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'a');
    env.txns.update(t1, 2, 'b');
    env.txns.commit(t1);
    await env.snapshots.beginSnapshot();
    await env.snapshots.completeSnapshot(); // snapshot 1: pages 1,2

    const t2 = env.txns.begin();
    env.txns.update(t2, 2, 'b2');
    env.txns.commit(t2);
    await env.snapshots.beginSnapshot();
    const m2 = await env.snapshots.completeSnapshot(); // snapshot 2 reuses page-1 from snapshot 1
    assert.equal(m2.pages['1'].file, 'snapshot-1/page-1.json');

    await env.snapshots.archiveSnapshot(1);
    assert.deepEqual(env.snapshots.listSnapshots(), [2]);
    assert.deepEqual(env.snapshots.listArchives(), [1]);

    // Dependent snapshot 2 had its shared reference re-homed.
    const m2After = env.snapshots.readManifest(2);
    assert.equal(m2After.pages['1'].file, 'snapshot-2/page-1.json');
    assert.equal(m2After.parent, null);

    // Both the archive and the dependent snapshot restore correctly.
    const fromArchive = new DiskStorage(join(env.root, 'r1'));
    env.snapshots.restore(1, fromArchive);
    assert.equal(fromArchive.readPage(1)!.data, 'a');
    assert.equal(fromArchive.readPage(2)!.data, 'b');
    const fromSnapshot = new DiskStorage(join(env.root, 'r2'));
    env.snapshots.restore(2, fromSnapshot);
    assert.equal(fromSnapshot.readPage(1)!.data, 'a');
    assert.equal(fromSnapshot.readPage(2)!.data, 'b2');
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('prune keeps the newest snapshots and archives the rest', async () => {
  const env = makeEnv();
  try {
    for (let round = 1; round <= 5; round++) {
      const t = env.txns.begin();
      env.txns.update(t, 1, `value-${round}`);
      env.txns.commit(t);
      await env.snapshots.beginSnapshot();
      await env.snapshots.completeSnapshot();
    }
    assert.deepEqual(env.snapshots.listSnapshots(), [1, 2, 3, 4, 5]);

    const archived = await env.snapshots.prune(2);
    assert.deepEqual(archived, [1, 2, 3]);
    assert.deepEqual(env.snapshots.listSnapshots(), [4, 5]);
    assert.deepEqual(env.snapshots.listArchives(), [1, 2, 3]);

    // Every generation remains restorable: live snapshots and archives.
    for (let round = 1; round <= 5; round++) {
      const target = new DiskStorage(join(env.root, `restore-${round}`));
      env.snapshots.restore(round, target);
      assert.equal(target.readPage(1)!.data, `value-${round}`);
    }
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('restore of an unknown snapshot fails loudly', async () => {
  const env = makeEnv();
  try {
    const target = new DiskStorage(join(env.root, 'nowhere'));
    assert.throws(() => env.snapshots.restore(99, target), /not found/);
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('abortSnapshot leaves no files and releases the lifecycle lock', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'x');
    env.txns.commit(t1);

    await env.snapshots.beginSnapshot();
    const t2 = env.txns.begin();
    env.txns.update(t2, 1, 'y'); // staged via copy-on-write
    assert.equal(env.snapshots.stagedCount, 1);
    await env.snapshots.abortSnapshot();
    assert.equal(env.snapshots.hasActiveSnapshot, false);
    assert.deepEqual(env.snapshots.listSnapshots(), []);
    env.txns.abort(t2); // end the still-open writer before leak checks

    // Lock was released: a new snapshot can start immediately.
    await env.snapshots.beginSnapshot();
    const manifest = await env.snapshots.completeSnapshot();
    assert.equal(manifest.id, 1);
    assertNoLeaks(env);
  } finally {
    env.cleanup();
  }
});

test('snapshot ids never collide with archived snapshots', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'one');
    env.txns.commit(t1);
    await env.snapshots.beginSnapshot();
    await env.snapshots.completeSnapshot();
    await env.snapshots.archiveSnapshot(1);
    assert.deepEqual(env.snapshots.listSnapshots(), []);

    await env.snapshots.beginSnapshot();
    const manifest = await env.snapshots.completeSnapshot();
    assert.equal(manifest.id, 2, 'id continues past archived snapshots');
    assertNoLeaks(env);
    assertNoOpenTransactions(env);
  } finally {
    env.cleanup();
  }
});

test('full cycle: snapshot before crash, recover, restore archived baseline', async () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'baseline');
    env.txns.commit(t1);
    env.pool.flushAll();
    await env.snapshots.beginSnapshot();
    await env.snapshots.completeSnapshot();
    await env.snapshots.archiveSnapshot(1);

    // More committed work, then an uncommitted write and a crash.
    const t2 = env.txns.begin();
    env.txns.update(t2, 1, 'committed-later');
    env.txns.commit(t2);
    const t3 = env.txns.begin();
    env.txns.update(t3, 1, 'uncommitted');
    env.wal.flush();

    const { recovery } = reopenAfterCrash(env);
    recovery.recover();
    assert.equal(env.storage.readPage(1)!.data, 'committed-later');

    // The archived baseline is still intact and restorable elsewhere.
    const baseline = new DiskStorage(join(env.root, 'baseline'));
    env.snapshots.restore(1, baseline);
    assert.equal(baseline.readPage(1)!.data, 'baseline');
    assertNoLeaks(env);
    // t3 was left uncommitted by the simulated crash; the recovered
    // database instance owns its rollback, verified above.
  } finally {
    env.cleanup();
  }
});
