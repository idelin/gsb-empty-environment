import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, readFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { MetricsRegistry } from '../src/metrics/registry.ts';
import { Aggregator, computeStats, percentile } from '../src/metrics/aggregator.ts';
import { toJSON, toPrometheus, exportToFile } from '../src/metrics/exporter.ts';

test('Counter：递增、批量增加、标签隔离、非法输入', () => {
  const registry = new MetricsRegistry();
  const c = registry.counter('ops_total');
  c.inc();
  c.inc(4);
  assert.equal(c.value(), 5);

  const byPhaseA = registry.counter('ops_total', { phase: 'redo' });
  const byPhaseB = registry.counter('ops_total', { phase: 'undo' });
  byPhaseA.inc(2);
  byPhaseB.inc(3);
  assert.equal(registry.counter('ops_total', { phase: 'redo' }).value(), 2);
  assert.equal(registry.counter('ops_total', { phase: 'undo' }).value(), 3);
  assert.equal(registry.counter('ops_total').value(), 5, 'labelled series are independent');

  assert.throws(() => c.inc(-1));
  assert.throws(() => c.inc(Number.NaN));
  // Same name + labels returns the same series (no duplicates).
  assert.equal(registry.counter('ops_total', { phase: 'redo' }), byPhaseA);
});

test('Gauge：set/inc/dec 与非法值', () => {
  const registry = new MetricsRegistry();
  const g = registry.gauge('dpt_size');
  g.set(10);
  g.inc(5);
  g.dec(3);
  assert.equal(g.value(), 12);
  assert.throws(() => g.set(Number.POSITIVE_INFINITY));
});

test('Histogram：空快照与桶边界（le 含等号）', () => {
  const registry = new MetricsRegistry();
  const h = registry.histogram('latency_ns', [10, 50, 100]);
  assert.equal(h.count(), 0);
  assert.equal(h.sum(), 0);
  assert.deepEqual(
    h.bucketSnapshot(),
    [
      { le: 10, count: 0 },
      { le: 50, count: 0 },
      { le: 100, count: 0 },
    ],
  );

  h.record(10); // exactly on the first boundary
  h.record(50); // exactly on the second
  h.record(101); // above all buckets
  h.record(1);
  assert.deepEqual(h.bucketSnapshot(), [
    { le: 10, count: 2 },
    { le: 50, count: 3 },
    { le: 100, count: 3 },
  ]);
  assert.equal(h.count(), 4);
  assert.equal(h.sum(), 162);
  assert.throws(() => h.record(Number.NaN));
  assert.throws(() => registry.histogram('bad', [10, 5]), 'buckets must be increasing');
});

test('percentile：空、单样本、插值与边界', () => {
  assert.equal(percentile([], 50), null);
  assert.equal(percentile([5], 0), 5);
  assert.equal(percentile([5], 50), 5);
  assert.equal(percentile([5], 99), 5);
  const values = [10, 20, 30, 40];
  assert.equal(percentile(values, 0), 10);
  assert.equal(percentile(values, 100), 40);
  assert.equal(percentile(values, 50), 25, 'linear interpolation between ranks');
  assert.equal(percentile(values, 25), 17.5);
});

test('computeStats：已知分布的均值/标准差/分位数', () => {
  const stats = computeStats([2, 4, 4, 4, 5, 5, 7, 9]);
  assert.equal(stats.count, 8);
  assert.equal(stats.min, 2);
  assert.equal(stats.max, 9);
  assert.equal(stats.mean, 5);
  assert.equal(stats.stddev, 2, 'population stddev of the classic textbook sample');
  assert.equal(stats.p50, 4.5);

  const empty = computeStats([]);
  assert.equal(empty.count, 0);
  assert.equal(empty.mean, null);
  assert.equal(empty.p99, null);
  assert.equal(empty.ratePerSecond, 0);
});

test('Timer：纳秒级计时并保留函数返回值', () => {
  const registry = new MetricsRegistry();
  const h = registry.histogram('fn_duration_ns');
  const result = registry.time(h, () => 42);
  assert.equal(result, 42);
  assert.equal(h.count(), 1);
  assert.ok(h.sum() > 0);

  const stop = registry.startTimer(h);
  const elapsed = stop();
  assert.ok(elapsed > 0);
  assert.equal(h.count(), 2);
});

test('Aggregator：时间窗口过滤与速率', () => {
  const registry = new MetricsRegistry();
  const h = registry.histogram('op_latency_ns');
  const now = 1_000_000;
  h.record(100, now - 10_000); // outside a 5s window
  h.record(200, now - 1_000);
  h.record(400, now - 500);

  const agg = new Aggregator(registry);
  const windowed = agg.histogramStats(h, 5_000, now);
  assert.equal(windowed.count, 2, 'old sample excluded from the window');
  assert.equal(windowed.min, 200);
  assert.equal(windowed.max, 400);
  assert.equal(windowed.mean, 300);
  assert.equal(windowed.ratePerSecond, 0.4, '2 samples over 5 seconds');

  const all = agg.histogramStats(h, undefined, now);
  assert.equal(all.count, 3);

  const emptyWindow = agg.histogramStats(h, 100, now);
  assert.equal(emptyWindow.count, 0);
  assert.equal(emptyWindow.p95, null);
});

