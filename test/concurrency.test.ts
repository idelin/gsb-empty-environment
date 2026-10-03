import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { AtomicMPMCQueue } from '../src/queue.ts';

const WORKER_URL = new URL('./helpers/mpmc_worker.ts', import.meta.url);

interface WorkerResult {
  done: boolean;
  collected?: number[];
}

function runWorker(workerData: Record<string, unknown>): Promise<WorkerResult> {
  return new Promise((resolve, reject) => {
    const worker = new Worker(WORKER_URL, { workerData });
    worker.once('message', resolve);
    worker.once('error', reject);
    worker.once('exit', (code) => {
      if (code !== 0) reject(new Error(`worker exited with code ${code}`));
    });
  });
}

async function runCluster(
  capacity: number,
  producers: number,
  consumers: number,
  perProducer: number,
): Promise<{ all: number[]; queue: AtomicMPMCQueue; total: number }> {
  const total = producers * perProducer;
  const queue = new AtomicMPMCQueue(capacity);
  const counterSab = new SharedArrayBuffer(4);
  const jobs: Promise<WorkerResult>[] = [];
  for (let p = 0; p < producers; p++) {
    jobs.push(
      runWorker({
        role: 'producer',
        sab: queue.buffer,
        capacity,
        start: p * perProducer,
        count: perProducer,
      }),
    );
  }
  for (let c = 0; c < consumers; c++) {
    jobs.push(
      runWorker({
        role: 'consumer',
        sab: queue.buffer,
        capacity,
        counterSab,
        total,
      }),
    );
  }
  const results = await Promise.all(jobs);
  const consumed = new Int32Array(counterSab);
  assert.equal(Atomics.load(consumed, 0), total, 'consumed count mismatch');
  const all: number[] = [];
  for (const r of results) {
    if (r.collected) all.push(...r.collected);
  }
  return { all, queue, total };
}

test('MPMC: 3 producers x 3 consumers, zero loss, zero duplication', async () => {
  const { all, queue, total } = await runCluster(1024, 3, 3, 50_000);
  assert.ok(queue.isEmpty, 'queue must be drained');
  assert.equal(all.length, total, 'item loss detected');
  const seen = new Uint8Array(total);
  for (const v of all) {
    assert.ok(v >= 0 && v < total, `value ${v} out of expected range`);
    assert.equal(seen[v], 0, `duplicate value ${v}`);
    seen[v] = 1;
  }
});

test('MPMC: minimal-capacity queue under 2 producers x 2 consumers', async () => {
  const { all, queue, total } = await runCluster(2, 2, 2, 5_000);
  assert.ok(queue.isEmpty);
  assert.equal(all.length, total);
  assert.equal(new Set(all).size, total);
});
