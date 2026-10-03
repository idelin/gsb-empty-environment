/**
 * Demo: one big source transaction (500k row changes) streamed to a
 * receiver in 100 chunks through the memory-protection rate limiter.
 *
 * The simulated receiver sink is deliberately slower than the source,
 * so the guard's in-memory budget fills up and overflows to disk —
 * exactly what happens when a receiver falls behind under load.
 *
 * Run: npm run demo
 */
import { setTimeout as delay } from 'node:timers/promises';
import { TransactionReceiver } from '../src/receiver.ts';
import { generateChanges } from '../src/source.ts';
import { SpillQueue } from '../src/spill-queue.ts';
import { transportTransaction } from '../src/transporter.ts';

const TOTAL_ROWS = 500_000;
const CHUNK_SIZE = 5_000; // -> 100 batches
const PEAK_BUDGET = 10 * 1024 * 1024; // 10MB acceptance bound
const SINK_LATENCY_MS = 10; // simulated slow receiver

function mb(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`;
}

// --- Baseline: what the naive approach (load everything) would cost ---
// Only with --naive: it needs real headroom, so don't combine with the
// constrained-heap flags used by `npm run demo`.
if (process.argv.includes('--naive')) {
  global.gc();
  const naiveBase = process.memoryUsage().heapUsed;
  const all: unknown[] = [];
  for await (const row of generateChanges(TOTAL_ROWS)) all.push(row);
  global.gc();
  const naiveDelta = process.memoryUsage().heapUsed - naiveBase;
  console.log(`naive load-all-into-memory : ${mb(naiveDelta)} heap for ${all.length.toLocaleString()} rows`);
  all.length = 0;
}

// --- Chunked streaming transfer through the memory guard ---
global.gc();
const baseline = process.memoryUsage().heapUsed;
let peakRaw = baseline;
let peakLive = baseline;

const guard = new SpillQueue({
  maxMemoryBytes: 1024 * 1024, // in-memory budget before disk spill
  maxQueuedBytes: 2 * 1024 * 1024, // high-water mark -> producer throttled
});

let appliedRows = 0;
const receiver = new TransactionReceiver('txn-demo', async (rows) => {
  appliedRows += rows.length; // receiver applies rows, retains nothing
  await delay(SINK_LATENCY_MS); // slow sink -> forces backpressure + spill
});

console.log(`streaming transfer       : ${TOTAL_ROWS.toLocaleString()} rows in chunks of ${CHUNK_SIZE.toLocaleString()}...`);
const started = performance.now();

const result = await transportTransaction({
  txnId: 'txn-demo',
  source: generateChanges(TOTAL_ROWS),
  chunkSize: CHUNK_SIZE,
  guard,
  receiver,
  onChunkDelivered: () => {
    const raw = process.memoryUsage().heapUsed;
    if (raw > peakRaw) peakRaw = raw;
    global.gc(); // measure live heap, not GC slack
    const live = process.memoryUsage().heapUsed;
    if (live > peakLive) peakLive = live;
  },
});

global.gc();
const elapsed = performance.now() - started;
const rawDelta = peakRaw - baseline;
const liveDelta = peakLive - baseline;
const summary = receiver.summary;

console.log('');
console.log('--- Transfer result ---');
console.log(`rows transferred     : ${result.rows.toLocaleString()}`);
console.log(`data chunks          : ${summary.totalChunks} (+1 END marker)`);
console.log(`rows applied at sink : ${appliedRows.toLocaleString()}`);
console.log(`integrity committed  : ${summary.committed}`);
console.log(`txn checksum         : ${summary.txnChecksum.slice(0, 16)}...`);
console.log(`elapsed              : ${elapsed.toFixed(0)} ms`);
console.log('');
console.log('--- Memory guard ---');
console.log(`guard peak memory    : ${mb(result.guard.peakMemoryBytes)} (budget 1.00 MB)`);
console.log(`spilled to disk      : ${mb(result.guard.spillWriteBytes)}`);
console.log(`read back from disk  : ${mb(result.guard.spillReadBytes)}`);
console.log(`rate-limit stalls    : ${result.guard.highWaterHits}`);
console.log('');
console.log('--- Process memory (baseline ' + mb(baseline) + ') ---');
console.log(`peak live heap delta : ${mb(liveDelta)} (acceptance bound: ${mb(PEAK_BUDGET)})`);
console.log(`peak raw heap delta  : ${mb(rawDelta)} (includes not-yet-collected garbage)`);

await guard.destroy();

if (!summary.committed || summary.totalRows !== TOTAL_ROWS) {
  console.error('FAIL: transaction integrity not verified');
  process.exit(1);
}
if (liveDelta >= PEAK_BUDGET) {
  console.error('FAIL: peak live memory exceeded 10MB');
  process.exit(1);
}
console.log('');
console.log('OK: 500k rows streamed in 100 batches, integrity verified, peak memory < 10MB');
