/**
 * Sample: run a 10-operator vectorized pipeline over 100M rows and show
 * that, after pipeline setup, heap allocations and GC pauses stay at zero.
 */

import { buildPipeline } from '../src/pipeline.ts';
import { MemoryMonitor } from '../src/monitor.ts';

const TOTAL_ROWS = 100_000_000;
const CHUNK_SIZE = 4096;

function mb(bytes: number): string {
  return (bytes / 1024 / 1024).toFixed(2) + ' MB';
}

// Warm-up run: JIT compilation + pipeline setup (allocations allowed here).
{
  const warmup = buildPipeline(1_000_000, { chunkSize: CHUNK_SIZE });
  warmup.sink.run();
}
(globalThis as { gc?: () => void }).gc?.();

// --- Measured run: pipeline is built, then executed under the monitor. ---
const monitor = new MemoryMonitor();
const { pool, sink } = buildPipeline(TOTAL_ROWS, {
  capacity: 16,
  chunkSize: CHUNK_SIZE,
  monitor,
});

monitor.start();
const started = performance.now();
const checksum = sink.run();
const elapsedMs = performance.now() - started;
const report = monitor.stop();

const stats = pool.stats();
console.log('=== Vectorized pipeline sample run ===');
console.log(`rows scanned        : ${TOTAL_ROWS.toLocaleString('en-US')}`);
console.log(`rows after fan-out  : ${sink.rows.toLocaleString('en-US')}`);
console.log(`chunks processed    : ${sink.chunks.toLocaleString('en-US')}`);
console.log(`checksum            : ${checksum}`);
console.log(`elapsed             : ${elapsedMs.toFixed(1)} ms`);
console.log('--- pool ---');
console.log(`pool capacity       : ${stats.capacity} chunks x ${stats.chunkSize} slots`);
console.log(`acquire / release   : ${stats.acquireCount} / ${stats.releaseCount}`);
console.log(`in-flight (leaks)   : ${stats.inFlight}`);
console.log(`heap allocations    : ${stats.allocations}  (post-setup chunk growth)`);
console.log('--- memory monitor ---');
console.log(`GC events           : ${report.gcEvents}`);
console.log(`GC pause time       : ${report.gcPauseMs.toFixed(3)} ms`);
console.log(`heap baseline       : ${mb(report.baselineHeapBytes)}`);
console.log(`heap min / max      : ${mb(report.minHeapBytes)} / ${mb(report.maxHeapBytes)}`);
console.log(`heap delta          : ${mb(report.heapDeltaBytes)} over ${report.samples} samples`);