test('Aggregator：命中率派生（含 0/0 边界）', () => {
  const registry = new MetricsRegistry();
  const agg = new Aggregator(registry);

  const empty = agg.hitRate('pool', 'hits', 'misses');
  assert.equal(empty.hitRate, null, 'no lookups -> null, not NaN');

  registry.counter('hits').inc(3);
  registry.counter('misses').inc(1);
  const rate = agg.hitRate('pool', 'hits', 'misses');
  assert.equal(rate.hitRate, 0.75);
  assert.equal(rate.hits, 3);
  assert.equal(rate.misses, 1);
});

test('Exporter：JSON 结构完整', () => {
  const registry = new MetricsRegistry();
  registry.counter('requests_total', { route: '/a' }).inc(7);
  registry.gauge('queue_depth').set(3);
  registry.histogram('latency_ns', [100]).record(50);
  registry.counter('buffer_pool_hits_total').inc(9);
  registry.counter('buffer_pool_misses_total').inc(1);

  const parsed = JSON.parse(toJSON(registry));
  assert.ok(typeof parsed.timestamp === 'number');
  const counter = parsed.counters.find((c: { name: string }) => c.name === 'requests_total');
  assert.equal(counter.value, 7);
  assert.deepEqual(counter.labels, { route: '/a' });
  const hist = parsed.histograms.find((h: { name: string }) => h.name === 'latency_ns');
  assert.equal(hist.count, 1);
  assert.equal(hist.p50, 50);
  const hitRate = parsed.hitRates.find((r: { name: string }) => r.name === 'buffer_pool');
  assert.equal(hitRate.hitRate, 0.9);
});

test('Exporter：Prometheus 文本格式与标签转义', () => {
  const registry = new MetricsRegistry();
  registry.counter('errors_total', { code: 'WAL_"CORRUPT"\n' }).inc(2);
  registry.histogram('phase_ns', [10, 100], { phase: 'redo' }).record(50);

  const text = toPrometheus(registry);
  assert.match(text, /# TYPE errors_total counter/);
  assert.match(text, /errors_total\{code="WAL_\\"CORRUPT\\"\\n"\} 2/);
  assert.match(text, /# TYPE phase_ns histogram/);
  assert.match(text, /phase_ns_bucket\{phase="redo",le="10"\} 0/);
  assert.match(text, /phase_ns_bucket\{phase="redo",le="100"\} 1/);
  assert.match(text, /phase_ns_bucket\{phase="redo",le="\+Inf"\} 1/);
  assert.match(text, /phase_ns_sum\{phase="redo"\} 50/);
  assert.match(text, /phase_ns_count\{phase="redo"\} 1/);
});

test('Exporter：写文件', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'metrics-export-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const registry = new MetricsRegistry();
  registry.counter('x_total').inc(1);

  const jsonPath = join(dir, 'metrics.json');
  const promPath = join(dir, 'metrics.prom');
  exportToFile(registry, jsonPath, 'json');
  exportToFile(registry, promPath, 'prometheus');
  assert.ok(existsSync(jsonPath));
  assert.ok(existsSync(promPath));
  assert.equal(JSON.parse(readFileSync(jsonPath, 'utf8')).counters[0].value, 1);
  assert.match(readFileSync(promPath, 'utf8'), /x_total 1/);
});

test('并发安全：异步交错写入无丢失、快照不抛错', async () => {
  const registry = new MetricsRegistry();
  const counter = registry.counter('concurrent_ops_total');
  const hist = registry.histogram('concurrent_latency_ns', [10, 100]);
  const gauge = registry.gauge('inflight');
  const agg = new Aggregator(registry);

  const workers = 64;
  const perWorker = 250;
  const tasks: Promise<void>[] = [];
  for (let w = 0; w < workers; w++) {
    tasks.push(
      (async () => {
        for (let i = 0; i < perWorker; i++) {
          counter.inc();
          hist.record(i % 200);
          gauge.inc();
          if (i % 7 === 0) {
            // Interleave with readers while other tasks mutate.
            registry.snapshot();
            agg.collect(1_000);
            await Promise.resolve();
          }
          gauge.dec();
        }
      })(),
    );
  }
  await Promise.all(tasks);

  assert.equal(counter.value(), workers * perWorker, 'no lost increments');
  assert.equal(hist.count(), workers * perWorker, 'no lost samples');
  assert.equal(gauge.value(), 0, 'gauge balanced after concurrent inc/dec');
  const snapshot = registry.snapshot();
  assert.equal(
    snapshot.counters.find((c) => c.name === 'concurrent_ops_total')?.value,
    workers * perWorker,
  );
});

test('无泄漏：close 释放全部序列且不残留句柄/定时器', () => {
  const handlesBefore = (process as unknown as { _getActiveHandles(): unknown[] })._getActiveHandles().length;

  const registry = new MetricsRegistry();
  registry.counter('leak_check_total').inc(10);
  registry.gauge('leak_gauge').set(1);
  registry.histogram('leak_hist', [1, 2, 3]).record(2);
  new Aggregator(registry).collect(1000);
  toJSON(registry);
  toPrometheus(registry);
  assert.ok(registry.size() > 0);

  registry.close();
  assert.equal(registry.size(), 0, 'all series released');
  assert.equal(registry.isClosed, true);
  assert.deepEqual(registry.snapshot(), { counters: [], gauges: [], histograms: [] });
  assert.throws(() => registry.counter('after_close'), 'closed registry rejects new series');

  const handlesAfter = (process as unknown as { _getActiveHandles(): unknown[] })._getActiveHandles().length;
  assert.equal(handlesAfter, handlesBefore, 'no leaked handles (timers included)');
});
