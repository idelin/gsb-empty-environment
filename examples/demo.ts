import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { LsmEngine } from '../src/lsm/engine.ts';
import { SnapshotExporter } from '../src/backup/exporter.ts';

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-snapshot-demo-'));
const dataDir = path.join(root, 'data');
const backupDir = path.join(root, 'backups');

const fmt = (n: number) => n.toLocaleString('en-US');
const du = (dir: string) =>
  fs.readdirSync(dir)
    .filter((f) => f.endsWith('.sst'))
    .reduce((sum, f) => sum + fs.statSync(path.join(dir, f)).size, 0);

console.log('=== LSM hard-link snapshot exporter demo ===\n');
console.log(`workspace: ${root}\n`);

// 1. Foreground workload: dense writes -> memtable flushes -> SSTables.
const engine = LsmEngine.open(dataDir, { flushThreshold: 2000, compactThreshold: 8 });
const exporter = new SnapshotExporter(engine);

console.log('[1] loading 20,000 keys (flush threshold 2,000) ...');
for (let i = 0; i < 20_000; i++) {
  engine.put(`user:${String(i).padStart(8, '0')}`, `payload-${i}-${'x'.repeat(32)}`);
}
engine.flush();
console.log(`    live SSTables: ${engine.listSSTables().length}, data size: ${fmt(du(dataDir))} bytes\n`);

// 2. Full snapshot: zero-copy hard links, millisecond-scale.
const fullDir = path.join(backupDir, '001-full');
console.log('[2] taking FULL snapshot ...');
const t0 = performance.now();
const fullManifest = exporter.exportFull(fullDir);
const fullMs = performance.now() - t0;
console.log(`    linked ${fullManifest.tables.length} SSTables in ${fullMs.toFixed(2)} ms (zero-copy)`);
const sample = fullManifest.tables[0].file;
console.log(
  `    inode check: data/${sample} ino=${fs.statSync(path.join(dataDir, sample)).ino}` +
    ` == backup/${sample} ino=${fs.statSync(path.join(fullDir, sample)).ino}` +
    ` (nlink=${fs.statSync(path.join(fullDir, sample)).nlink})\n`,
);

// 3. More writes -> incremental snapshot with only the new SSTables.
console.log('[3] writing 3,000 more keys, then INCREMENTAL snapshot ...');
for (let i = 20_000; i < 23_000; i++) {
  engine.put(`user:${String(i).padStart(8, '0')}`, `payload-${i}-${'y'.repeat(32)}`);
}
engine.flush();
const incDir = path.join(backupDir, '002-inc');
const t1 = performance.now();
const inc = exporter.exportIncremental(incDir, path.join(fullDir, 'MANIFEST.json'));
const incMs = performance.now() - t1;
console.log(
  `    delta: ${inc.added.length} new SSTables linked in ${incMs.toFixed(2)} ms,` +
    ` ${inc.skipped.length} already archived (skipped)\n`,
);

// 4. Heavy foreground churn: many flushes + compactions delete old files.
console.log('[4] foreground churn: 30,000 more writes + forced compaction ...');
for (let i = 23_000; i < 55_000; i++) {
  engine.put(`user:${String(i).padStart(8, '0')}`, `payload-${i}-${'z'.repeat(32)}`);
}
engine.flush();
engine.compact();
const liveNow = engine.listSSTables();
console.log(`    live SSTables after compaction: ${liveNow.length} (${liveNow.map((t) => t.file).join(', ')})`);
const gone = fullManifest.tables.filter((t) => !fs.existsSync(path.join(dataDir, t.file)));
console.log(`    baseline files deleted from data dir by compaction: ${gone.length}/${fullManifest.tables.length}\n`);

// 5. Verify snapshots are untouched and byte-identical to their manifests.
console.log('[5] verifying snapshot integrity ...');
for (const dir of [fullDir, incDir]) {
  const problems = SnapshotExporter.verify(dir);
  const name = path.basename(dir);
  if (problems.length === 0) {
    const soleOwners = fs
      .readdirSync(dir)
      .filter((f) => f.endsWith('.sst'))
      .filter((f) => fs.statSync(path.join(dir, f)).nlink === 1).length;
    console.log(`    ${name}: OK (${du(dir)} bytes intact, ${soleOwners} file(s) now solely owned by snapshot)`);
  } else {
    console.log(`    ${name}: CORRUPT -> ${problems.join('; ')}`);
    process.exitCode = 1;
  }
}

console.log('\nDemo complete. Snapshots preserved every generation despite compaction.');
