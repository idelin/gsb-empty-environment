/**
 * OTel-style metric data model used by the temporality processors.
 *
 * The model is intentionally dependency-free: it mirrors the structure of
 * OpenTelemetry metric data points (number points and explicit-bucket
 * histogram points) without pulling in the SDK. Timestamps are unsigned
 * nanosecond Unix epoch values carried as `bigint` so that high-resolution
 * monotonic clocks never lose precision.
 */

export type Temporality = 'CUMULATIVE' | 'DELTA';

export type InstrumentType =
  | 'COUNTER'
  | 'UP_DOWN_COUNTER'
  | 'HISTOGRAM'
  | 'GAUGE';

/** Attribute values supported by the OpenTelemetry data model. */
export type AttributeValue =
  | string
  | number
  | boolean
  | ReadonlyArray<string | number | boolean>;

export type Attributes = Readonly<Record<string, AttributeValue>>;

/** Nanosecond Unix epoch timestamps. Accept `number` on input, use bigint internally. */
export type TimeUnixNano = bigint;

interface BasePoint {
  attributes?: Attributes;
  /** Observation timestamp, nanoseconds since the Unix epoch. */
  timestamp: TimeUnixNano;
  /** Instrument start timestamp carried by the point, if known. */
  startTime?: TimeUnixNano;
}

export interface NumberDataPoint extends BasePoint {
  value: number;
}

export interface HistogramDataPoint extends BasePoint {
  /**
   * Monotonic cumulative/delta counts per explicit bucket.
   * Length is always `explicitBounds.length + 1` (the overflow bucket last).
   */
  bucketCounts: number[];
  /** Strictly increasing upper-inclusive bucket boundaries. */
  explicitBounds: number[];
  /** Total number of recorded values (sum of bucket counts). */
  count: number;
  /** Sum of recorded values; may be omitted when the instrument cannot provide it. */
  sum?: number;
  min?: number;
  max?: number;
}

export type DataPoint = NumberDataPoint | HistogramDataPoint;

export interface Metric {
  name: string;
  instrumentType: InstrumentType;
  temporality: Temporality;
  dataPoints: DataPoint[];
}

/** Why a series state was (re)initialized when emitting a point. */
export type ResetReason =
  | 'first_point'
  | 'restart_start_time'
  | 'restart_value_decrease'
  | 'histogram_bounds_change'
  | 'eviction_reentry';

/** Bookkeeping attached to every emitted point for downstream observability. */
export interface ConversionMetadata {
  /** True when the point starts a fresh lifecycle (first point, restart or eviction). */
  restart: boolean;
  /** Set whenever `restart` is true. */
  resetReason?: ResetReason;
}

export interface OutputNumberDataPoint extends NumberDataPoint, ConversionMetadata {}

export interface OutputHistogramDataPoint extends HistogramDataPoint, ConversionMetadata {}

export type OutputDataPoint = OutputNumberDataPoint | OutputHistogramDataPoint;

export interface OutputMetric {
  name: string;
  instrumentType: InstrumentType;
  temporality: Temporality;
  dataPoints: OutputDataPoint[];
}

/**
 * Lifecycle counters of a processor. All counters are monotonic over the
 * lifetime of the processor unless `resetStats()` is called.
 */
export interface ProcessorStats {
  /** Total input points offered to the processor. */
  inputPoints: number;
  /** Points successfully converted and emitted. */
  outputPoints: number;
  /** Out-of-order / late points rejected (timestamp <= last timestamp). */
  droppedOutOfOrder: number;
  /** Gauge points forwarded without stateful conversion. */
  gaugePassThrough: number;
  /** Points that began a brand-new series baseline. */
  firstPointBaselines: number;
  /** Points emitted as a full value because of an emitter restart. */
  restartEmissions: number;
  /** Histogram points whose explicit bounds changed, forcing a reset. */
  histogramBoundsResets: number;
  /** LRU series evictions performed to respect `maxSeries`. */
  seriesEvicted: number;
  /** First points observed for a series that had previously been evicted. */
  evictionReentries: number;
  /** Breakdown of every lifecycle reset by reason. */
  resetReasons: Record<ResetReason, number>;
}

export interface ProcessorOptions {
  /**
   * Hard cap on concurrently tracked series. When the cap is reached the
   * least-recently-updated series is evicted. Defaults to unbounded.
   */
  maxSeries?: number;
  /**
   * Cumulative -> Delta only: when true, the first point of a series is
   * emitted as a delta spanning [startTime, timestamp] instead of being
   * swallowed as a baseline. Only honoured when `startTime < timestamp`.
   */
  initialPointAsDelta?: boolean;
  /** Optional callback invoked for every rejected late/out-of-order point. */
  onDrop?: (info: DropInfo) => void;
}

export interface DropInfo {
  metricName: string;
  timestamp: TimeUnixNano;
  lastTimestamp: TimeUnixNano;
  reason: 'out_of_order';
}

export function createEmptyStats(): ProcessorStats {
  return {
    inputPoints: 0,
    outputPoints: 0,
    droppedOutOfOrder: 0,
    gaugePassThrough: 0,
    firstPointBaselines: 0,
    restartEmissions: 0,
    histogramBoundsResets: 0,
    seriesEvicted: 0,
    evictionReentries: 0,
    resetReasons: {
      first_point: 0,
      restart_start_time: 0,
      restart_value_decrease: 0,
      histogram_bounds_change: 0,
      eviction_reentry: 0,
    },
  };
}
