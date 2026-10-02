import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LSMEngine } from '../src/lsm/engine.ts';
import { SnapshotManager } from '../src/backup/snapshot.ts';
import { decodeSSTable } from '../src/lsm/sstable.ts';

function makeWorkspace() {
  const root = mkdtempSync(join(tmpdir(), 'lsm-incr-test-'));
  const engine = new LSMEngine(join(root, 'data'), { flushThreshold: 100 });
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

function readDirTables(backups: SnapshotManager, id: string, file: 'MANIFEST.json' | 'DELTA.json') {
  const dir = backups.snapshotDir(id);
  const manifest = JSON.parse(readFileSync(join(dir, file), 'utf8'));
  const names: string[] = manifest.kind === 'full' ? manifest.files.map((f: { name: string }) => f.name)
    : manifest.added.map((f: { name: string }) => f.name);
  return names.map((n) => decodeSSTable(readFileSync(join(dir, n))).entries);
}

test('incremental export contains only the SSTables added since the baseline', () => {
  const { engine, backups, cleanup } = makeWorkspace();
  try {
    writeBatch(engine, 'a', 300);
    const base = backups.createSnapshot({ checksum: true });
    const baseNames = new Set(base.files.map((f) => f.name));

    writeBatch(engine, 'b', 250); // new SSTables appear
    const liveNames = new Set(engine.listSSTables().map((t) => t.name));

    const incr = backups.exportIncremental(base.id, { checksum: true });
    assert.equal(incr.base, base.id);
    assert.ok(incr.added.length > 0);
    assert.equal(incr.removed.length, 0);

    // Delta = live set minus baseline, exactly.
    const expectedAdded = [...liveNames].filter((n) => !baseNames.has(n)).sort();
    assert.deepEqual(incr.added.map((f) => f.name).sort(), expectedAdded);

    // Exported files are hard links of the live SSTables (same inode).
    for (const f of incr.added) {
      const inoExport = statSync(join(backups.snapshotDir(incr.id), f.name)).ino;
      const inoLive = statSync(join(engine.dir, f.name)).ino;
      assert.equal(inoExport, inoLive);
    }

    assert.equal(backups.verify(incr.id).ok, true);
  } finally {
    cleanup();
  }
});

test('baseline snapshot + incremental export reconstruct the full dataset', () => {
  const { engine, backups, cleanup } = makeWorkspace();
  try {
    const partA = writeBatch(engine, 'a', 300);
    const base = backups.createSnapshot({ checksum: true });
    const partB = writeBatch(engine, 'b', 250);
    const incr = backups.exportIncremental(base.id, { checksum: true });

    const restored = new Map<string, string>();
    for (const entries of [...readDirTables(backups, base.id, 'MANIFEST.json'),
                          ...readDirTables(backups, incr.id, 'DELTA.json')]) {
      for (const e of entries) if (e.value !== null) restored.set(e.key, e.value);
    }
    const expected = new Map([...partA, ...partB]);
    assert.deepEqual(restored, expected);
    assert.deepEqual(restored, engine.readAll());
  } finally {
    cleanup();
  }
});

test('incremental after compaction links merged tables and records removals', () => {
  const { engine, backups, cleanup } = makeWorkspace();
  try {
    writeBatch(engine, 'a', 300);
    const base = backups.createSnapshot({ checksum: true });

    writeBatch(engine, 'b', 250);
    const compact = engine.compact();
    assert.ok(compact);

    const incr = backups.exportIncremental(base.id, { checksum: true });
    // The merged table is the only addition; all pre-compaction tables are removals.
    assert.deepEqual(incr.added.map((f) => f.name), [compact.merged.name]);
    // "removed" tracks baseline files no longer live (all were compacted away).
    assert.deepEqual([...incr.removed].sort(), base.files.map((f) => f.name).sort());
    assert.ok(compact.removed.includes(incr.removed[0]));

    // Baseline + delta still reconstruct everything, even though compaction
    // unlinked the original files from the live directory.
    assert.equal(backups.verify(base.id).ok, true);
    assert.equal(backups.verify(incr.id).ok, true);
    const restored = new Map<string, string>();
    for (const entries of readDirTables(backups, incr.id, 'DELTA.json')) {
      for (const e of entries) if (e.value !== null) restored.set(e.key, e.value);
    }
    assert.deepEqual(restored, engine.readAll());
  } finally {
    cleanup();
  }
});

test('diffSnapshots reports added/removed tables between two snapshots', () => {
  const { engine, backups, cleanup } = makeWorkspace();
  try {
    writeBatch(engine, 'a', 200);
    const snapA = backups.createSnapshot();
    writeBatch(engine, 'b', 200);
    const snapB = backups.createSnapshot();

    const diff = backups.diffSnapshots(snapA.id, snapB.id);
    const namesA = new Set(snapA.files.map((f) => f.name));
    const namesB = new Set(snapB.files.map((f) => f.name));
    assert.deepEqual(diff.added.sort(), [...namesB].filter((n) => !namesA.has(n)).sort());
    assert.deepEqual(diff.removed, []);
  } finally {
    cleanup();
  }
});
