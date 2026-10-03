import assert from 'node:assert/strict';
import { test } from 'node:test';
import { promises as fs } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { chunkTransaction } from '../src/chunker.ts';
import { TransactionReceiver } from '../src/receiver.ts';
import { generateChanges } from '../src/source.ts';
import { SpillQueue } from '../src/spill-queue.ts';
import { transportTransaction } from '../src/transporter.ts';
import type { ChangeEvent, Chunk } from '../src/types.ts';

const TOTAL_ROWS = 500_000;
const CHUNK_SIZE = 5_000; // 500k / 5k = 100 batches
const MEMORY_BUDGET_BYTES = 10 * 1024 * 1024; // 10MB acceptance bound

function makeReceiver(txnId: string, applied: { rows: number }) {
  return new TransactionReceiver(txnId, (rows: ChangeEvent[]) => {
    applied.rows += rows.length;
  });
}

test('chunker splits 500k-row transaction into 100 phased chunks + END marker', async () => {
  const phases: string[] = [];
  let dataChunks = 0;
  let endMarker: Chunk | undefined;
  let lastCumulative = 0;

  for await (const chunk of chunkTransaction('txn-1', generateChanges(TOTAL_ROWS), CHUNK_SIZE)) {
    phases.push(chunk.header.phase);
    if (chunk.header.phase === 'END') {
      endMarker = chunk;
    } else {
      dataChunks += 1;
      assert.equal(chunk.rows.length, CHUNK_SIZE);
      assert.equal(chunk.header.chunkIndex, dataChunks - 1);
      lastCumulative = chunk.header.cumulativeRows;
    }
  }

  assert.equal(dataChunks, 100);
  assert.equal(phases[0], 'BEGIN');
  assert.ok(phases.slice(1, 100).every((p) => p === 'CONTINUE'));
  assert.equal(phases[100], 'END');
  assert.equal(lastCumulative, TOTAL_ROWS);
  assert.equal(endMarker?.header.totalRows, TOTAL_ROWS);
  assert.equal(endMarker?.header.totalChunks, 100);
  assert.ok(endMarker?.header.txnChecksum);
});

test('end-to-end: 500k rows streamed in 100 batches with integrity verified', async () => {
  // Budget below one serialized chunk (~0.55MB) so spill engages deterministically.
  const guard = new SpillQueue({ maxMemoryBytes: 256 * 1024, maxQueuedBytes: 2 * 1024 * 1024 });
  const applied = { rows: 0 };
  const receiver = makeReceiver('txn-big', applied);

  const result = await transportTransaction({
    txnId: 'txn-big',
    source: generateChanges(TOTAL_ROWS),
    chunkSize: CHUNK_SIZE,
    guard,
    receiver,
  });

  assert.equal(result.rows, TOTAL_ROWS);
  assert.equal(result.chunks, 101); // 100 data chunks + END marker
  assert.equal(applied.rows, TOTAL_ROWS);

  const summary = receiver.summary;
  assert.equal(summary.committed, true);
  assert.equal(summary.totalRows, TOTAL_ROWS);
  assert.equal(summary.totalChunks, 100);
  assert.match(summary.txnChecksum, /^[0-9a-f]{64}$/);

  // Memory guard engaged: chunks exceeded the memory budget and spilled.
  assert.ok(result.guard.spillWriteBytes > 0, 'expected disk spill to engage');
  assert.equal(result.guard.spillReadBytes, result.guard.spillWriteBytes);
  assert.ok(result.guard.peakMemoryBytes <= 256 * 1024, 'memory budget respected');

  await guard.destroy();
});

test('peak live heap stays within 10MB while streaming 500k rows', async () => {
  global.gc();
  const baseline = process.memoryUsage().heapUsed;
  let peakLive = baseline;

  const guard = new SpillQueue({ maxMemoryBytes: 1024 * 1024, maxQueuedBytes: 2 * 1024 * 1024 });
  const applied = { rows: 0 };
  const receiver = makeReceiver('txn-mem', applied);

  const result = await transportTransaction({
    txnId: 'txn-mem',
    source: generateChanges(TOTAL_ROWS),
    chunkSize: CHUNK_SIZE,
    guard,
    receiver,
    onChunkDelivered: () => {
      // Force GC before sampling so we measure the live working set,
      // not garbage V8 has not yet bothered to collect.
      global.gc();
      const used = process.memoryUsage().heapUsed;
      if (used > peakLive) peakLive = used;
    },
  });

  global.gc();
  const peakDelta = peakLive - baseline;
  assert.equal(result.rows, TOTAL_ROWS);
  assert.ok(
    peakDelta < MEMORY_BUDGET_BYTES,
    `peak live heap delta ${(peakDelta / 1024 / 1024).toFixed(2)}MB exceeds 10MB budget`,
  );
  await guard.destroy();
});

