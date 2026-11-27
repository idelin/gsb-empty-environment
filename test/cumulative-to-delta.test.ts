import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { CumulativeToDeltaProcessor } from '../src/index.js';
import { asHistogram, histogramPoint, metric, ns, numberPoint, numberValue } from './helpers.js';

describe('CumulativeToDeltaProcessor - monotonic counter', () => {
  test('first point only establishes a baseline and emits nothing by default', () => {
    const processor = new CumulativeToDeltaProcessor();
    const output = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 10, { startTime: ns(0) }),
      ]),
    );
    assert.equal(output.temporality, 'DELTA');
    assert.equal(output.dataPoints.length, 0);
    assert.equal(processor.getStats().firstPointBaselines, 1);
  });

  test('initialPointAsDelta emits first segment only when startTime < timestamp', () => {
    const processor = new CumulativeToDeltaProcessor({ initialPointAsDelta: true });
    const output = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 10, { startTime: ns(50) }),
      ]),
    );
    assert.equal(output.dataPoints.length, 1);
    assert.equal(numberValue(output.dataPoints[0]!), 10);
    assert.equal(output.dataPoints[0]!.restart, true);

    const equalTimes = new CumulativeToDeltaProcessor({ initialPointAsDelta: true });
    const suppressed = equalTimes.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 10, { startTime: ns(100) }),
      ]),
    );
    assert.equal(suppressed.dataPoints.length, 0);
  });

  test('subsequent points emit the positive difference', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 10, { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(200), 25, { startTime: ns(0) }),
      ]),
    );
    assert.equal(output.dataPoints.length, 1);
    const point = output.dataPoints[0]!;
    assert.equal(numberValue(point), 15);
    assert.equal(point.startTime, ns(100));
    assert.equal(point.timestamp, ns(200));
    assert.equal(point.restart, false);
  });

  test('startTime change is a restart: full value, never a negative delta', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 100, { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(200), 3, { startTime: ns(150) }),
      ]),
    );
    const point = output.dataPoints[0]!;
    assert.equal(numberValue(point), 3);
    assert.equal(point.restart, true);
    assert.equal(point.resetReason, 'restart_start_time');
    assert.equal(point.startTime, ns(150));
    assert.ok(numberValue(point) >= 0, 'restart must never produce a negative value');
    assert.equal(processor.getStats().restartEmissions, 1);
  });

  test('value decrease without startTime change is a restart anchored at old timestamp', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 100, { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(200), 4, { startTime: ns(0) }),
      ]),
    );
    const point = output.dataPoints[0]!;
    assert.equal(numberValue(point), 4);
    assert.equal(point.restart, true);
    assert.equal(point.resetReason, 'restart_value_decrease');
    assert.equal(point.startTime, ns(100));
    assert.equal(processor.getStats().resetReasons.restart_value_decrease, 1);
  });

  test('late/out-of-order points are dropped and never treated as restarts', () => {
    const drops: string[] = [];
    const processor = new CumulativeToDeltaProcessor({
      onDrop: (info) => drops.push(info.reason),
    });
    processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 10, { startTime: ns(0) }),
        numberPoint(ns(200), 20, { startTime: ns(0) }),
      ]),
    );
    const late = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(150), 99, { startTime: ns(0) }),
      ]),
    );
    assert.equal(late.dataPoints.length, 0);
    const equal = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(200), 99, { startTime: ns(0) }),
      ]),
    );
    assert.equal(equal.dataPoints.length, 0);
    assert.equal(processor.getStats().droppedOutOfOrder, 2);
    assert.deepEqual(drops, ['out_of_order', 'out_of_order']);

    const after = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(300), 35, { startTime: ns(0) }),
      ]),
    );
    assert.equal(numberValue(after.dataPoints[0]!), 15);
  });
});

describe('CumulativeToDeltaProcessor - non-monotonic UpDownCounter', () => {
  test('diffs algebraically and never treats decreases as restarts', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('queue_size', 'UP_DOWN_COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 10, { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('queue_size', 'UP_DOWN_COUNTER', 'CUMULATIVE', [
        numberPoint(ns(200), 4, { startTime: ns(0) }),
        numberPoint(ns(300), 9, { startTime: ns(0) }),
      ]),
    );
    assert.deepEqual(output.dataPoints.map((point) => numberValue(point)), [-6, 5]);
    assert.ok(output.dataPoints.every((point) => point.restart === false));
    assert.equal(processor.getStats().restartEmissions, 0);
  });

  test('does not mutate the caller-owned input points or arrays', () => {
    const processor = new CumulativeToDeltaProcessor();
    const input = metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
      histogramPoint(ns(100), [10, 100], [1, 2, 3], { startTime: ns(0), sum: 300 }),
      histogramPoint(ns(200), [10, 100], [2, 5, 4], { startTime: ns(0), sum: 650 }),
    ]);
    const snapshotBuckets = input.dataPoints.map((point) => [...(point as { bucketCounts: number[] }).bucketCounts]);
    processor.process(input);
    assert.deepEqual(
      input.dataPoints.map((point) => [...(point as { bucketCounts: number[] }).bucketCounts]),
      snapshotBuckets,
    );
  });
});

