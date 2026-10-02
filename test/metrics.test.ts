import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  BufferWriter, LatencyHistogram, MetricsRegistry, ProtoEngine, WireType,
  exportJson, exportText,
  type MessageDescriptor,
} from '../src/index.ts';

const Doc: MessageDescriptor = {
  name: 'Doc',
  fields: [
    { number: 1, name: 'id', type: 'uint64', required: true },
    { number: 2, name: 'title', type: 'string' },
  ],
};

function docBytes(id: bigint): Uint8Array {
  return new BufferWriter().writeTag(1, WireType.Varint).writeVarint(id).finish();
}

test('histogram buckets boundaries: 0, 1, powers of two, overflow', () => {
  const h = new LatencyHistogram();
  for (const v of [0, 1, 2, 3, 4, 1024, 1025, 2 ** 40]) h.record(v);
  const s = h.snapshot();
  assert.equal(s.count, 8);
  assert.equal(s.minNs, 0);
  assert.equal(s.maxNs, 2 ** 40);
  assert.equal(s.sumNs, 0 + 1 + 2 + 3 + 4 + 1024 + 1025 + 2 ** 40);
  // bucket le=1 catches {0,1}; le=2 catches {2}; le=4 catches {3,4}; le=1024 catches {1024}; le=2048 catches {1025}
  const byLe = new Map(s.buckets.map((b) => [b.leNs, b.count]));
  assert.equal(byLe.get(1), 2);
  assert.equal(byLe.get(2), 1);
  assert.equal(byLe.get(4), 2);
  assert.equal(byLe.get(1024), 1);
  assert.equal(byLe.get(2048), 1);
  assert.equal(byLe.get(2 ** 40), 1);
});

test('empty histogram snapshot is all zeros', () => {
  const s = new LatencyHistogram().snapshot();
  assert.equal(s.count, 0);
  assert.equal(s.minNs, 0);
  assert.equal(s.maxNs, 0);
  assert.equal(s.meanNs, 0);
  assert.deepEqual(s.percentiles, { p50: 0, p90: 0, p95: 0, p99: 0 });
});

test('percentiles land within recorded range', () => {
  const h = new LatencyHistogram();
  for (let i = 0; i < 100; i++) h.record(1000 + i);
  const s = h.snapshot();
  for (const p of Object.values(s.percentiles)) {
    assert.ok(p >= 0 && p <= s.maxNs, `percentile ${p} out of range`);
  }
  assert.ok(s.percentiles.p99 >= s.percentiles.p50);
});

test('negative latency samples clamp to zero bucket', () => {
  const h = new LatencyHistogram();
  h.record(-5);
  const s = h.snapshot();
  assert.equal(s.count, 1);
  assert.equal(s.minNs, 0);
});

test('hit rate ratio, counters, gauges aggregate correctly', () => {
  const r = new MetricsRegistry();
  r.recordHit('cache');
  r.recordHit('cache');
  r.recordMiss('cache');
  assert.deepEqual(r.hitRate('cache'), { hits: 2, misses: 1, ratio: 2 / 3 });
  assert.deepEqual(r.hitRate('untouched'), { hits: 0, misses: 0, ratio: 0 });

  r.incCounter('c');
  r.incCounter('c', 4);
  assert.equal(r.getCounter('c'), 5);
  r.setGauge('g', 10);
  r.incGauge('g', -3);
  assert.equal(r.getGauge('g'), 7);
});

test('timer records real nanosecond latency via hrtime', () => {
  const r = new MetricsRegistry();
  const stop = r.startTimer('op');
  const elapsed = stop();
  assert.ok(elapsed >= 0n);
  const s = r.snapshot();
  assert.equal(s.histograms.op.count, 1);
  assert.equal(s.histograms.op.sumNs, Number(elapsed));
});

