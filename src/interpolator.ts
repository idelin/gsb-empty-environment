/**
 * Multi-speaker gain interpolator.
 *
 * Control points carry integer weights (0..maxWeight) per speaker. Gains are
 * linearly interpolated between control points in fixed point, then
 * normalized to unit total energy (constant-power normalization). A control
 * point whose weights are all zero is treated as silence: the output frame
 * is exactly zero and normalization is skipped.
 *
 * Every output frame is a pure function of (sampleIndex, control points,
 * config), so processing in blocks of any size yields bit-identical results
 * to processing the whole range at once.
 */
import { isqrt, pow2, roundDiv } from "./fixedPoint";

export interface InterpolatorConfig {
  /** Number of loudspeakers (length of every weight vector). */
  numSpeakers: number;
  /** Maximum integer weight; weights must satisfy 0 <= w <= maxWeight. */
  maxWeight: number;
  /** Fractional bits of the internal interpolated-gain format. */
  fracBits: number;
  /** Fractional bits of the normalized output format. */
  outFracBits: number;
  /** Guard bits used inside the reciprocal-square-root computation. */
  guardBits: number;
}

/** One output frame: Q(outFracBits) fixed-point gain per speaker. */
export type GainFrame = number[];

interface Segment {
  startTime: number;
  startWeights: readonly number[];
  endTime: number; // Infinity when holding the last control point
  endWeights: readonly number[] | null;
}

export class GainInterpolator {
  private readonly config: InterpolatorConfig;
  private readonly points = new Map<number, number[]>();
  private sortedTimes: number[] = [];
  private cursor = 0;
  private streamSegment = 0;

  constructor(config: InterpolatorConfig) {
    validateConfig(config);
    this.config = { ...config };
  }

  get numSpeakers(): number {
    return this.config.numSpeakers;
  }

  /**
   * Adds a control point. If a control point already exists at `time`, it is
   * replaced (same-time override: the last one wins).
   */
  addControlPoint(time: number, weights: readonly number[]): void {
    if (!Number.isInteger(time) || time < 0) {
      throw new Error("control point time must be a non-negative integer");
    }
    if (time < this.cursor) {
      throw new Error(
        `control point at t=${time} is before the streaming cursor (t=${this.cursor})`
      );
    }
    if (weights.length !== this.config.numSpeakers) {
      throw new Error(
        `expected ${this.config.numSpeakers} weights, got ${weights.length}`
      );
    }
    const stored = weights.map((w) => {
      if (!Number.isInteger(w) || w < 0 || w > this.config.maxWeight) {
        throw new Error(
          `weight ${w} is not an integer in [0, ${this.config.maxWeight}]`
        );
      }
      return w;
    });
    if (!this.points.has(time)) {
      this.sortedTimes.push(time);
      this.sortedTimes.sort((a, b) => a - b);
    }
    this.points.set(time, stored);
  }

  clearControlPoints(): void {
    this.points.clear();
    this.sortedTimes = [];
    this.streamSegment = 0;
  }

  /** Rewinds the streaming cursor so processBlock starts over. */
  reset(): void {
    this.cursor = 0;
    this.streamSegment = 0;
  }

  /** Current streaming position (index of the next sample processBlock emits). */
  get position(): number {
    return this.cursor;
  }

  /** One-shot processing of [startSample, startSample + count). Stateless. */
  process(startSample: number, count: number): GainFrame[] {
    if (!Number.isInteger(startSample) || startSample < 0 || count < 0) {
      throw new Error("invalid range");
    }
    const frames: GainFrame[] = new Array(count);
    for (let i = 0; i < count; i++) {
      frames[i] = this.frameAt(startSample + i);
    }
    return frames;
  }

  /**
   * Streaming processing: emits `count` frames continuing from the internal
   * cursor. Bit-identical to process() over the same sample range.
   */
  processBlock(count: number): GainFrame[] {
    if (count < 0) {
      throw new Error("invalid block size");
    }
    const frames: GainFrame[] = new Array(count);
    for (let i = 0; i < count; i++) {
      const t = this.cursor;
      this.streamSegment = this.advanceSegment(this.streamSegment, t);
      frames[i] = this.frameInSegment(t, this.segmentAt(this.streamSegment));
      this.cursor++;
    }
    return frames;
  }

