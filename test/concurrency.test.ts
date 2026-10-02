import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { decodeMessage, encodeMessage, runWorkload } from '../src/index.ts';
import { Person, samplePerson } from '../fixtures/schemas.ts';

const workerPath = new URL('../fixtures/decode-worker.mjs', import.meta.url);

test('concurrent async decodes over a shared buffer are isolated and identical', async () => {
  const bytes = encodeMessage(Person, samplePerson);
  const results = await Promise.all(
    Array.from({ length: 256 }, () => Promise.resolve().then(() => decodeMessage(Person, bytes))),
  );
  for (const r of results) {
    assert.deepEqual(r, results[0]);
  }
  // input buffer untouched by the concurrent storm
  assert.deepEqual(bytes, encodeMessage(Person, samplePerson));
});

test('parallel worker threads decode the same buffer identically', async () => {
  const bytes = encodeMessage(Person, samplePerson);
  const workers = Array.from({ length: 4 }, () =>
    new Worker(workerPath, { workerData: { bytes, iterations: 2000 } }),
  );
  try {
    const results = await Promise.all(
      workers.map((w) => new Promise((resolve, reject) => {
        w.once('message', resolve);
        w.once('error', reject);
      })),
    );
    for (const r of results) {
      assert.deepEqual(r, results[0]);
    }
  } finally {
    await Promise.all(workers.map((w) => w.terminate()));
  }
});

test('interleaved simulations on shared rng seed stay independent', async () => {
  const [a, b] = await Promise.all([
    Promise.resolve().then(() => runWorkload({ events: 20_000, seed: 77, storeTrace: false })),
    Promise.resolve().then(() => runWorkload({ events: 20_000, seed: 77, storeTrace: false })),
  ]);
  assert.equal(a.checksum, b.checksum);
});

test('no leaked handles or listeners after full lifecycle', async () => {
  const handlesBefore = (process._getActiveHandles?.() ?? []).length;
  const listenersBefore = process.listenerCount('uncaughtException');

  const bytes = encodeMessage(Person, samplePerson);
  for (let i = 0; i < 100; i++) decodeMessage(Person, bytes);
  const sim = runWorkload({ events: 10_000, seed: 1, storeTrace: false });
  assert.ok(sim.steps > 0);

  const worker = new Worker(workerPath, { workerData: { bytes, iterations: 10 } });
  await new Promise((resolve, reject) => {
    worker.once('message', resolve);
    worker.once('error', reject);
  });
  await worker.terminate();

  const handlesAfter = (process._getActiveHandles?.() ?? []).length;
  assert.ok(handlesAfter <= handlesBefore, `handle leak: before=${handlesBefore} after=${handlesAfter}`);
  assert.equal(process.listenerCount('uncaughtException'), listenersBefore);
});

test('repeated decode does not accumulate memory (bounded heap growth)', () => {
  const bytes = encodeMessage(Person, samplePerson);
  const iterations = 200_000;
  for (let i = 0; i < 10_000; i++) decodeMessage(Person, bytes); // warmup
  const before = process.memoryUsage().heapUsed;
  for (let i = 0; i < iterations; i++) decodeMessage(Person, bytes);
  const after = process.memoryUsage().heapUsed;
  const growth = after - before;
  assert.ok(
    growth < 64 * 1024 * 1024,
    `heap grew by ${(growth / 1024 / 1024).toFixed(1)}MiB over ${iterations} decodes`,
  );
});