test('exportJson/exportText contain aggregated statistics', () => {
  const engine = new ProtoEngine();
  engine.decode(Doc, docBytes(1n));
  engine.decode(Doc, docBytes(2n));
  engine.decode(Doc, new Uint8Array([0x08])); // truncated varint -> error
  const json = exportJson(engine.metrics);
  assert.equal(json.counters.decode_messages_total, 2);
  assert.equal(json.counters.decode_errors_total, 1);
  assert.equal(json.counters.decode_error_ERR_TRUNCATED, 1);
  assert.equal(json.gauges.decode_active, 0);
  assert.equal(json.hitRates.schema_cache.hits, 2);
  assert.equal(json.hitRates.schema_cache.misses, 1);
  assert.equal(json.histograms.decode_latency.count, 3);
  assert.equal(json.histograms.validate_latency.count, 2);

  const text = exportText(engine.metrics);
  assert.match(text, /decode_messages_total 2/);
  assert.match(text, /decode_error_ERR_TRUNCATED 1/);
  assert.match(text, /schema_cache_ratio 0\.66/);
  assert.match(text, /decode_latency_p99_ns \d+/);
});

test('registry reset clears all series', () => {
  const r = new MetricsRegistry();
  r.incCounter('c');
  r.setGauge('g', 1);
  r.recordHit('h');
  r.histogram('lat').record(5);
  r.reset();
  const s = r.snapshot();
  assert.deepEqual(s.counters, {});
  assert.deepEqual(s.gauges, {});
  assert.deepEqual(s.hitRates, {});
  assert.equal(s.histograms.lat.count, 0);
});

test('concurrent decodes keep counters exact and gauges balanced', async () => {
  const engine = new ProtoEngine();
  const N = 512;
  const good = docBytes(7n);
  const bad = new Uint8Array([0x08]); // truncated
  const tasks: Promise<unknown>[] = [];
  for (let i = 0; i < N; i++) {
    // Interleave microtask scheduling to maximise async interleaving.
    tasks.push(Promise.resolve().then(() => engine.decode(Doc, i % 3 === 0 ? bad : good)));
  }
  const results = await Promise.all(tasks);
  const errors = results.filter((r) => !(r as { ok: boolean }).ok).length;
  const expectedErrors = Math.ceil(N / 3);
  assert.equal(errors, expectedErrors);

  const snap = engine.exportMetrics();
  assert.equal(snap.counters.decode_messages_total, N - expectedErrors);
  assert.equal(snap.counters.decode_errors_total, expectedErrors);
  assert.equal(snap.histograms.decode_latency.count, N);
  assert.equal(snap.gauges.decode_active, 0, 'gauge must return to zero after concurrency');
  assert.equal(snap.hitRates.schema_cache.hits + snap.hitRates.schema_cache.misses, N);
});

test('no leaks: no new active handles/requests and buffers released', async () => {
  const proc = process as unknown as {
    _getActiveHandles: () => unknown[];
    _getActiveRequests: () => unknown[];
  };
  const handlesBefore = proc._getActiveHandles().length;
  const requestsBefore = proc._getActiveRequests().length;

  const engine = new ProtoEngine();
  for (let i = 0; i < 200; i++) {
    engine.decode(Doc, docBytes(BigInt(i)));
    engine.decode(Doc, new Uint8Array([0xff])); // error path also releases
  }
  await new Promise((resolve) => setImmediate(resolve));

  assert.ok(proc._getActiveHandles().length <= handlesBefore, 'active handles grew');
  assert.ok(proc._getActiveRequests().length <= requestsBefore, 'active requests grew');
  assert.equal(engine.metrics.getGauge('decode_active'), 0, 'gauge leaked');
});

test('reader release is observable and engine never retains input buffers', () => {
  const engine = new ProtoEngine();
  const bytes = docBytes(1n);
  const out = engine.decode(Doc, bytes);
  assert.equal(out.ok, true);
  // The engine stores no reference to the input: mutating it afterwards
  // must not change the decoded result.
  bytes[1] = 0x7f;
  assert.equal((out.value as Record<string, unknown>).id, 1n);
});
