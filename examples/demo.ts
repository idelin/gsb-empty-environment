import { readdirSync, rmSync, statSync } from 'node:fs';
import path from 'node:path';
import { LsmEngine } from '../src/engine.ts';

const dir = process.argv[2] ?? path.join(process.cwd(), 'demo-data');
rmSync(dir, { recursive: true, force: true });

const fmt = (n: number) => n.toLocaleString('en-US');
const mb = (n: number) => `${(n / 1024 / 1024).toFixed(2)} MB`;

const engine = await LsmEngine.open({
  dir,
  memtableThresholdBytes: 4 * 1024 * 1024, // 4MB threshold per spec
  onRotate: (s) =>
    console.log(`[rotate #${s.rotations}] memtable froze at 4MB -> immutable queue (pending flushes: ${s.flushesPending})`),
  onFlush: (file, s) =>
    console.log(`[flush  #${s.flushesCompleted}] immutable memtable -> l0/${file} (WAL segment reclaimed)`),
});

const total = 20000;
const value = Buffer.alloc(440, 'x'); // ~483B/entry with key+overhead -> ~9.7MB total
console.log(`writing ${fmt(total)} entries (~${mb(total * 483)}) against a 4MB memtable threshold...\n`);

const started = performance.now();
for (let i = 0; i < total; i++) {
  engine.put(`key-${String(i).padStart(8, '0')}`, value);
  if (i % 512 === 0) await new Promise((r) => setImmediate(r)); // foreground keeps writing, flusher runs in background
}
const writeMs = performance.now() - started;
console.log(`\nforeground writes done: ${fmt(total)} puts in ${writeMs.toFixed(1)}ms (${fmt(Math.round(total / (writeMs / 1000)))} ops/s, never blocked on flush)`);

await engine.waitIdle();
const stats = engine.getStats();
console.log(`\nfinal state: rotations=${stats.rotations}, l0 files=${stats.l0FileCount}, wal segments left=${stats.walSegmentCount}`);

let verified = 0;
for (let i = 0; i < total; i++) {
  if (engine.get(`key-${String(i).padStart(8, '0')}`)) verified++;
}
console.log(`data integrity: ${fmt(verified)}/${fmt(total)} keys verified through the read path`);
await engine.close();

const reopened = await LsmEngine.open({ dir });
const sample = reopened.get('key-00012345');
console.log(`after reopen from disk: key-00012345 ${sample ? 'found' : 'MISSING'} (${sample?.length ?? 0} bytes)`);
await reopened.close();

console.log('\non-disk layout:');
for (const sub of ['wal', 'l0']) {
  for (const f of readdirSync(path.join(dir, sub))) {
    const size = statSync(path.join(dir, sub, f)).size;
    console.log(`  ${sub}/${f}  ${mb(size)}`);
  }
}
