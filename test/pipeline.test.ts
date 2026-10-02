import { test } from 'node:test';
import assert from 'node:assert/strict';

import { ChunkPool } from '../src/pool.ts';
import { MemoryMonitor } from '../src/monitor.ts';
import { buildPipeline } from '../src/pipeline.ts';

/** Scalar reference that mirrors the pipeline's chunking and FP order. */
function reference(totalRows: number, chunkSize: number): number {
  let carry = 0;
  let sum = 0;
  let emitted = 0;
  while (emitted < totalRows) {
    const n = Math.min(chunkSize, totalRows - emitted);
    const vals: number[] = [];
    for (let i = 0; i < n; i++) {
      const raw = emitted + i;
      if (raw % 3 === 0) continue;               // 1. filter
      let v = raw + 7.5;                         // 2. add
      v *= 1.25;                                 // 3. multiply
      v = Math.sqrt(v);                          // 4. sqrt
      v = v < 0 ? 0 : v > 500 ? 500 : v;         // 5. clamp
      v = v % 97;                                // 6. modulo
      v = Math.abs(v);                           // 7. abs
      v = 2 * v - 3;                             // 8. scale/offset
      carry += v;                                // 9. running sum
      vals.push(carry);
    }
    for (const x of vals) sum += x;              // 10. replicate, copy 1
    for (const x of vals) sum += x + 1;          // 10. replicate, copy 2
    emitted += n;
  }
  return sum;
}

test('pool reuses slots and guards against double release', () => {
  const pool = new ChunkPool(4, 8);
  const a = pool.acquire();
  const b = pool.acquire();
  assert.equal(pool.stats().inFlight, 2);
  pool.release(a);
  const c = pool.acquire();
  assert.equal(c.id, a.id, 'released slot must be reused');
  pool.release(b);
  pool.release(c);
  assert.equal(pool.stats().inFlight, 0);
  assert.throws(() => pool.release(c), /double release/);
  assert.equal(pool.stats().allocations, 0);
});

test('pool grows only when exhausted and counts it as an allocation', () => {
  const pool = new ChunkPool(1, 8);
  const a = pool.acquire();
  const b = pool.acquire();
  assert.equal(pool.stats().allocations, 1);
  pool.release(a);
  pool.release(b);
  const c = pool.acquire();
  assert.equal(pool.stats().allocations, 1, 'reacquire must not grow again');
  pool.release(c);
});

test('10-operator pipeline produces correct results', () => {
  const rows = 100_003; // deliberately not a multiple of the chunk size
  const chunkSize = 256;
  const { pool, sink } = buildPipeline(rows, { capacity: 8, chunkSize });
  const actual = sink.run();
  const expected = reference(rows, chunkSize);
  assert.equal(actual, expected);
  const stats = pool.stats();
  assert.equal(stats.acquireCount, stats.releaseCount, 'no leaked chunks');
  assert.equal(stats.allocations, 0);
});

test('zero heap allocation and zero GC pauses in steady state', () => {
  const chunkSize = 4096;

  // Warm up JIT and let setup-phase garbage be collected before measuring.
  buildPipeline(1_000_000, { chunkSize }).sink.run();
  (globalThis as { gc?: () => void }).gc?.();

  const monitor = new MemoryMonitor();
  const { pool, sink } = buildPipeline(20_000_000, {
    capacity: 16,
    chunkSize,
    monitor,
  });

  monitor.start();
  const sum = sink.run();
  const report = monitor.stop();

  assert.ok(Number.isFinite(sum));
  const stats = pool.stats();
  assert.equal(stats.allocations, 0, 'pool must not grow after setup');
  assert.equal(stats.inFlight, 0, 'all chunks returned to the pool');
  assert.equal(report.gcEvents, 0, 'no GC cycle may fire during execution');
  assert.equal(report.gcPauseMs, 0, 'no GC pause time');
  assert.ok(
    report.heapDeltaBytes < 4 * 1024 * 1024,
    `heap curve must stay flat, got delta ${report.heapDeltaBytes} bytes`,
  );
});
