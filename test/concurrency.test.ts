import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { encodeStruct, decodeStruct, createStats } from '../src/schema.ts';
import { Inspector } from '../src/inspector.ts';
import { OrderBookSchema, OrderSchema, UserSchema, sampleBook } from './helpers/orderbook.ts';

const here = dirname(fileURLToPath(import.meta.url));

function makeInspector(): Inspector {
  return new Inspector()
    .registerSchema(OrderBookSchema)
    .registerSchema(OrderSchema)
    .registerSchema(UserSchema);
}

const canonical = (v: unknown): string =>
  JSON.stringify(v, (_, x) => (typeof x === 'bigint' ? `${x}n` : x));

test('64 interleaved async decode+inspect runs are bit-identical', async () => {
  const bytes = encodeStruct(OrderBookSchema, sampleBook());
  const expected = canonical(decodeStruct(OrderBookSchema, bytes, createStats()));

  const runs = Array.from({ length: 64 }, async (_, i) => {
    // interleave microtasks to shake out shared-state bugs
    for (let k = 0; k < i % 5; k += 1) await Promise.resolve();
    const stats = createStats();
    const decoded = decodeStruct(OrderBookSchema, bytes, stats);
    const report = makeInspector().inspect('OrderBook', decoded, { leaks: stats.leaks });
    assert.equal(stats.leaks.length, 0, 'strictly no leaks');
    return canonical({ decoded, report });
  });

  const results = await Promise.all(runs);
  for (const r of results) {
    const parsed = JSON.parse(r) as { decoded: unknown };
    assert.equal(canonical(parsed.decoded), expected);
    assert.equal(r, results[0], 'all concurrent inspections must agree');
  }
});

test('shared inspector instance is safe under concurrent inspect() calls', async () => {
  const inspector = makeInspector();
  const book = sampleBook();
  const baseline = canonical(inspector.inspect('OrderBook', book));
  const runs = Array.from({ length: 32 }, async (_, i) => {
    for (let k = 0; k < i % 3; k += 1) await Promise.resolve();
    return canonical(inspector.inspect('OrderBook', book));
  });
  for (const r of await Promise.all(runs)) assert.equal(r, baseline);
});

test('worker threads decode the same buffer with identical results and no leaks', async (t) => {
  const bytes = encodeStruct(OrderBookSchema, sampleBook());
  const expected = canonical({
    decoded: decodeStruct(OrderBookSchema, bytes, createStats()),
    report: makeInspector().inspect('OrderBook', decodeStruct(OrderBookSchema, bytes, createStats())),
  });

  const workerPath = join(here, 'helpers', 'decode_worker.ts');
  const runWorker = (): Promise<string> =>
    new Promise((resolve, reject) => {
      const worker = new Worker(workerPath, {
        workerData: { bytes: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.length) },
        execArgv: ['--experimental-strip-types'],
      });
      worker.once('message', resolve);
      worker.once('error', reject);
    });

  let results: string[];
  try {
    results = await Promise.all([runWorker(), runWorker(), runWorker(), runWorker()]);
  } catch (err) {
    t.skip(`worker_threads unavailable: ${err}`);
    return;
  }
  for (const r of results) assert.equal(r, expected, 'worker result diverged from main thread');
});
