import test from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { decodeMessage, encodeMessage } from '../src/message.ts';
import { addressBookSchema, sampleBook } from './helpers/fixture.ts';

test('512 concurrent async decodes of one shared buffer are identical', async () => {
  const data = encodeMessage(addressBookSchema, sampleBook());
  const expected = decodeMessage(addressBookSchema, data);
  const results = await Promise.all(
    Array.from({ length: 512 }, async (_, i) => {
      await new Promise((resolve) => setImmediate(resolve));
      const decoded = decodeMessage(addressBookSchema, data);
      assert.strictEqual(
        (decoded.people as Array<Record<string, unknown>>)[0].avatar instanceof Uint8Array,
        true,
        `task ${i}`,
      );
      return decoded;
    }),
  );
  for (const r of results) assert.deepEqual(r, expected);
  // input buffer untouched by concurrent readers
  assert.deepEqual(data, encodeMessage(addressBookSchema, sampleBook()));
});

test('interleaved encode/decode from many tasks never corrupts output', async () => {
  const book = sampleBook();
  const reference = encodeMessage(addressBookSchema, book);
  const outputs = await Promise.all(
    Array.from({ length: 256 }, async () => {
      await new Promise((resolve) => setImmediate(resolve));
      const bytes = encodeMessage(addressBookSchema, book);
      return decodeMessage(addressBookSchema, bytes);
    }),
  );
  const expected = decodeMessage(addressBookSchema, reference);
  for (const o of outputs) assert.deepEqual(o, expected);
});

test('worker threads decode in parallel with identical results', async () => {
  const data = encodeMessage(addressBookSchema, sampleBook());
  const expected = decodeMessage(addressBookSchema, data);
  const workerUrl = new URL('./helpers/decode-worker.ts', import.meta.url);
  const results = await Promise.all(
    Array.from(
      { length: 4 },
      () =>
        new Promise<{ result: unknown; checksum: number }>((resolve, reject) => {
          const worker = new Worker(workerUrl, { workerData: { data, iterations: 200 } });
          worker.once('message', resolve);
          worker.once('error', reject);
        }),
    ),
  );
  const checksums = new Set<number>();
  for (const { result, checksum } of results) {
    assert.deepEqual(result, expected);
    checksums.add(checksum);
  }
  assert.equal(checksums.size, 1, 'all workers must compute the same checksum');
});
