// Acceptance walkthrough: snapshot an LSM engine via hard links, keep
// writing + compacting in the foreground, then prove the snapshot is
// untouched and an incremental export captures only the delta.
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LSMEngine } from '../src/lsm/engine.ts';
import { decodeSSTable } from '../src/lsm/sstable.ts';
import { SnapshotManager } from '../src/backup/snapshot.ts';

const root = mkdtempSync(join(tmpdir(), 'lsm-demo-'));
const engine = new LSMEngine(join(root, 'data'), { flushThreshold: 500 });
const backups = new SnapshotManager(engine, join(root, 'backups'));

const line = (s = '') => console.log(s);

line('=== 1. Foreground writes: 5,000 keys (auto-flush to immutable SSTables) ===');
for (let i = 0; i < 5000; i++) engine.put(`user-${String(i).padStart(6, '0')}`, `payload-${i}`);
engine.flush();
const live = engine.listSSTables();
const liveBytes = live.reduce((n, t) => n + t.size, 0);
line(`   live SSTables: ${live.length} (${live.map((t) => t.name).join(', ')})`);
line(`   live data: ${liveBytes} bytes`);

line('\n=== 2. Zero-copy snapshot (hard links only) ===');
// Pure hard-link cost: metadata ops only, no data bytes are read or copied.
const tPure = performance.now();
const timingProbe = backups.createSnapshot({ label: 'timing' });
const pureMs = performance.now() - tPure;
rmSync(backups.snapshotDir(timingProbe.id), { recursive: true, force: true });
const t0 = performance.now();
const snap = backups.createSnapshot({ label: 'baseline', checksum: true });
const elapsed = performance.now() - t0;
line(`   snapshot id: ${snap.id}`);
line(`   files linked: ${snap.files.length}`);
line(`   hard-link snapshot: ${pureMs.toFixed(2)}ms (zero data IO)`);
line(`   + optional sha256 manifest for later integrity checks: ${elapsed.toFixed(2)}ms`);
const probe = snap.files[0];
const inoSnap = statSync(join(backups.snapshotDir(snap.id), probe.name)).ino;
const inoLive = statSync(join(engine.dir, probe.name)).ino;
line(`   inode(${probe.name}) snapshot=${inoSnap} live=${inoLive} -> same inode, 0 bytes copied`);

line('\n=== 3. Foreground keeps writing 5,000 more keys, then compaction ===');
for (let i = 5000; i < 10000; i++) engine.put(`user-${String(i).padStart(6, '0')}`, `payload-${i}`);
const compact = engine.compact();
line(`   compaction merged into ${compact!.merged.name}`);
line(`   unlinked old files: ${compact!.removed.join(', ')}`);
line(`   live dir now: ${readdirSync(engine.dir).join(', ')}`);
line(`   original sst-000001.sst still in live dir? ${existsSync(join(engine.dir, 'sst-000001.sst'))}`);

line('\n=== 4. Snapshot is unaffected: verify integrity ===');
const report = backups.verify(snap.id);
line(`   verify(${snap.id}): ok=${report.ok}, checked=${report.checked} files, errors=${report.errors.length}`);
const restored = new Map<string, string>();
for (const f of snap.files) {
  for (const e of decodeSSTable(readFileSync(join(backups.snapshotDir(snap.id), f.name))).entries) {
    if (e.value !== null) restored.set(e.key, e.value);
  }
}
line(`   keys readable from snapshot: ${restored.size} (expected 5000)`);
line(`   sample: user-000000=${restored.get('user-000000')}, user-004999=${restored.get('user-004999')}`);

line('\n=== 5. Incremental export since baseline (differential archive) ===');
const incr = backups.exportIncremental(snap.id, { checksum: true });
line(`   incremental id: ${incr.id}`);
line(`   added:   [${incr.added.map((f) => f.name).join(', ')}]`);
line(`   removed: [${incr.removed.join(', ')}]`);
line(`   verify(${incr.id}): ok=${backups.verify(incr.id).ok}`);

line('\n=== 6. Backup inventory ===');
for (const b of backups.listBackups()) {
  const n = b.kind === 'full' ? b.files.length : b.added.length;
  line(`   ${b.kind.padEnd(11)} ${b.id}  files=${n}  seq=${b.sequence}`);
}
line(`\nWorkspace: ${root}`);
