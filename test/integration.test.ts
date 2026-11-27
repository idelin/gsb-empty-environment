import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CumulativeToDeltaProcessor,
  DeltaToCumulativeProcessor,
  createTemporalityProcessor,
  seriesKey,
} from '../src/index.js';
import { asHistogram, histogramPoint, metric, ns, numberPoint, numberValue } from './helpers.js';

describe('round-trip conservation', () => {
  test('cumulative -> delta -> cumulative reproduces the original values within a lifecycle', () => {
    const toDelta = new CumulativeToDeltaProcessor();
    const toCumulative = new DeltaToCumulativeProcessor();

    const cumulativePoints = [
      numberPoint(ns(100), 5, { startTime: ns(0) }),
      numberPoint(ns(200), 12, { startTime: ns(0) }),
      numberPoint(ns(300), 20, { startTime: ns(0) }),
      numberPoint(ns(400), 31, { startTime: ns(0) }),
    ];

    const restored: number[] = [];
    for (const point of cumulativePoints) {
      const deltaMetric = toDelta.process(
        metric('requests', 'COUNTER', 'CUMULATIVE', [point]),
      );
      if (deltaMetric.dataPoints.length === 0) {
        continue;
      }
      const cumulativeMetric = toCumulative.process({
        ...deltaMetric,
        temporality: 'DELTA',
      });
      restored.push(numberValue(cumulativeMetric.dataPoints[0]!));
    }

    assert.deepEqual(restored, [7, 15, 26]);
    const originalDeltas = cumulativePoints
      .slice(1)
      .map((point, index) => point.value - cumulativePoints[index]!.value);
    assert.deepEqual(restored, originalDeltas.map((_, index) => originalDeltas.slice(0, index + 1).reduce((a, b) => a + b, 0)));
  });

  test('delta -> cumulative -> delta preserves counter increments across a bounds reset', () => {
    const toCumulative = new DeltaToCumulativeProcessor();
    const toDelta = new CumulativeToDeltaProcessor();

    const first = toCumulative.process(
      metric('latency', 'HISTOGRAM', 'DELTA', [
        histogramPoint(ns(100), [10], [2, 3], { startTime: ns(0) }),
        histogramPoint(ns(200), [10], [1, 4], { startTime: ns(100) }),
      ]),
    );
    assert.deepEqual(first.dataPoints.map((point) => asHistogram(point).bucketCounts), [[2, 3], [3, 7]]);

    const backToDelta = toDelta.process({ ...first, temporality: 'CUMULATIVE' });
    assert.equal(backToDelta.dataPoints.length, 1);
    assert.deepEqual(asHistogram(backToDelta.dataPoints[0]!).bucketCounts, [1, 4]);
  });
});

describe('gauge passthrough and validation', () => {
  test('gauges pass through untouched regardless of input temporality', () => {
    const cumulativeProcessor = new CumulativeToDeltaProcessor();
    const output = cumulativeProcessor.process(
      metric('temperature', 'GAUGE', 'CUMULATIVE', [
        numberPoint(ns(100), 21.5),
        numberPoint(ns(200), 19.0),
      ]),
    );
    assert.equal(output.temporality, 'CUMULATIVE');
    assert.deepEqual(output.dataPoints.map((point) => numberValue(point)), [21.5, 19.0]);
    assert.equal(cumulativeProcessor.getStats().gaugePassThrough, 2);
    assert.equal(cumulativeProcessor.seriesCount, 0);
  });

  test('wrong input temporality is rejected explicitly', () => {
    assert.throws(
      () =>
        new CumulativeToDeltaProcessor().process(
          metric('requests', 'COUNTER', 'DELTA', [numberPoint(ns(100), 1)]),
        ),
      /only accepts CUMULATIVE input/,
    );
    assert.throws(
      () =>
        new DeltaToCumulativeProcessor().process(
          metric('requests', 'COUNTER', 'CUMULATIVE', [numberPoint(ns(100), 1)]),
        ),
      /only accepts DELTA input/,
    );
  });

  test('malformed histograms are rejected before touching state', () => {
    const processor = new CumulativeToDeltaProcessor();
    assert.throws(
      () =>
        processor.process(
          metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
            histogramPoint(ns(100), [10, 5], [1, 2, 3]),
          ]),
        ),
      /strictly increasing/,
    );
    assert.throws(
      () =>
        processor.process(
          metric('latency', 'HISTOGRAM', 'CUMULATIVE', [
            histogramPoint(ns(100), [10, 100], [1, 2, 5], { count: 5 }),
          ]),
        ),
      /does not match sum of bucket counts/,
    );
    assert.equal(processor.seriesCount, 0);
  });

  test('invalid maxSeries is rejected', () => {
    assert.throws(() => new CumulativeToDeltaProcessor({ maxSeries: 0 }), /positive integer/);
    assert.throws(() => new CumulativeToDeltaProcessor({ maxSeries: 1.5 }), /positive integer/);
  });
});

describe('factory and series keys', () => {
  test('factory selects the matching processor', () => {
    assert.ok(createTemporalityProcessor('DELTA') instanceof CumulativeToDeltaProcessor);
    assert.ok(createTemporalityProcessor('CUMULATIVE') instanceof DeltaToCumulativeProcessor);
  });

  test('series keys are order-independent and collision-safe', () => {
    const keyA = seriesKey('m', { host: 'a', region: 'r' });
    const keyB = seriesKey('m', { region: 'r', host: 'a' });
    assert.equal(keyA, keyB);
    assert.notEqual(seriesKey('m', { host: 'a' }), seriesKey('m', { host: 'b' }));
    assert.notEqual(seriesKey('m', { host: 'a,b' }), seriesKey('m', { host: 'a', b: '' }));
    assert.equal(seriesKey('m'), seriesKey('m', undefined));
  });

  test('resetStats returns a snapshot and zeroes the live counters', () => {
    const processor = new CumulativeToDeltaProcessor();
    processor.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 1, { startTime: ns(0) }),
      ]),
    );
    const snapshot = processor.resetStats();
    assert.equal(snapshot.firstPointBaselines, 1);
    assert.equal(processor.getStats().firstPointBaselines, 0);
    assert.equal(processor.seriesCount, 1);
  });

  test('stats conserve input = output + dropped + swallowed baselines', () => {
    const c2d = new CumulativeToDeltaProcessor();
    c2d.process(
      metric('requests', 'COUNTER', 'CUMULATIVE', [
        numberPoint(ns(100), 1, { startTime: ns(0) }),
        numberPoint(ns(200), 4, { startTime: ns(0) }),
        numberPoint(ns(150), 99, { startTime: ns(0) }),
      ]),
    );
    const c2dStats = c2d.getStats();
    assert.equal(c2dStats.inputPoints, 3);
    assert.equal(
      c2dStats.outputPoints + c2dStats.droppedOutOfOrder + c2dStats.firstPointBaselines,
      3,
    );
    assert.equal(c2dStats.outputPoints, 1);
    assert.equal(c2dStats.droppedOutOfOrder, 1);

    const d2c = new DeltaToCumulativeProcessor();
    d2c.process(
      metric('requests', 'COUNTER', 'DELTA', [
        numberPoint(ns(100), 1),
        numberPoint(ns(200), 4),
        numberPoint(ns(150), 99),
      ]),
    );
    const d2cStats = d2c.getStats();
    assert.equal(d2cStats.inputPoints, 3);
    assert.equal(d2cStats.outputPoints + d2cStats.droppedOutOfOrder, 3);
    assert.equal(d2cStats.outputPoints, 2);
  });
});
