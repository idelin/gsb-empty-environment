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
  lastValue: number;
  lastStartTime?: bigint;
  lastTimestamp: bigint;
}

interface HistogramSeriesState {
  kind: 'histogram';
  lastBounds: number[];
  lastBucketCounts: number[];
  lastCount: number;
  lastSum?: number;
  lastMin?: number;
  lastMax?: number;
  lastStartTime?: bigint;
  lastTimestamp: bigint;
}

type SeriesState = NumberSeriesState | HistogramSeriesState;

/**
 * Stateful Cumulative -> Delta conversion.
 *
 * Monotonic instruments (COUNTER, HISTOGRAM) are protected from negative
 * spikes: a startTime change or any monotonic value/bucket decrease is treated
 * as an emitter restart, never as a negative delta. Non-monotonic
 * UP_DOWN_COUNTER points are simple differences. Late/out-of-order points are
 * rejected without touching the baseline.
 */
export class CumulativeToDeltaProcessor extends BaseProcessor {
  constructor(options: ProcessorOptions = {}) {
    super(options);
  }

  process(metric: Metric): OutputMetric {
    validateMetric(metric, 'CUMULATIVE');

    const outputPoints: OutputDataPoint[] = [];
    for (const point of metric.dataPoints) {
      this.stats.inputPoints += 1;

      if (metric.instrumentType === 'GAUGE') {
        this.stats.gaugePassThrough += 1;
        outputPoints.push({ ...clonePoint(point), restart: false });
        continue;
      }

      const key = seriesKey(metric.name, point.attributes);
      const output = this.processPoint(metric.name, key, point, metric.instrumentType);
      if (output) {
        this.stats.outputPoints += 1;
        outputPoints.push(output);
      }
    }

    return {
      name: metric.name,
      instrumentType: metric.instrumentType,
      temporality: metric.instrumentType === 'GAUGE' ? metric.temporality : 'DELTA',
      dataPoints: outputPoints,
    };
  }

  private processPoint(
    metricName: string,
    key: string,
    point: DataPoint,
    instrumentType: Metric['instrumentType'],
  ): OutputDataPoint | undefined {
    if (isHistogram(point) && instrumentType !== 'HISTOGRAM') {
      throw new Error(`histogram point received for non-histogram metric ${metricName}`);
    }
    if (!isHistogram(point) && instrumentType === 'HISTOGRAM') {
      throw new Error(`number point received for histogram metric ${metricName}`);
    }

    const cached = this.cache.get(key) as SeriesState | undefined;

    if (cached && point.timestamp <= cached.lastTimestamp) {
      this.rejectLatePoint(metricName, point, cached.lastTimestamp);
      return undefined;
    }

    const reentry = cached ? false : this.consumeReentryMarker(key);

    if (!cached) {
      return this.handleFirstPoint(key, point, reentry);
    }

    return isHistogram(point)
      ? this.handleHistogramPoint(key, asHistogramPoint(point), cached, instrumentType)
      : this.handleNumberPoint(key, asNumberPoint(point), cached, instrumentType);
  }

  private handleFirstPoint(
    key: string,
    point: DataPoint,
    reentry: boolean,
  ): OutputDataPoint | undefined {
    const reason: ResetReason = reentry ? 'eviction_reentry' : 'first_point';
    this.recordReset(reason);
    this.stats.firstPointBaselines += 1;

    const emitInitial =
      this.initialPointAsDelta &&
      point.startTime !== undefined &&
      point.startTime < point.timestamp;

    if (isHistogram(point)) {
      const histogram = asHistogramPoint(point);
      this.storeState(key, this.histogramState(histogram));
      if (!emitInitial) {
        return undefined;
      }
      return { ...clonePoint(histogram), restart: true, resetReason: reason };
    }

    const numberPoint = asNumberPoint(point);
    this.storeState(key, this.numberState(numberPoint));
    if (!emitInitial) {
      return undefined;
    }
    return { ...clonePoint(numberPoint), restart: true, resetReason: reason };
  }

