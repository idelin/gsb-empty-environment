/**
 * PromQL range-vector counter evaluation, faithful to the Prometheus
 * `extrapolatedRate` / `instantValue` implementations (the 1.1*average-scrape
 * interval generation).
 *
 * Inputs are ascending samples with millisecond timestamps (`t`) and IEEE-754
 * float64 values. Prometheus "stale" markers are a specific NaN bit pattern
 * (`0x7ff0000000000002`) and must be distinguished from ordinary mathematical
 * NaNs; because V8 canonicalises NaN payloads when values are stored through
 * `Float64Array`, bit-exact detection is performed on the raw integer
 * representation using `DataView` / `BigInt64Array`.
 */

/** Raw little-endian IEEE-754 bit pattern of the Prometheus stale marker. */
export const STALE_NAN_BITS = 0x7ff0000000000002n;

/** A single timestamped sample. Timestamps are milliseconds since the epoch. */
export interface Sample {
  /** Sample timestamp in milliseconds. */
  t: number;
  /** IEEE-754 float64 sample value. May be a (stale or ordinary) NaN. */
  v: number;
  /**
   * Optional explicit stale marker. Set this when the value was decoded from a
   * binary buffer whose exact NaN bit pattern may have been canonicalised while
   * being stored in a `Float64Array`. When omitted, stale-ness is detected from
   * the raw bits of `v` via `isStaleNaN(v)`.
   */
  stale?: boolean;
}

const MS_PER_SECOND = 1000;

/**
 * Returns true when `value` is the Prometheus stale marker NaN
 * (`0x7ff0000000000002`). An ordinary NaN (any other bit pattern) returns
 * false. The check reads the raw IEEE-754 bits through a `DataView`; note that
 * a round-trip through `Float64Array` would canonicalise the payload.
 */
export function isStaleNaN(value: number): boolean {
  const buffer = new ArrayBuffer(8);
  new DataView(buffer).setFloat64(0, value, true);
  return new DataView(buffer).getBigUint64(0, true) === STALE_NAN_BITS;
}

/**
 * Decodes an interleaved binary sample buffer: each sample occupies
 * `strideBytes` bytes, an int64 little-endian millisecond timestamp at
 * `offsetT`, followed by a float64 value at `offsetV`. Stale markers are
 * detected from the raw bits (via `DataView`) before any NaN canonicalisation
 * can occur, exactly as Prometheus reads them from the wire.
 */
export function decodeSamples(
  buffer: ArrayBufferLike,
  options: {
    count: number;
    strideBytes?: number;
    offsetT?: number;
    offsetV?: number;
    littleEndian?: boolean;
  },
): Sample[] {
  const {
    count,
    strideBytes = 16,
    offsetT = 0,
    offsetV = 8,
    littleEndian = true,
  } = options;
  const dataView = new DataView(buffer);
  const samples: Sample[] = new Array(count);
  for (let index = 0; index < count; index += 1) {
    const base = index * strideBytes;
    const t = Number(dataView.getBigInt64(base + offsetT, littleEndian));
    const rawBits = dataView.getBigUint64(base + offsetV, littleEndian);
    const stale = rawBits === STALE_NAN_BITS;
    const v = dataView.getFloat64(base + offsetV, littleEndian);
    samples[index] = { t, v, stale };
  }
  return samples;
}

/**
 * Decodes a columnar binary layout: timestamps and values live in two separate
 * 8-byte-wide views. The raw integer view preserves the stale-marker bit
 * pattern (a float view would canonicalise it).
 */
export function decodeColumnarSamples(
  timestamps: BigInt64Array,
  values: ArrayBufferLike,
  littleEndian = true,
): Sample[] {
  const count = Math.min(timestamps.length, values.byteLength / 8);
  const valueView = new DataView(values);
  const samples: Sample[] = new Array(count);
  for (let index = 0; index < count; index += 1) {
    const t = Number(timestamps[index]);
    const rawBits = valueView.getBigUint64(index * 8, littleEndian);
    const stale = rawBits === STALE_NAN_BITS;
    const v = valueView.getFloat64(index * 8, littleEndian);
    samples[index] = { t, v, stale };
  }
  return samples;
}

function isSampleStale(sample: Sample): boolean {
  return sample.stale === true || isStaleNaN(sample.v);
}

/**
 * Selects samples inside the inclusive `[evalTs - rangeMs, evalTs]` window and
 * removes every stale-marker sample. Stale markers only split adjacency; they
 * are never counted as counter resets.
 */
function filterWindow(samples: readonly Sample[], evalTs: number, rangeMs: number): Sample[] {
  const windowStart = evalTs - rangeMs;
  const result: Sample[] = [];
  for (const sample of samples) {
    if (sample.t < windowStart || sample.t > evalTs) {
      continue;
    }
    if (isSampleStale(sample)) {
      continue;
    }
    result.push(sample);
  }
  return result;
}

/**
 * Sum of adjacent sample deltas following the counter-reset rule: when
 * `v[i] < v[i-1]` the counter was reset, so the absolute new value `v[i]` is
 * accumulated instead of the (negative) difference. Gauges (`isCounter`
 * false) always take the plain difference, including negative deltas.
 */
