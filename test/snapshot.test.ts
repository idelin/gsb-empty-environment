import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { LsmEngine } from '../src/lsm/engine.ts';
import { SnapshotExporter } from '../src/backup/exporter.ts';

let workDir: string;
let dataDir: string;
let engine: LsmEngine;
let exporter: SnapshotExporter;

beforeEach(() => {
  workDir = fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-snapshot-test-'));
  dataDir = path.join(workDir, 'data');
  engine = LsmEngine.open(dataDir, { flushThreshold: 100, compactThreshold: 4 });
  exporter = new SnapshotExporter(engine);
});

afterEach(() => {
  fs.rmSync(workDir, { recursive: true, force: true });
});

function writeBatch(prefix: string, count: number): void {
  for (let i = 0; i < count; i++) {
    engine.put(`${prefix}-${String(i).padStart(6, '0')}`, `value-of-${prefix}-${i}`);
  }
}

function snapshotFiles(dir: string): string[] {
  return fs.readdirSync(dir).filter((f) => f.endsWith('.sst')).sort();
}

test('full snapshot hard-links every live SSTable (zero-copy: same inode)', () => {
  writeBatch('a', 250); // -> 2 flushes + 50 entries in memtable
  engine.flush();
  const live = engine.listSSTables();
  assert.equal(live.length, 3);

  const snapDir = path.join(workDir, 'snap-full');
  const started = performance.now();
  const manifest = exporter.exportFull(snapDir);
  const elapsedMs = performance.now() - started;

  // Manifest records every live table.
  assert.deepEqual(
    manifest.tables.map((t) => t.file).sort(),
    live.map((t) => t.file).sort(),
  );
  assert.equal(manifest.kind, 'full');

  // Every snapshot entry is a hard link: identical inode, link count >= 2.
  for (const table of live) {
    const srcStat = fs.statSync(path.join(dataDir, table.file));
    const dstStat = fs.statSync(path.join(snapDir, table.file));
    assert.equal(dstStat.ino, srcStat.ino, `${table.file} must share an inode (zero-copy)`);
    assert.ok(srcStat.nlink >= 2, `${table.file} link count should be >= 2`);
  }

  // Snapshotting is millisecond-scale even for several tables.
  assert.ok(elapsedMs < 100, `snapshot took ${elapsedMs.toFixed(2)}ms, expected < 100ms`);

  assert.deepEqual(SnapshotExporter.verify(snapDir), []);
});

test('snapshot is unaffected by subsequent heavy writes and compaction deleting old files', () => {
  writeBatch('gen0', 300);
  engine.flush();
  const baseline = engine.listSSTables();
  const snapDir = path.join(workDir, 'snap-stable');
  exporter.exportFull(snapDir);

  // Dense foreground writes: 20 more generations, forcing flushes and
  // repeated compactions that unlink every baseline SSTable in the data dir.
  for (let g = 1; g <= 20; g++) writeBatch(`gen${g}`, 100);
  engine.flush();
  engine.compact();

  // The engine's own names for the baseline files are gone...
  for (const table of baseline) {
    assert.ok(!fs.existsSync(path.join(dataDir, table.file)), `${table.file} should be compacted away`);
  }
  // ...yet the snapshot directory still holds every byte, now as sole owner.
  assert.deepEqual(snapshotFiles(snapDir), baseline.map((t) => t.file).sort());
  for (const table of baseline) {
    const stat = fs.statSync(path.join(snapDir, table.file));
    assert.equal(stat.nlink, 1, `${table.file} should now be owned solely by the snapshot`);
  }

  // Data integrity: checksums still match the manifest taken earlier.
  assert.deepEqual(SnapshotExporter.verify(snapDir), []);

  // The live engine is also fully consistent after compaction.
  assert.equal(engine.get('gen0-000000'), 'value-of-gen0-0');
  assert.equal(engine.get('gen20-000099'), 'value-of-gen20-99');
});

test('incremental snapshot exports only new SSTables; full + delta reconstruct the live set', () => {
  // High compaction threshold: base tables must stay live so the incremental
  // run can observe "already archived" tables and skip them.
  engine = LsmEngine.open(dataDir, { flushThreshold: 100, compactThreshold: 20 });
  exporter = new SnapshotExporter(engine);

  writeBatch('base', 300);
  engine.flush();
  const fullDir = path.join(workDir, 'snap-1-full');
  const fullManifest = exporter.exportFull(fullDir);
  const baseCount = fullManifest.tables.length;
  assert.ok(baseCount >= 3);

  // More writes -> new SSTables appear alongside the old ones.
  writeBatch('delta1', 150);
  engine.flush();
  writeBatch('delta2', 120);
  engine.flush();

  const incDir = path.join(workDir, 'snap-2-inc');
  const result = exporter.exportIncremental(incDir, path.join(fullDir, 'MANIFEST.json'));

  // Delta contains exactly the tables created after the base snapshot.
  const liveNow = engine.listSSTables().map((t) => t.file).sort();
  const expectedNew = liveNow.filter((f) => !fullManifest.tables.some((t) => t.file === f));
  assert.deepEqual(result.added.map((t) => t.file).sort(), expectedNew);
  assert.equal(result.skipped.length, baseCount);
  assert.deepEqual(snapshotFiles(incDir), expectedNew);
  assert.equal(result.manifest.kind, 'incremental');

  // Union of full + incremental links == current live set, all zero-copy.
  const union = [...snapshotFiles(fullDir), ...snapshotFiles(incDir)].sort();
  assert.deepEqual(union, liveNow);
  for (const dir of [fullDir, incDir]) {
    for (const file of snapshotFiles(dir)) {
      assert.equal(
        fs.statSync(path.join(dir, file)).ino,
        fs.statSync(path.join(dataDir, file)).ino,
      );
    }
  }

  assert.deepEqual(SnapshotExporter.verify(fullDir), []);
  assert.deepEqual(SnapshotExporter.verify(incDir), []);
});

test('verify detects tampering with snapshot content', () => {
  writeBatch('t', 120);
  engine.flush();
  const snapDir = path.join(workDir, 'snap-tamper');
  const manifest = exporter.exportFull(snapDir);

  // Replace one linked file with corrupted content (unlink + rewrite, so the
  // engine's own inode is untouched, exactly like a corrupted archive copy).
  const victim = manifest.tables[0].file;
  fs.unlinkSync(path.join(snapDir, victim));
  fs.writeFileSync(path.join(snapDir, victim), 'corrupted-bytes');

  const problems = SnapshotExporter.verify(snapDir);
  assert.equal(problems.length, 1);
  assert.match(problems[0], /checksum mismatch|size mismatch/);
});
