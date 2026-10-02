import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { MPMCRing } from '../src/queue/mpmc-ring.ts';

const WORKER_URL = new URL('./helpers/ring-worker.ts', import.meta.url);

function runWorker(workerData: unknown): Promise<void> {
  return new Promise((resolve, reject) => {
    const worker = new Worker(WORKER_URL, { workerData });
    worker.once('error', reject);
    worker.once('exit', (code) => {
      if (code === 0) resolve();
      else reject(new Error(`worker exited with code ${code}`));
    });
  });
}

test('lock-free MPMC ring: 4 producers x 4 consumers, exact delivery, no loss', async () => {
  const PRODUCERS = 4;
  const CONSUMERS = 4;
  const PER_PRODUCER = 25_000;
  const TOTAL = PRODUCERS * PER_PRODUCER;

  const ring = MPMCRing.create(1024);
  const seenBuffer = new SharedArrayBuffer(TOTAL * 4);
  const counterBuffer = new SharedArrayBuffer(4);

  const tasks: Promise<void>[] = [];
  for (let p = 0; p < PRODUCERS; p++) {
    tasks.push(
      runWorker({
        mode: 'produce',
        ringBuffer: ring.buffer,
        capacity: ring.capacity,
        start: p * PER_PRODUCER,
        count: PER_PRODUCER,
      })
    );
  }
  for (let c = 0; c < CONSUMERS; c++) {
    tasks.push(
      runWorker({
        mode: 'consume',
        ringBuffer: ring.buffer,
        capacity: ring.capacity,
        total: TOTAL,
        seenBuffer,
        counterBuffer,
      })
    );
  }
  await Promise.all(tasks);

  // Every value 0..TOTAL-1 must be consumed exactly once: no loss, no dupes.
  const seen = new Uint32Array(seenBuffer);
  for (let i = 0; i < TOTAL; i++) {
    assert.equal(seen[i], 1, `value ${i} consumed ${seen[i]} times`);
  }
  const consumed = new Int32Array(counterBuffer);
  assert.equal(Atomics.load(consumed, 0), TOTAL);

  // No leak: the ring is completely drained afterwards.
  assert.ok(ring.isEmpty);
  assert.equal(ring.dequeue(), undefined);
});

test('CAS counter: 8 threads x 100k atomic increments, no lost updates', async () => {
  const THREADS = 8;
  const PER_THREAD = 100_000;
  const counterBuffer = new SharedArrayBuffer(4);

  await Promise.all(
    Array.from({ length: THREADS }, () =>
      runWorker({ mode: 'count', counterBuffer, count: PER_THREAD })
    )
  );

  const counter = new Int32Array(counterBuffer);
  assert.equal(Atomics.load(counter, 0), THREADS * PER_THREAD);
});