function sumDeltas(values: readonly number[], isCounter: boolean): number {
  let result = 0;
  for (let index = 1; index < values.length; index += 1) {
    const previous = values[index - 1];
    const current = values[index];
    if (isCounter && current < previous) {
      result += current;
    } else {
      result += current - previous;
    }
  }
  return result;
}

/**
 * Shared implementation of `rate`/`increase`, mirroring the Prometheus
 * `extrapolatedRate` function.
 *
 * Returns `undefined` when fewer than two non-stale samples fall in the window
 * (the series must not be emitted). Returns `NaN` when any surviving sample is
 * an ordinary mathematical NaN (IEEE propagation).
 */
function extrapolatedRate(
  samples: readonly Sample[],
  evalTs: number,
  rangeMs: number,
  isCounter: boolean,
  factor: (rangeSeconds: number) => number,
): number | undefined {
  if (!(rangeMs > 0)) {
    return undefined;
  }
  const windowStart = evalTs - rangeMs;
  const points = filterWindow(samples, evalTs, rangeMs);
  if (points.length < 2) {
    return undefined;
  }

  const timestamps = points.map((sample) => sample.t);
  const values = points.map((sample) => sample.v);

  // Ordinary (non-stale) NaNs propagate per IEEE-754 semantics.
  if (values.some((value) => Number.isNaN(value))) {
    return Number.NaN;
  }

  const sampledInterval = timestamps[timestamps.length - 1] - timestamps[0];
  const averageInterval = sampledInterval / (values.length - 1);
  let resultValue = sumDeltas(values, isCounter);

  // Each boundary is extrapolated across the full gap to the window edge only
  // when that gap is within 1.1 average scrape intervals; otherwise (sparse
  // data) the extrapolation on that side is capped at half an average interval
  // to avoid amplifying thin data.
  let durationToStart = timestamps[0] - windowStart;
  let durationToEnd = evalTs - timestamps[timestamps.length - 1];
  const extrapolationThreshold = 1.1 * averageInterval;
  if (durationToStart >= extrapolationThreshold) {
    durationToStart = averageInterval / 2;
  }
  if (durationToEnd >= extrapolationThreshold) {
    durationToEnd = averageInterval / 2;
  }

  // Counter-specific guard: never back-extrapolate across zero. If the average
  // slope would reach zero before the window's left edge, clamp the left
  // extrapolation duration so the projected value at the edge stays non-negative.
  if (
    isCounter &&
    values[0] >= 0 &&
    resultValue > 0 &&
    durationToStart > (values[0] / resultValue) * sampledInterval
  ) {
    durationToStart = (values[0] / resultValue) * sampledInterval;
  }

  const sampledIntervalSeconds = sampledInterval / MS_PER_SECOND;
  // Factor multiplies the per-second average rate over the sampled interval.
  const extrapolationFactor =
    sampledInterval === 0 || resultValue === 0
      ? 1
      : (sampledInterval + durationToStart + durationToEnd) / sampledInterval;
  resultValue =
    (resultValue / sampledIntervalSeconds) *
    extrapolationFactor *
    factor(
      rangeMs / MS_PER_SECOND +
        (durationToStart + durationToEnd) / MS_PER_SECOND -
        averageInterval / MS_PER_SECOND,
    );

  // Counters cannot go negative: guard against residual floating-point drift.
  if (isCounter && resultValue < 0) {
    return 0;
  }
  return resultValue;
}

/**
 * PromQL `rate()`: per-second average counter increment over the range window,
 * with boundary extrapolation. Pass `false` for `isCounter` to evaluate gauges.
 */
export function rate(
  samples: readonly Sample[],
  evalTs: number,
  rangeMs: number,
  isCounter = true,
): number | undefined {
  return extrapolatedRate(samples, evalTs, rangeMs, isCounter, () => 1);
}

/**
 * PromQL `increase()`: total counter increment over the range window, with
 * boundary extrapolation. Pass `false` for `isCounter` to evaluate gauges.
 */
export function increase(
  samples: readonly Sample[],
  evalTs: number,
  rangeMs: number,
  isCounter = true,
): number | undefined {
  return extrapolatedRate(samples, evalTs, rangeMs, isCounter, (durationSeconds) => durationSeconds);
}

/**
 * PromQL `irate()`: instantaneous rate from the last two non-stale samples in
 * the window, applying the counter-reset rule. No extrapolation is performed.
 *
 * Returns `undefined` when fewer than two non-stale samples fall in the window
 * and `NaN` when either of the final two values is an ordinary NaN.
 */
export function irate(
  samples: readonly Sample[],
  evalTs: number,
  rangeMs: number,
  isCounter = true,
): number | undefined {
  if (!(rangeMs > 0)) {
    return undefined;
  }
  const points = filterWindow(samples, evalTs, rangeMs);
  if (points.length < 2) {
    return undefined;
  }
  const last = points[points.length - 1];
  const previous = points[points.length - 2];
  if (Number.isNaN(last.v) || Number.isNaN(previous.v)) {
    return Number.NaN;
  }
  let delta: number;
  if (isCounter && last.v < previous.v) {
    delta = last.v;
  } else {
    delta = last.v - previous.v;
  }
  const intervalSeconds = (last.t - previous.t) / MS_PER_SECOND;
  return delta / intervalSeconds;
}
