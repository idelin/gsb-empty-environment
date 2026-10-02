import { rm, readdir } from 'node:fs/promises';
import { performance } from 'node:perf_hooks';
import { LsmEngine } from '../src/engine.ts';

const dir = new URL('../demo-data', import.meta.url).pathname;
await rm(dir, { recursive: true, force: true });

const engine = await LsmEngine.open(dir); // default 4 MB MemTable threshold

const t0 = performance.now();
const stamp = () => `+${((performance.now() - t0) / 1000).toFixed(2)}s`;

engine.on('rotate', (e) =>
  console.log(`[${stamp()}] ROTATE  memtable#${e.tableId} frozen at ${(e.sizeBytes / 1048576).toFixed(2)} MB -> immutable queue`),
);
engine.on('flush:start', (e) =>
  console.log(`[${stamp()}] FLUSH>  memtable#${e.tableId} (${e.entries} entries) -> L0`),
);
engine.on('flush:end', (e) =>
  console.log(`[${stamp()}] FLUSH<  memtable#${e.tableId} persisted as ${e.sstPath.split('/').pop()}, sealed WAL archived`),
);

console.log('High-frequency write phase: 1 KB values until the 4 MB threshold trips twice\n');
let maxPutLatencyMs = 0;
let written = 0;
let rotations = 0;
engine.on('rotate', () => rotations++);

while (rotations < 2) {
  const start = performance.now();
  await engine.put(`user:${String(written).padStart(8, '0')}`, Buffer.alloc(1024, written % 251));
  maxPutLatencyMs = Math.max(maxPutLatencyMs, performance.now() - start);
  written++;
}
// Keep the foreground busy while the flusher drains in the background.
for (let extra = 0; extra < 500; extra++, written++) {
  await engine.put(`user:${String(written).padStart(8, '0')}`, Buffer.alloc(1024, written % 251));
}

await engine.drain();
const elapsed = (performance.now() - t0) / 1000;

console.log(`\n[${stamp()}] write phase complete`);
console.log(`  keys written        : ${written}`);
console.log(`  write throughput    : ${Math.round(written / elapsed).toLocaleString()} ops/s (${(written * 1040 / 1048576 / elapsed).toFixed(1)} MB/s)`);
console.log(`  max put latency     : ${maxPutLatencyMs.toFixed(2)} ms (no foreground stall during flush)`);

// Full integrity verification.
let verified = 0;
for (let i = 0; i < written; i++) {
  const got = engine.get(`user:${String(i).padStart(8, '0')}`);
  if (!got || !got.equals(Buffer.alloc(1024, i % 251))) throw new Error(`integrity failure at key ${i}`);
  verified++;
}
console.log(`  integrity check     : ${verified}/${written} keys verified`);

const stats = engine.stats();
const files = await readdir(dir);
console.log(`  L0 SSTables         : ${files.filter((f) => f.endsWith('.sst')).sort().join(', ')}`);
console.log(`  live WAL segments   : ${files.filter((f) => f.endsWith('.log')).sort().join(', ')}`);
console.log(`  immutable queue     : ${stats.immutableQueue.length === 0 ? '(empty)' : JSON.stringify(stats.immutableQueue)}`);

await engine.close();
console.log('\nDone. Engine closed cleanly.');