  private advanceSegment(index: number, t: number): number {
    const times = this.sortedTimes;
    let i = Math.min(index, Math.max(times.length - 1, 0));
    while (i + 1 < times.length && times[i + 1] <= t) {
      i++;
    }
    return i;
  }

  private segmentIndexFor(t: number): number {
    const times = this.sortedTimes;
    // Binary search: last control point time <= t.
    let lo = 0;
    let hi = times.length; // invariant: times[lo-1] <= t < times[hi]
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (times[mid] <= t) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    return lo - 1; // -1 when t precedes the first control point
  }

  private segmentAt(index: number): Segment | null {
    if (index < 0 || this.sortedTimes.length === 0) {
      return null;
    }
    const startTime = this.sortedTimes[index];
    const startWeights = this.points.get(startTime)!;
    if (index + 1 < this.sortedTimes.length) {
      const endTime = this.sortedTimes[index + 1];
      return {
        startTime,
        startWeights,
        endTime,
        endWeights: this.points.get(endTime)!,
      };
    }
    return { startTime, startWeights, endTime: Infinity, endWeights: null };
  }

  private frameAt(t: number): GainFrame {
    return this.frameInSegment(t, this.segmentAt(this.segmentIndexFor(t)));
  }

  private frameInSegment(t: number, seg: Segment | null): GainFrame {
    const { numSpeakers, maxWeight, fracBits } = this.config;
    if (seg === null) {
      return new Array<number>(numSpeakers).fill(0); // before first point: silence
    }
    const scale = pow2(fracBits);
    const gains = new Array<bigint>(numSpeakers);
    if (seg.endWeights === null || seg.endTime === seg.startTime) {
      // Holding the final control point.
      for (let s = 0; s < numSpeakers; s++) {
        gains[s] = roundDiv(BigInt(seg.startWeights[s]) * scale, BigInt(maxWeight));
      }
    } else {
      const span = BigInt(seg.endTime - seg.startTime);
      const dt = BigInt(t - seg.startTime);
      const denom = span * BigInt(maxWeight);
      for (let s = 0; s < numSpeakers; s++) {
        const w0 = BigInt(seg.startWeights[s]);
        const w1 = BigInt(seg.endWeights[s]);
        const blended = w0 * (span - dt) + w1 * dt;
        gains[s] = roundDiv(blended * scale, denom);
      }
    }
    return this.normalize(gains);
  }

  /**
   * Constant-power normalization: g_i / sqrt(sum g_j^2), computed in fixed
   * point with `guardBits` of extra precision inside the reciprocal square
   * root. An all-zero (silent) vector maps to an all-zero frame.
   */
  private normalize(gains: bigint[]): GainFrame {
    const { fracBits, outFracBits, guardBits } = this.config;
    let energy = 0n;
    for (const g of gains) {
      energy += g * g;
    }
    if (energy === 0n) {
      return gains.map(() => 0); // silence: skip normalization
    }
    // S ~= 2^(fracBits + outFracBits) / sqrt(energy), with guardBits of
    // extra precision inside the integer square root.
    const root = isqrt(energy << BigInt(2 * guardBits));
    const scaleFactor = roundDiv(
      pow2(fracBits + outFracBits + guardBits),
      root
    );
    const unit = pow2(fracBits);
    return gains.map((g) => Number(roundDiv(g * scaleFactor, unit)));
  }
}

function validateConfig(config: InterpolatorConfig): void {
  const { numSpeakers, maxWeight, fracBits, outFracBits, guardBits } = config;
  if (!Number.isInteger(numSpeakers) || numSpeakers < 1) {
    throw new Error("numSpeakers must be a positive integer");
  }
  if (!Number.isInteger(maxWeight) || maxWeight < 1) {
    throw new Error("maxWeight must be a positive integer");
  }
  for (const [name, value] of [
    ["fracBits", fracBits],
    ["outFracBits", outFracBits],
    ["guardBits", guardBits],
  ] as const) {
    if (!Number.isInteger(value) || value < 1 || value > 52) {
      throw new Error(`${name} must be an integer in [1, 52]`);
    }
  }
}