test('SpillQueue preserves FIFO order across the memory/disk boundary', async () => {
  const spillDir = await fs.mkdtemp(join(tmpdir(), 'spillq-test-'));
  const guard = new SpillQueue({ maxMemoryBytes: 64, spillDir });

  const items = Array.from({ length: 10 }, (_, i) =>
    Buffer.from(`item-${i}`.padEnd(16, '-')),
  );
  for (const item of items) await guard.push(item);
  assert.ok(guard.stats.spillWriteBytes > 0, 'expected overflow to disk');

  guard.close();
  const received: string[] = [];
  for (;;) {
    const buf = await guard.shift();
    if (buf === undefined) break;
    received.push(buf.toString());
  }
  assert.deepEqual(received, items.map((b) => b.toString()));
  assert.equal(guard.stats.spillReadBytes, guard.stats.spillWriteBytes);

  await guard.destroy();
  const leftover = await fs.readdir(spillDir);
  assert.equal(leftover.length, 0, 'spill file cleaned up after drain');
  await fs.rmdir(spillDir);
});

test('SpillQueue throttles producer at the high-water mark', async () => {
  const guard = new SpillQueue({ maxMemoryBytes: 128, maxQueuedBytes: 256 });
  const big = Buffer.alloc(256, 1);

  await guard.push(big); // exceeds 128B memory budget -> spilled to disk
  assert.ok(guard.stats.spillWriteBytes > 0);

  let pushDone = false;
  const pending = guard.push(big).then(() => {
    pushDone = true;
  });
  // Wait until the producer is blocked at the high-water mark.
  for (let i = 0; i < 100 && guard.stats.highWaterHits === 0; i++) {
    await new Promise((r) => setImmediate(r));
  }
  assert.ok(guard.stats.highWaterHits > 0, 'rate limiter engaged');
  await new Promise((r) => setImmediate(r));
  assert.equal(pushDone, false, 'producer blocked by rate limiter');

  // Consumer drains one item -> queued bytes drop -> producer unblocks.
  const first = await guard.shift();
  assert.ok(first);
  await pending;
  assert.equal(pushDone, true);

  guard.close();
  const second = await guard.shift();
  assert.ok(second);
  assert.equal(await guard.shift(), undefined);
  await guard.destroy();
});

test('receiver rejects corrupted or out-of-order chunks', async () => {
  const chunks: Chunk[] = [];
  for await (const chunk of chunkTransaction('txn-x', generateChanges(20), 10)) {
    chunks.push(chunk);
  }
  assert.equal(chunks.length, 3); // 2 data chunks + END

  // Tampered row payload -> chunk checksum mismatch.
  const tampered = structuredClone(chunks[0]);
  tampered.rows[0].payload = '{"forged":true}';
  const r1 = makeReceiver('txn-x', { rows: 0 });
  await assert.rejects(() => r1.accept(tampered), /checksum mismatch/);

  // Skipped chunk index -> gap detected.
  const r2 = makeReceiver('txn-x', { rows: 0 });
  await r2.accept(chunks[0]);
  await assert.rejects(() => r2.accept(chunks[2]), /chunk index gap/);

  // Forged END totals -> integrity violation.
  const forgedEnd = structuredClone(chunks[2]);
  forgedEnd.header.totalRows = 999;
  const r3 = makeReceiver('txn-x', { rows: 0 });
  await r3.accept(chunks[0]);
  await r3.accept(chunks[1]);
  await assert.rejects(() => r3.accept(forgedEnd), /totalRows mismatch/);

  // Happy path commits.
  const r4 = makeReceiver('txn-x', { rows: 0 });
  for (const chunk of chunks) await r4.accept(chunk);
  assert.equal(r4.summary.committed, true);
  assert.equal(r4.summary.totalRows, 20);
});
