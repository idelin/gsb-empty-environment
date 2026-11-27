import { BaseProcessor } from './base-processor.js';
import {
  asHistogramPoint,
  asNumberPoint,
  boundsEqual,
  clonePoint,
  isHistogram,
  validateMetric,
} from './validate.js';
import type {
  DataPoint,
  HistogramDataPoint,
  Metric,
  OutputDataPoint,
  OutputMetric,
  ProcessorOptions,
  ResetReason,
} from './types.js';
import { seriesKey } from './series.js';

interface NumberSeriesState {
  kind: 'number';
  cumulative: number;
  startTime: bigint;
  lastTimestamp: bigint;
}

interface HistogramSeriesState {
  kind: 'histogram';
  bounds: number[];
  bucketCounts: number[];
  count: number;
  sum?: number;
  min?: number;
  max?: number;
  startTime: bigint;
  lastTimestamp: bigint;
}

type SeriesState = NumberSeriesState | HistogramSeriesState;

/**
 * Stateful Delta -> Cumulative conversion.
 *
 * COUNTER deltas accumulate monotonically while UP_DOWN_COUNTER deltas may be
 * negative and are summed algebraically. Histogram accumulation requires an
 * element-by-element explicitBounds match; a boundary change restarts the
 * series with a fresh startTime instead of misaligning buckets. Late points
 * are rejected. An LRU-evicted series restarts at the arriving window start so
 * a backend never misreads the implicit zero as a continuation.
 */
export class DeltaToCumulativeProcessor extends BaseProcessor {
  constructor(options: ProcessorOptions = {}) {
    super(options);
  }

  process(metric: Metric): OutputMetric {
    validateMetric(metric, 'DELTA');

    const outputPoints: OutputDataPoint[] = [];
    for (const point of metric.dataPoints) {
      this.stats.inputPoints += 1;

      if (metric.instrumentType === 'GAUGE') {
        this.stats.gaugePassThrough += 1;
        outputPoints.push({ ...clonePoint(point), restart: false });
        continue;
      }

      const key = seriesKey(metric.name, point.attributes);
      const output = this.processPoint(metric.name, key, point);
      if (output) {
        this.stats.outputPoints += 1;
        outputPoints.push(output);
      }
    }

    return {
      name: metric.name,
      instrumentType: metric.instrumentType,
      temporality: metric.instrumentType === 'GAUGE' ? metric.temporality : 'CUMULATIVE',
      dataPoints: outputPoints,
    };
  }

  private processPoint(
    metricName: string,
    key: string,
    point: DataPoint,
  ): OutputDataPoint | undefined {
    const cached = this.cache.get(key) as SeriesState | undefined;

    if (cached && point.timestamp <= cached.lastTimestamp) {
      this.stats.droppedOutOfOrder += 1;
      this.onDrop?.({
        metricName,
        timestamp: point.timestamp,
        lastTimestamp: cached.lastTimestamp,
        reason: 'out_of_order',
      });
      return undefined;
    }

    const reentry = cached ? false : this.consumeReentryMarker(key);

    if (!cached) {
      return this.handleFirstPoint(key, point, reentry);
    }

    return isHistogram(point)
      ? this.handleHistogramPoint(key, asHistogramPoint(point), cached)
      : this.handleNumberPoint(key, asNumberPoint(point), cached);
  }

  private handleFirstPoint(
    key: string,
    point: DataPoint,
    reentry: boolean,
  ): OutputDataPoint {
    const reason: ResetReason = reentry ? 'eviction_reentry' : 'first_point';
    this.recordReset(reason);
    this.stats.firstPointBaselines += 1;

    const startTime = this.windowStart(point);

    if (isHistogram(point)) {
      const histogram = asHistogramPoint(point);
      this.storeState(
        key,
        {
          kind: 'histogram',
          bounds: [...histogram.explicitBounds],
          bucketCounts: [...histogram.bucketCounts],
          count: histogram.count,
          sum: histogram.sum,
          min: histogram.min,
          max: histogram.max,
          startTime,
          lastTimestamp: histogram.timestamp,
        },
      );
      return { ...clonePoint(histogram), startTime, restart: true, resetReason: reason };
    }

    const numberPoint = asNumberPoint(point);
    this.storeState(key, {
      kind: 'number',
      cumulative: numberPoint.value,
      startTime,
      lastTimestamp: numberPoint.timestamp,
    });
    return { ...clonePoint(numberPoint), startTime, restart: true, resetReason: reason };
  }

  private handleNumberPoint(
    key: string,
    point: ReturnType<typeof asNumberPoint>,
    state: SeriesState,
  ): OutputDataPoint {
    const numberState = state.kind === 'number' ? state : undefined;
    if (!numberState) {
      throw new Error(`instrument type changed for series ${key} (number point after histogram)`);
    }

    numberState.cumulative += point.value;
    numberState.lastTimestamp = point.timestamp;
    this.storeState(key, numberState);

    return {
      ...clonePoint(point),
      value: numberState.cumulative,
      startTime: numberState.startTime,
      restart: false,
    };
  }

  private handleHistogramPoint(
    key: string,
    point: HistogramDataPoint,
    state: SeriesState,
  ): OutputDataPoint {
    const histogramState = state.kind === 'histogram' ? state : undefined;
    if (!histogramState) {
      throw new Error(`instrument type changed for series ${key} (histogram point after number)`);
    }

    if (!boundsEqual(histogramState.bounds, point.explicitBounds)) {
      const startTime = this.windowStart(point);
      this.recordReset('histogram_bounds_change');
      this.stats.histogramBoundsResets += 1;
      this.stats.restartEmissions += 1;

      this.storeState(
        key,
        {
          kind: 'histogram',
          bounds: [...point.explicitBounds],
          bucketCounts: [...point.bucketCounts],
          count: point.count,
          sum: point.sum,
          min: point.min,
          max: point.max,
          startTime,
          lastTimestamp: point.timestamp,
        },
      );
      return { ...clonePoint(point), startTime, restart: true, resetReason: 'histogram_bounds_change' };
    }

    histogramState.bucketCounts = point.bucketCounts.map(
      (count, index) => count + (histogramState.bucketCounts[index] ?? 0),
    );
    histogramState.count += point.count;
    histogramState.sum =
      histogramState.sum !== undefined && point.sum !== undefined
        ? histogramState.sum + point.sum
        : point.sum ?? histogramState.sum;
    histogramState.min =
      histogramState.min === undefined
        ? point.min
        : point.min === undefined
          ? histogramState.min
          : Math.min(histogramState.min, point.min);
    histogramState.max =
      histogramState.max === undefined
        ? point.max
        : point.max === undefined
          ? histogramState.max
          : Math.max(histogramState.max, point.max);
    histogramState.lastTimestamp = point.timestamp;
    this.storeState(key, histogramState);

    return {
      ...clonePoint(point),
      bucketCounts: [...histogramState.bucketCounts],
      count: histogramState.count,
      sum: histogramState.sum,
      min: histogramState.min,
      max: histogramState.max,
      startTime: histogramState.startTime,
      restart: false,
    };
  }

  private windowStart(point: DataPoint): bigint {
    return point.startTime !== undefined && point.startTime <= point.timestamp
      ? point.startTime
      : point.timestamp;
  }

  private storeState(key: string, state: SeriesState): void {
    const eviction = this.cache.set(key, state);
    this.registerEviction(eviction, this.markerCapacity());
  }
}