describe('CumulativeToDeltaProcessor - histogram', () => {
  const bounds = [10, 100];

  test('first histogram point is a baseline, later points are bucket deltas', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(100), bounds, [1, 2, 3], { startTime: ns(0), sum: 300 }),
      ]),
    );
    const output = processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(200), bounds, [2, 5, 4], { startTime: ns(0), sum: 650 }),
      ]),
    );
    const point = asHistogram(output.dataPoints[0]!);
    assert.deepEqual(point.bucketCounts, [1, 3, 1]);
    assert.equal(point.count, 5);
    assert.equal(point.sum, 350);
    assert.equal(point.restart, false);
    assert.equal(point.startTime, ns(100));
  });

  test('any bucket decrease is a restart emitting the full new histogram', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(100), bounds, [10, 20, 30], { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(200), bounds, [1, 0, 2], { startTime: ns(0) }),
      ]),
    );
    const point = asHistogram(output.dataPoints[0]!);
    assert.deepEqual(point.bucketCounts, [1, 0, 2]);
    assert.equal(point.restart, true);
    assert.equal(point.resetReason, 'restart_value_decrease');
    assert.ok(point.bucketCounts.every((count) => count >= 0));
  });

  test('startTime change on a histogram is a restart with the new startTime', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(100), bounds, [10, 20, 30], { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(200), bounds, [1, 1, 1], { startTime: ns(180) }),
      ]),
    );
    const point = asHistogram(output.dataPoints[0]!);
    assert.equal(point.restart, true);
    assert.equal(point.resetReason, 'restart_start_time');
    assert.equal(point.startTime, ns(180));
  });

  test('bucket boundary change resets the series instead of misaligning buckets', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(100), [10, 100], [1, 2, 3], { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(200), [10, 50, 100], [1, 1, 1, 1], { startTime: ns(0) }),
      ]),
    );
    const point = asHistogram(output.dataPoints[0]!);
    assert.equal(point.restart, true);
    assert.equal(point.resetReason, 'histogram_bounds_change');
    assert.deepEqual(point.explicitBounds, [10, 50, 100]);
    assert.deepEqual(point.bucketCounts, [1, 1, 1, 1]);
    assert.equal(processor.getStats().histogramBoundsResets, 1);
  });

  test('late histogram points are dropped without baseline overwrite', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(100), bounds, [1, 2, 3], { startTime: ns(0) }),
        histogramPoint(ns(200), bounds, [2, 4, 6], { startTime: ns(0) }),
      ]),
    );
    const late = processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(150), bounds, [9, 9, 9], { startTime: ns(0) }),
      ]),
    );
    assert.equal(late.dataPoints.length, 0);
    assert.equal(processor.getStats().droppedOutOfOrder, 1);
    const next = processor.process(
      metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
        histogramPoint(ns(300), bounds, [3, 6, 9], { startTime: ns(0) }),
      ]),
    );
    assert.deepEqual(asHistogram(next.dataPoints[0]!).bucketCounts, [1, 2, 3]);
  });
});

describe('CumulativeToDeltaProcessor - LRU eviction', () => {
  test('evicted series re-enters as a fresh baseline with no full-value spike', () => {
    const processor = new CumulativeToDeltaProcessor({ maxSeries: 2 });
    processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 10, { startTime: ns(0), attributes: { host: 'a' } }),
        numberPoint(ns(100), 10, { startTime: ns(0), attributes: { host: 'b' } }),
      ]),
    );
    assert.equal(processor.seriesCount, 2);

    const evict = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(200), 5, { startTime: ns(0), attributes: { host: 'c' } }),
      ]),
    );
    assert.equal(evict.dataPoints.length, 0);
    assert.equal(processor.seriesCount, 2);
    assert.equal(processor.getStats().seriesEvicted, 1);

    const reentry = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(300), 999, { startTime: ns(0), attributes: { host: 'a' } }),
      ]),
    );
    assert.equal(reentry.dataPoints.length, 0);
    assert.equal(processor.getStats().evictionReentries, 1);

    const following = processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(400), 1020, { startTime: ns(0), attributes: { host: 'a' } }),
      ]),
    );
    assert.equal(numberValue(following.dataPoints[0]!), 21);
    assert.equal(following.dataPoints[0]!.restart, false);
  });

  test('deltas conserve the cumulative total across a restart lifecycle', () => {
    const processor = new CumulativeToDeltaProcessor();
    const stream = [
      numberPoint(ns(100), 10, { startTime: ns(0) }),
      numberPoint(ns(200), 30, { startTime: ns(0) }),
      numberPoint(ns(300), 5, { startTime: ns(250) }),
      numberPoint(ns(400), 12, { startTime: ns(250) }),
    ];
    const emitted: number[] = [];
    for (const point of stream) {
      const output = processor.process(
        metric('requests', 'COUNTER', 'CUMULATIVE', [point]),
      );
      for (const dataPoint of output.dataPoints) {
        emitted.push(numberValue(dataPoint));
      }
    }
    const lastCumulative = 12;
    const firstLifecycle = 30 - 10;
    assert.deepEqual(emitted, [firstLifecycle, 5, 7]);
    assert.equal(emitted.reduce((sum, value) => sum + value, 0), firstLifecycle + lastCumulative);
    assert.ok(emitted.every((value) => value >= 0));
  });
});
