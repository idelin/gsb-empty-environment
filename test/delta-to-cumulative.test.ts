import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { DeltaToCumulativeProcessor } from '../src/index.js';
import { asHistogram, histogramPoint, metric, ns, numberPoint, numberValue } from './helpers.js';

describe('DeltaToCumulativeProcessor - counters', () => {
  test('counter deltas accumulate into a monotonic cumulative series', () => {
    const processor = new DeltaToCumulativeProcessor();
    processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(100), 4, { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(200), 7, { startTime: ns(100) }),
        numberPoint(ns(300), 2, { startTime: ns(200) }),
      ]),
    );
    assert.equal(output.temporality, 'CUMULATIVE');
    assert.equal(output.dataPoints.length, 2);
    assert.equal(numberValue(output.dataPoints[0]!), 11);
    assert.equal(output.dataPoints[0]!.startTime, ns(0));
    assert.equal(numberValue(output.dataPoints[1]!), 13);
    assert.equal(output.dataPoints[1]!.restart, false);
  });

  test('UpDownCounter accepts negative deltas and sums them algebraically', () => {
    const processor = new DeltaToCumulativeProcessor();
    const values: number[] = [];
    for (const point of [
      numberPoint(ns(100), 10, { startTime: ns(0) }),
      numberPoint(ns(200), -3, { startTime: ns(100) }),
      numberPoint(ns(300), -8, { startTime: ns(200) }),
      numberPoint(ns(400), 4, { startTime: ns(300) }),
    ]) {
      const output = processor.process(
        metric('inflight', 'UP_DOWN_COUNTER', 'DELTA', [point]),
      );
      for (const dataPoint of output.dataPoints) {
        values.push(numberValue(dataPoint));
      }
    }
    assert.deepEqual(values, [10, 7, -1, 3]);
    assert.equal(processor.getStats().restartEmissions, 0);
  });

  test('late delta points are rejected without corrupting the cumulative value', () => {
    const drops: Array<{ reason: string }> = [];
    const processor = new DeltaToCumulativeProcessor({
      onDrop: (info) => drops.push({ reason: info.reason }),
    });
    processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(100), 5, { startTime: ns(0) }),
        numberPoint(ns(200), 5, { startTime: ns(100) }),
      ]),
    );
    const late = processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(150), 100, { startTime: ns(100) }),
      ]),
    );
    assert.equal(late.dataPoints.length, 0);
    assert.equal(processor.getStats().droppedOutOfOrder, 1);
    assert.deepEqual(drops, [{ reason: 'out_of_order' }]);

    const after = processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(300), 2, { startTime: ns(200) }),
      ]),
    );
    assert.equal(numberValue(after.dataPoints[0]!), 12);
  });
});

describe('DeltaToCumulativeProcessor - histogram', () => {
  const bounds = [10, 100];

  test('bucket counts, count, sum, min and max accumulate window by window', () => {
    const processor = new DeltaToCumulativeProcessor();
    processor.process(
      metric('latency', 'HISTOGRAM', 'DELTA', [
        histogramPoint(ns(100), bounds, [1, 2, 0], {
          startTime: ns(0),
          sum: 120,
          min: 2,
          max: 80,
        }),
      ]),
    );
    const output = processor.process(
      metric('latency', 'HISTOGRAM', 'DELTA', [
        histogramPoint(ns(200), bounds, [0, 1, 3], {
          startTime: ns(100),
          sum: 260,
          min: 12,
          max: 200,
        }),
      ]),
    );
    const point = asHistogram(output.dataPoints[0]!);
    assert.deepEqual(point.bucketCounts, [1, 3, 3]);
    assert.equal(point.count, 7);
    assert.equal(point.sum, 380);
    assert.equal(point.min, 2);
    assert.equal(point.max, 200);
    assert.equal(point.startTime, ns(0));
  });

  test('explicitBounds change restarts the series with a fresh startTime', () => {
    const processor = new DeltaToCumulativeProcessor();
    processor.process(
      metric('latency', 'HISTOGRAM', 'DELTA', [
        histogramPoint(ns(100), [10, 100], [1, 2, 3], { startTime: ns(0) }),
      ]),
    );
    const output = processor.process(
      metric('latency', 'HISTOGRAM', 'DELTA', [
        histogramPoint(ns(200), [10, 50, 100], [1, 1, 1, 1], { startTime: ns(150) }),
      ]),
    );
    const point = asHistogram(output.dataPoints[0]!);
    assert.equal(point.restart, true);
    assert.equal(point.resetReason, 'histogram_bounds_change');
    assert.equal(point.startTime, ns(150));
    assert.deepEqual(point.bucketCounts, [1, 1, 1, 1]);
    assert.equal(point.count, 4);
    assert.equal(processor.getStats().histogramBoundsResets, 1);

    const following = processor.process(
      metric('latency', 'HISTOGRAM', 'DELTA', [
        histogramPoint(ns(300), [10, 50, 100], [1, 0, 0, 1], { startTime: ns(200) }),
      ]),
    );
    assert.deepEqual(asHistogram(following.dataPoints[0]!).bucketCounts, [2, 1, 1, 2]);
    assert.equal(following.dataPoints[0]!.startTime, ns(150));
  });
});

describe('DeltaToCumulativeProcessor - LRU eviction', () => {
  test('evicted series restarts at the arriving window startTime, never accumulating from zero', () => {
    const processor = new DeltaToCumulativeProcessor({ maxSeries: 1 });

    processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(100), 5, { startTime: ns(0), attributes: { host: 'a' } }),
      ]),
    );
    processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(200), 9, { startTime: ns(100), attributes: { host: 'b' } }),
      ]),
    );
    assert.equal(processor.getStats().seriesEvicted, 1);

    const reentry = processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(300), 7, { startTime: ns(200), attributes: { host: 'a' } }),
      ]),
    );
    const point = reentry.dataPoints[0]!;
    assert.equal(numberValue(point), 7);
    assert.equal(point.startTime, ns(200));
    assert.equal(point.restart, true);
    assert.equal(point.resetReason, 'eviction_reentry');
    assert.equal(processor.getStats().evictionReentries, 1);

    const following = processor.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(400), 3, { startTime: ns(300), attributes: { host: 'a' } }),
      ]),
    );
    assert.equal(numberValue(following.dataPoints[0]!), 10);
    assert.equal(following.dataPoints[0]!.startTime, ns(200));
  });

  test('cumulative values conserve the sum of all deltas within each lifecycle', () => {
    const processor = new DeltaToCumulativeProcessor();
    const deltas = [4, 6, -2, 3];
    let cumulative = 0;
    const results: number[] = [];
    deltas.forEach((delta, index) => {
      const timestamp = ns(100 * (index + 1));
      const output = processor.process(
        metric('queue', 'UP_DOWN_COUNTER', 'DELTA', [
          numberPoint(timestamp, delta, { startTime: ns(100 * index) }),
        ]),
      );
      cumulative += delta;
      results.push(numberValue(output.dataPoints[0]!));
    });
    assert.deepEqual(results, deltas.map((_, index) => deltas.slice(0, index + 1).reduce((a, b) => a + b, 0)));
    assert.equal(results.at(-1), deltas.reduce((a, b) => a + b, 0));
    assert.equal(cumulative, 11);
  });
});