  private handleNumberPoint(
    key: string,
    point: ReturnType<typeof asNumberPoint>,
    state: SeriesState,
    instrumentType: Metric['instrumentType'],
  ): OutputDataPoint {
    const numberState = state.kind === 'number' ? state : undefined;
    if (!numberState) {
      throw new Error(`instrument type changed for series ${key} (number point after histogram)`);
    }

    const monotonic = instrumentType === 'COUNTER';
    const startTimeChanged =
      point.startTime !== undefined &&
      numberState.lastStartTime !== undefined &&
      point.startTime !== numberState.lastStartTime;

    const valueDecreased = point.value < numberState.lastValue;
    const restart = monotonic && (startTimeChanged || valueDecreased);

    if (restart) {
      const reason: ResetReason = startTimeChanged
        ? 'restart_start_time'
        : 'restart_value_decrease';
      this.recordReset(reason);
      this.stats.restartEmissions += 1;

      const effectiveStartTime = startTimeChanged
        ? point.startTime
        : numberState.lastTimestamp;

      this.storeState(key, this.numberState(point));
      return {
        ...clonePoint(point),
        startTime: effectiveStartTime,
        restart: true,
        resetReason: reason,
      };
    }

    const delta = point.value - numberState.lastValue;
    this.storeState(key, this.numberState(point));
    return {
      ...clonePoint(point),
      value: delta,
      startTime: numberState.lastTimestamp,
      restart: false,
    };
  }

  private handleHistogramPoint(
    key: string,
    point: HistogramDataPoint,
    state: SeriesState,
    instrumentType: Metric['instrumentType'],
  ): OutputDataPoint {
    const histogramState = state.kind === 'histogram' ? state : undefined;
    if (!histogramState) {
      throw new Error(`instrument type changed for series ${key} (histogram point after number)`);
    }
    if (instrumentType !== 'HISTOGRAM') {
      throw new Error(`histogram point received for non-histogram instrument ${key}`);
    }

    const startTimeChanged =
      point.startTime !== undefined &&
      histogramState.lastStartTime !== undefined &&
      point.startTime !== histogramState.lastStartTime;
    const boundsChanged = !boundsEqual(histogramState.lastBounds, point.explicitBounds);
    const bucketDecreased = point.bucketCounts.some(
      (count, index) => count < (histogramState.lastBucketCounts[index] ?? 0),
    );
    const countDecreased = point.count < histogramState.lastCount;
    const sumDecreased =
      point.sum !== undefined &&
      histogramState.lastSum !== undefined &&
      point.sum < histogramState.lastSum;

    const restart =
      startTimeChanged || boundsChanged || bucketDecreased || countDecreased || sumDecreased;

    if (restart) {
      let reason: ResetReason;
      if (boundsChanged) {
        reason = 'histogram_bounds_change';
        this.stats.histogramBoundsResets += 1;
      } else if (startTimeChanged) {
        reason = 'restart_start_time';
      } else {
        reason = 'restart_value_decrease';
      }
      this.recordReset(reason);
      this.stats.restartEmissions += 1;

      const effectiveStartTime =
        startTimeChanged || boundsChanged ? point.startTime : histogramState.lastTimestamp;

      this.storeState(key, this.histogramState(point));
      return {
        ...clonePoint(point),
        startTime: effectiveStartTime,
        restart: true,
        resetReason: reason,
      };
    }

    const bucketCounts = point.bucketCounts.map(
      (count, index) => count - (histogramState.lastBucketCounts[index] ?? 0),
    );
    const count = point.count - histogramState.lastCount;
    const sum =
      point.sum !== undefined && histogramState.lastSum !== undefined
        ? point.sum - histogramState.lastSum
        : point.sum;

    this.storeState(key, this.histogramState(point));
    return {
      ...clonePoint(point),
      bucketCounts,
      count,
      sum,
      startTime: histogramState.lastTimestamp,
      restart: false,
    };
  }

  private numberState(point: ReturnType<typeof asNumberPoint>): NumberSeriesState {
    return {
      kind: 'number',
      lastValue: point.value,
      lastStartTime: point.startTime,
      lastTimestamp: point.timestamp,
    };
  }

  private histogramState(point: HistogramDataPoint): HistogramSeriesState {
    return {
      kind: 'histogram',
      lastBounds: [...point.explicitBounds],
      lastBucketCounts: [...point.bucketCounts],
      lastCount: point.count,
      lastSum: point.sum,
      lastMin: point.min,
      lastMax: point.max,
      lastStartTime: point.startTime,
      lastTimestamp: point.timestamp,
    };
  }

  private storeState(key: string, state: SeriesState): void {
    const eviction = this.cache.set(key, state);
    this.registerEviction(eviction, this.markerCapacity());
  }

  private rejectLatePoint(
    metricName: string,
    point: DataPoint,
    lastTimestamp: bigint,
  ): void {
    this.stats.droppedOutOfOrder += 1;
    this.onDrop?.({
      metricName,
      timestamp: point.timestamp,
      lastTimestamp,
      reason: 'out_of_order',
    });
  }
}
