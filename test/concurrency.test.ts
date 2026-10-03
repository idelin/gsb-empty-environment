import { test } from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { Worker } from 'node:worker_threads';
import { defineSchema, MessageDecoder } from '../src/decoder.ts';
import { encode } from '../src/encoder.ts';

const Task = defineSchema('Task', [
  { no: 1, name: 'id', type: 'varint' },
  { no: 2, name: 'title', type: 'string' },
  { no: 3, name: 'priority', type: 'zigzag32' },
  { no: 4, name: 'labels', type: 'string', repeated: true },
  { no: 5, name: 'points', type: 'fixed32', repeated: true, packed: true },
]);

test('async interleaving: 64 concurrent tasks share one decoder, zero leaks', async () => {
  const decoder = new MessageDecoder(Task);
  const bytes = encode(Task, {
    id: 7,
    title: 'hot path',
    priority: -3,
    labels: ['a', 'b', 'c'],
    points: [1, 2, 3, 4],
  });
  const tasks = Array.from({ length: 64 }, async (_, taskNo) => {
    for (let i = 0; i < 500; i++) {
      const msg = decoder.decode(bytes) as Record<string, unknown>;
      // yield to interleave with other tasks mid-flight
      if (i % 7 === 0) await new Promise((resolve) => setImmediate(resolve));
      assert.equal(msg.id, 7);
      assert.equal(msg.title, 'hot path');
      assert.equal(msg.priority, -3);
      assert.deepEqual(msg.labels, ['a', 'b', 'c']);
      assert.deepEqual(msg.points, [1, 2, 3, 4]);
      decoder.release(msg);
    }
    return taskNo;
  });
  await Promise.all(tasks);
  decoder.assertNoLeaks();
});

test('async interleaving: decode errors during concurrent load do not leak', async () => {
  const decoder = new MessageDecoder(Task);
  const good = encode(Task, { id: 1, title: 'ok', labels: ['x'] });
  const truncated = good.subarray(0, good.length - 1);
  const tasks = Array.from({ length: 32 }, async (_, taskNo) => {
    for (let i = 0; i < 200; i++) {
      if ((i + taskNo) % 3 === 0) {
        assert.throws(() => decoder.decode(truncated));
      } else {
        decoder.release(decoder.decode(good));
      }
      if (i % 5 === 0) await new Promise((resolve) => setImmediate(resolve));
    }
  });
  await Promise.all(tasks);
  decoder.assertNoLeaks();
});

test('worker threads: parallel decoders in real threads, all leak-free', async () => {
  const workerUrl = new URL('./decode-worker.ts', import.meta.url);
  const iterations = 20_000;
  const workers = Array.from(
    { length: 4 },
    (_, seed) => new Worker(workerUrl, { workerData: { iterations, seed } }),
  );
  const results = await Promise.all(
    workers.map(async (w) => {
      const [msg] = (await once(w, 'message')) as [{ ok: boolean; checksum: number }];
      const [exitCode] = await once(w, 'exit');
      assert.equal(exitCode, 0);
      return msg;
    }),
  );
  for (const [i, r] of results.entries()) {
    assert.equal(r.ok, true);
    assert.equal(r.checksum, iterations * (i + 3), `worker ${i} checksum`);
  }
});
