import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Counter, Gauge, Histogram, HitRate, MetricRegistry, Timer } from '../src/metrics.ts';

test('counter increments and rejects negative deltas', () => {
  const c = new Counter();
  c.inc();
  c.inc(4);
  assert.equal(c.value, 5);
  assert.throws(() => c.inc(-1), RangeError);
  c.reset();
  assert.equal(c.value, 0);
});

test('gauge set/inc/dec', () => {
  const g = new Gauge();
  g.set(10);
  g.inc(5);
  g.dec(3);
  assert.equal(g.value, 12);
});

test('histogram buckets, min/max/mean and percentiles', () => {
  const h = new Histogram([1, 5, 10]);
  for (const v of [0.5, 2, 3, 7, 9, 20]) h.observe(v);
  const s = h.snapshot([50, 90, 99]);
  assert.equal(s.count, 6);
  assert.equal(s.sum, 41.5);
  assert.equal(s.min, 0.5);
  assert.equal(s.max, 20);
  assert.ok(Math.abs(s.mean - 41.5 / 6) < 1e-9);
  assert.deepEqual(s.buckets, [
    { le: 1, count: 1 },
    { le: 5, count: 3 },
    { le: 10, count: 5 },
  ]);
  assert.ok(s.percentiles.p50 >= 1 && s.percentiles.p50 <= 5);
  assert.ok(s.percentiles.p99 <= 20);
  assert.ok(s.percentiles.p90 >= s.percentiles.p50);
});

test('histogram edge cases: empty, single value, boundary values', () => {
  const h = new Histogram([1, 10]);
  const empty = h.snapshot();
  assert.equal(empty.count, 0);
  assert.equal(empty.min, 0);
  assert.equal(empty.max, 0);
  assert.equal(empty.percentiles.p50, 0);

  h.observe(1); // exactly on a bucket bound goes into that bucket
  const s = h.snapshot();
  assert.deepEqual(s.buckets, [
    { le: 1, count: 1 },
    { le: 10, count: 1 },
  ]);
  assert.equal(h.percentile(0), 1);
  assert.equal(h.percentile(100), 1);

  assert.throws(() => h.observe(NaN), RangeError);
  assert.throws(() => h.observe(Infinity), RangeError);
  assert.throws(() => new Histogram([]), RangeError);
});

test('hit rate tracks hits/misses and handles zero traffic', () => {
  const hr = new HitRate();
  assert.equal(hr.rate, 0);
  hr.hit();
  hr.hit();
  hr.miss();
  assert.equal(hr.total, 3);
  assert.ok(Math.abs(hr.rate - 2 / 3) < 1e-9);
  hr.reset();
  assert.equal(hr.rate, 0);
});

test('registry deduplicates series by name+labels', () => {
  const reg = new MetricRegistry();
  reg.counter('ops', { kind: 'a' }).inc();
  reg.counter('ops', { kind: 'a' }).inc(2);
  reg.counter('ops', { kind: 'b' }).inc();
  const snap = reg.snapshot();
  assert.equal(snap.counters['ops{kind=a}'], 3);
  assert.equal(snap.counters['ops{kind=b}'], 1);
  assert.equal(reg.seriesCount, 2);
});

test('registry capacity guard prevents label-cardinality leaks', () => {
  const reg = new MetricRegistry({ maxSeriesPerKind: 4 });
  for (let i = 0; i < 4; i++) reg.counter('c', { i: String(i) }).inc();
  assert.throws(() => reg.counter('c', { i: 'x' }), /capacity/);
});

test('error code counter tallies and resets', () => {
  const reg = new MetricRegistry();
  reg.errorCodes.record('ERR_TRUNCATED');
  reg.errorCodes.record('ERR_TRUNCATED');
  reg.errorCodes.record('ERR_INVALID_TAG');
  assert.equal(reg.errorCodes.get('ERR_TRUNCATED'), 2);
  assert.equal(reg.errorCodes.total, 3);
  reg.reset();
  assert.equal(reg.errorCodes.total, 0);
  assert.equal(reg.seriesCount, 0);
});

test('timeSync records latency even when fn throws', () => {
  const reg = new MetricRegistry();
  assert.throws(() =>
    reg.timeSync('lat', () => {
      throw new Error('boom');
    }),
  );
  assert.equal(reg.histogram('lat').count, 1);
});

test('timeAsync records latency for async work', async () => {
  const reg = new MetricRegistry();
  const out = await reg.timeAsync('lat_async', async () => {
    await new Promise((r) => setTimeout(r, 5));
    return 42;
  });
  assert.equal(out, 42);
  const s = reg.histogram('lat_async').snapshot();
  assert.equal(s.count, 1);
  assert.ok(s.min >= 4, `expected >= 4ms, got ${s.min}`);
});

test('Timer measures monotonic elapsed time', async () => {
  const t = new Timer();
  await new Promise((r) => setTimeout(r, 5));
  assert.ok(t.elapsedMs() >= 4);
});

test('concurrent async tasks never lose increments', async () => {
  const reg = new MetricRegistry();
  const TASKS = 64;
  const PER_TASK = 500;
  await Promise.all(
    Array.from({ length: TASKS }, async (_, i) => {
      for (let j = 0; j < PER_TASK; j++) {
        reg.counter('ops').inc();
        reg.hitRate('hr').hit();
        reg.histogram('lat').observe(j % 10);
        if (j % 100 === 0) await Promise.resolve(); // force interleaving
        if (i % 2 === 0) reg.errorCodes.record('E1');
      }
    }),
  );
  assert.equal(reg.counter('ops').value, TASKS * PER_TASK);
  assert.equal(reg.hitRate('hr').total, TASKS * PER_TASK);
  assert.equal(reg.histogram('lat').count, TASKS * PER_TASK);
  assert.equal(reg.errorCodes.get('E1'), (TASKS / 2) * PER_TASK);
  // no series leak: exactly one series per kind
  assert.equal(reg.seriesCount, 3);
});

test('export JSON and text are consistent with snapshot', () => {
  const reg = new MetricRegistry();
  reg.counter('decode_total', { message: 'Person' }).inc(3);
  reg.histogram('decode_latency_ms').observe(0.4);
  reg.hitRate('field_hit_rate').hit();
  reg.errorCodes.record('ERR_TRUNCATED');

  const parsed = JSON.parse(reg.exportJSON());
  assert.equal(parsed.counters['decode_total{message=Person}'], 3);
  assert.equal(parsed.errorCodes.ERR_TRUNCATED, 1);
  assert.equal(parsed.seriesCount, 3);

  const text = reg.exportText();
  assert.match(text, /decode_total\{message=Person\} 3/);
  assert.match(text, /decode_latency_ms_bucket\{le="\+Inf"\} 1/);
  assert.match(text, /error_code_total\{code="ERR_TRUNCATED"\} 1/);
});

test('reset leaves no leaked series and memory stays bounded under churn', () => {
  const reg = new MetricRegistry();
  for (let round = 0; round < 100; round++) {
    for (let i = 0; i < 50; i++) reg.counter('churn', { round: String(i) }).inc();
    assert.ok(reg.seriesCount <= 50);
    reg.reset();
    assert.equal(reg.seriesCount, 0);
  }
});
