import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync, openSync, writeSync, closeSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LSMEngine } from '../src/lsm/engine.ts';
import { SnapshotManager } from '../src/backup/snapshot.ts';
import { decodeSSTable } from '../src/lsm/sstable.ts';

function makeWorkspace() {
  const root = mkdtempSync(join(tmpdir(), 'lsm-snap-test-'));
  const engine = new LSMEngine(join(root, 'data'), { flushThreshold: 200 });
  const backups = new SnapshotManager(engine, join(root, 'backups'));
  return { root, engine, backups, cleanup: () => rmSync(root, { recursive: true, force: true }) };
}

function writeBatch(engine: LSMEngine, prefix: string, count: number, start = 0): Map<string, string> {
  const written = new Map<string, string>();
  for (let i = start; i < start + count; i++) {
    const k = `${prefix}-${String(i).padStart(6, '0')}`;
    const v = `value-of-${k}`;
    engine.put(k, v);
    written.set(k, v);
  }
  engine.flush();
  return written;
}

test('snapshot contains hard links to every live SSTable (same inode, zero copy)', () => {
  const { engine, backups, cleanup } = makeWorkspace();
  try {
    writeBatch(engine, 'k', 1000); // ~5 SSTables at threshold 200
    const live = engine.listSSTables();
    assert.ok(live.length >= 3, `expected several SSTables, got ${live.length}`);

    const manifest = backups.createSnapshot({ checksum: true });
    assert.equal(manifest.files.length, live.length);

    for (const f of manifest.files) {
      const snapPath = join(backups.snapshotDir(manifest.id), f.name);
      const srcPath = join(engine.dir, f.name);
      assert.ok(existsSync(snapPath), `${f.name} missing from snapshot`);
      const snapStat = statSync(snapPath);
      const srcStat = statSync(srcPath);
      assert.equal(snapStat.ino, srcStat.ino, 'snapshot file must share the source inode (hard link)');
      assert.ok(snapStat.nlink >= 2, 'hard link must raise the link count');
    }
  } finally {
    cleanup();
  }
});

test('snapshot survives heavy writes + compaction deleting original files', () => {
  const { engine, backups, cleanup } = makeWorkspace();
  try {
    const expected = writeBatch(engine, 'k', 1000);
    const liveBefore = engine.listSSTables();
    const manifest = backups.createSnapshot({ checksum: true });

    // Foreground keeps writing densely, then compaction unlinks old files.
    writeBatch(engine, 'k', 1000, 1000);
    const compact = engine.compact();
    assert.ok(compact, 'compaction should have run');
    assert.ok(compact.removed.length >= liveBefore.length);

    // The original SSTable files are gone from the live directory...
    for (const t of liveBefore) {
      assert.ok(!existsSync(t.path), `${t.name} should have been deleted by compaction`);
    }

    // ...but the snapshot directory is completely unaffected.
    const report = backups.verify(manifest.id);
    assert.deepEqual(report.errors, []);
    assert.equal(report.ok, true);
    assert.equal(report.checked, manifest.files.length);

    // And the snapshot still decodes to the exact pre-compaction dataset.
    const restored = new Map<string, string>();
    for (const f of manifest.files) {
      const { entries } = decodeSSTable(readFileSync(join(backups.snapshotDir(manifest.id), f.name)));
      for (const e of entries) if (e.value !== null) restored.set(e.key, e.value);
    }
    assert.deepEqual(restored, expected);
  } finally {
    cleanup();
  }
});

test('snapshot creation is millisecond-fast and copies zero data bytes', () => {
  const { engine, backups, cleanup } = makeWorkspace();
  try {
    writeBatch(engine, 'big', 5000);
    const live = engine.listSSTables();
    const liveBytes = live.reduce((n, t) => n + t.size, 0);

    const start = performance.now();
    const manifest = backups.createSnapshot();
    const elapsedMs = performance.now() - start;

    assert.equal(manifest.files.length, live.length);
    // Zero-copy proof: every snapshot entry IS the same inode as the source.
    for (const f of manifest.files) {
      assert.equal(statSync(join(backups.snapshotDir(manifest.id), f.name)).ino,
                   statSync(join(engine.dir, f.name)).ino);
    }
    // Millisecond-scale regardless of data volume (25 tables here).
    assert.ok(elapsedMs < 1000, `snapshot took ${elapsedMs.toFixed(1)}ms for ${liveBytes} bytes`);
    console.log(`    snapshot of ${live.length} SSTables / ${liveBytes} bytes took ${elapsedMs.toFixed(2)}ms`);
  } finally {
    cleanup();
  }
});

test('verify detects tampering with a snapshot file', () => {
  const { engine, backups, cleanup } = makeWorkspace();
  try {
    writeBatch(engine, 'k', 500);
    const manifest = backups.createSnapshot({ checksum: true });
    engine.compact(); // originals unlinked; snapshot now owns the only links

    const victim = join(backups.snapshotDir(manifest.id), manifest.files[0].name);
    const fd = openSync(victim, 'r+');
    writeSync(fd, Buffer.from('X'), 0, 1, 10);
    closeSync(fd);

    const report = backups.verify(manifest.id);
    assert.equal(report.ok, false);
    assert.ok(report.errors.some((e) => e.includes('sha256 mismatch') || e.includes('size mismatch')));
  } finally {
    cleanup();
  }
});
