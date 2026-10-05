import { ceilDiv, divRound, isqrt } from "./fixedpoint";

/** A control point: integer per-speaker weights at an integer sample time. */
export interface ControlPoint {
  time: number;
  weights: number[];
}

/**
 * One processed sample.
 * `gains` are Q(fracBits) fixed-point values; all zero when `muted`.
 * `energyErrorBound` is a certified upper bound, in Q(2*fracBits) units, on
 * |sum(gains^2)/2^(2F) - targetEnergy|, where targetEnergy is 0 when muted
 * and 1 otherwise.
 */
export interface Frame {
  time: number;
  gains: bigint[];
  muted: boolean;
  energyErrorBound: bigint;
}

export interface Violation {
  time: number;
  /** Certified error bound at this sample, as a plain number. */
  errorBound: number;
}

export interface AnalysisReport {
  /** True when every sample's certified error bound stays within the limit. */
  feasible: boolean;
  /** Largest certified energy-error bound over the analysed range. */
  maxEnergyErrorBound: number;
  /** Sample index where the maximum occurs, or null for an empty range. */
  worstTime: number | null;
  /** Every sample whose certified bound exceeds the requested limit. */
  violations: Violation[];
  /** Human-readable explanations when `feasible` is false. */
  reasons: string[];
}

export interface ConfigCheck {
  feasible: boolean;
  requiredFracBits: number;
  reasons: string[];
}

export const DEFAULT_FRAC_BITS = 32;

/**
 * Multi-speaker gain interpolator with fixed-point arithmetic and certified
 * error enclosure.
 *
 * - Control points carry integer weights; a point added at an already-used
 *   time overwrites the previous one.
 * - Weights are linearly interpolated between control points in Q(F).
 * - Each sample is normalised to unit energy (sum of squared gains = 1);
 *   an all-zero weight vector is emitted as silence (all gains zero).
 * - Every frame carries a rigorously certified bound on the total energy
 *   error, derived by interval reasoning over the fixed-point roundings.
 * - Each sample is computed purely from its absolute time, so processing in
 *   blocks of any size yields bit-identical results to one-shot processing.
 */
export class GainInterpolator {
  readonly speakers: number;
  readonly fracBits: number;
  private readonly points = new Map<number, number[]>();
  private sortedTimes: number[] | null = null;

  constructor(options: { speakers: number; fracBits?: number }) {
    if (!Number.isInteger(options.speakers) || options.speakers < 1) {
      throw new RangeError("speakers must be a positive integer");
    }
    const fracBits = options.fracBits ?? DEFAULT_FRAC_BITS;
    if (!Number.isInteger(fracBits) || fracBits < 8 || fracBits > 53) {
      throw new RangeError("fracBits must be an integer in [8, 53]");
    }
    this.speakers = options.speakers;
    this.fracBits = fracBits;
  }

  /** Add (or overwrite, if `time` is already used) a control point. */
  addControlPoint(time: number, weights: readonly number[]): void {
    if (!Number.isInteger(time) || time < 0) {
      throw new RangeError("time must be a non-negative integer");
    }
    if (weights.length !== this.speakers) {
      throw new RangeError(
        `expected ${this.speakers} weights, got ${weights.length}`,
      );
    }
    for (const w of weights) {
      if (!Number.isInteger(w)) {
        throw new RangeError("weights must be integers");
      }
    }
    this.points.set(time, [...weights]);
    this.sortedTimes = null;
  }

  removeControlPoint(time: number): boolean {
    this.sortedTimes = null;
    return this.points.delete(time);
  }

  controlPoints(): ControlPoint[] {
    return this.times().map((t) => ({
      time: t,
      weights: [...this.points.get(t)!],
    }));
  }

  /** Compute a single frame at absolute sample time `time`. */
  frameAt(time: number): Frame {
    if (!Number.isInteger(time) || time < 0) {
      throw new RangeError("time must be a non-negative integer");
    }
    const w = this.weightVectorAt(time);
    const scale = 1n << BigInt(this.fracBits);
    const s = w.reduce((acc, x) => acc + x * x, 0n);
    if (s === 0n) {
      // Zero weight vector: silence. Target energy is 0 and we emit exactly 0.
      return {
        time,
        gains: w.map(() => 0n),
        muted: true,
        energyErrorBound: 0n,
      };
    }
    const r = isqrt(s); // floor(sqrt(s)), so 0 <= s - r^2 <= 2r
    const gains = w.map((x) => divRound(x * scale, r));

    // Certified error enclosure, all quantities in Q(F) units.
    // |g_k - w_k*2^F/sqrt(s)|
    //   <= 1/2                       (final division rounding; bounded by 1)
    //    + |w_k|*2^F*(s - r^2)/(2r^3)  (floor of the square root)
    const sqrtResid = s - r * r;
    const denom = 2n * r * r * r;
    let err = 0n;
    for (let k = 0; k < gains.length; k++) {
      const g = gains[k] < 0n ? -gains[k] : gains[k];
      const x = w[k] < 0n ? -w[k] : w[k];
      const deltaSqrt =
        sqrtResid === 0n ? 0n : ceilDiv(x * scale * sqrtResid, denom);
      const delta = 1n + deltaSqrt;
      // |g_k^2 - gtrue_k^2| <= delta * (2|g_k| + delta)
      err += delta * (2n * g + delta);
    }
    return { time, gains, muted: false, energyErrorBound: err };
  }

  /**
   * Process samples [start, start + length). Every frame depends only on its
   * absolute time, so any partitioning of a range into blocks produces
   * bit-identical output to processing the whole range at once.
   */
  processBlock(start: number, length: number): Frame[] {
    if (!Number.isInteger(start) || start < 0) {
      throw new RangeError("start must be a non-negative integer");
    }
    if (!Number.isInteger(length) || length < 0) {
      throw new RangeError("length must be a non-negative integer");
    }
    const frames: Frame[] = new Array(length);
    for (let i = 0; i < length; i++) frames[i] = this.frameAt(start + i);
    return frames;
  }

  /**
   * Scan [start, start + length) and check the certified energy-error bound
   * of every sample against `energyErrorBound` (a plain fraction, e.g. 1e-6).
   * Reports the worst sample and every violation so that configurations that
   * cannot reach the requested precision are identified explicitly.
   */
  analyze(start: number, length: number, energyErrorBound: number): AnalysisReport {
    if (!(energyErrorBound > 0)) {
      throw new RangeError("energyErrorBound must be positive");
    }
    const frames = this.processBlock(start, length);
    const limit = this.energyToBigInt(energyErrorBound);
    let maxErr = 0n;
    let worstTime: number | null = null;
    const violations: Violation[] = [];
    for (const frame of frames) {
      if (frame.energyErrorBound > maxErr) {
        maxErr = frame.energyErrorBound;
        worstTime = frame.time;
      }
      if (frame.energyErrorBound > limit) {
        violations.push({
          time: frame.time,
          errorBound: this.energyToNumber(frame.energyErrorBound),
        });
      }
    }
    const feasible = violations.length === 0;
    const reasons: string[] = [];
    if (!feasible) {
      reasons.push(
        `${violations.length} of ${frames.length} samples exceed the energy ` +
          `error bound ${energyErrorBound} (worst: ` +
          `${this.energyToNumber(maxErr)} at t=${worstTime}). ` +
          `Increase fracBits (currently ${this.fracBits}) or relax the bound. ` +
          `Note: normalisation is ill-conditioned for near-zero weight ` +
          `vectors (e.g. at the start of a fade from silence); no fracBits ` +
          `can fix samples whose interpolated weights round to tiny values.`,
      );
    }
    return {
      feasible,
      maxEnergyErrorBound: this.energyToNumber(maxErr),
      worstTime,
      violations,
      reasons,
    };
  }

  /** Convert a Q(2F) energy-error bound to a plain number. */
  energyToNumber(energyQ: bigint): number {
    return Number(energyQ) / 2 ** (2 * this.fracBits);
  }

  /** Convert a plain fraction to Q(2F), rounded up so comparisons stay safe. */
  energyToBigInt(energy: number): bigint {
    const scaled = energy * 2 ** (2 * this.fracBits);
    return BigInt(Math.ceil(scaled));
  }

  /**
   * Smallest F such that the steady-state energy-error bound
   * 2*sqrt(speakers)*2^-F stays within `energyErrorBound`.
   */
  static requiredFracBits(speakers: number, energyErrorBound: number): number {
    if (!Number.isInteger(speakers) || speakers < 1) {
      throw new RangeError("speakers must be a positive integer");
    }
    if (!(energyErrorBound > 0)) {
      throw new RangeError("energyErrorBound must be positive");
    }
    let f = 1;
    while (2 * Math.sqrt(speakers) / 2 ** f > energyErrorBound) f++;
    return f;
  }

  /**
   * Static feasibility check for a configuration, before any control points
   * are involved. Reports configurations that cannot reach the requested
   * precision in steady state; `analyze` additionally catches the
   * ill-conditioned near-zero samples of a concrete trajectory.
   */
  static checkConfig(options: {
    speakers: number;
    fracBits: number;
    energyErrorBound: number;
  }): ConfigCheck {
    const { speakers, fracBits, energyErrorBound } = options;
    const required = GainInterpolator.requiredFracBits(
      speakers,
      energyErrorBound,
    );
    const feasible = fracBits >= required;
    const reasons: string[] = [];
    if (!feasible) {
      reasons.push(
        `fracBits=${fracBits} is insufficient for ${speakers} speakers at ` +
          `energy error bound ${energyErrorBound}: at least ${required} ` +
          `fractional bits are required.`,
      );
    }
    return { feasible, requiredFracBits: required, reasons };
  }

  private times(): number[] {
    if (this.sortedTimes === null) {
      this.sortedTimes = [...this.points.keys()].sort((a, b) => a - b);
    }
    return this.sortedTimes;
  }

  /** Interpolated weight vector at `time`, in Q(F). Exact BigInt maths. */
  private weightVectorAt(time: number): bigint[] {
    const times = this.times();
    if (times.length === 0) {
      throw new RangeError("no control points have been added");
    }
    const scale = 1n << BigInt(this.fracBits);
    const toQ = (w: number): bigint => BigInt(w) * scale;

    if (time <= times[0]) return this.points.get(times[0])!.map(toQ);
    const last = times[times.length - 1];
    if (time >= last) return this.points.get(last)!.map(toQ);

    // Binary search: t0 <= time <= t1 with t0, t1 consecutive control times.
    let lo = 0;
    let hi = times.length - 1;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (times[mid] <= time) lo = mid;
      else hi = mid;
    }
    const t0 = times[lo];
    const t1 = times[hi];
    if (time === t0) return this.points.get(t0)!.map(toQ);
    const w0 = this.points.get(t0)!;
    const w1 = this.points.get(t1)!;
    const dt = BigInt(t1 - t0);
    const tt = BigInt(time - t0);
    // w(t) = (w0*(t1-t) + w1*(t-t0)) / (t1-t0), rounded to nearest Q(F).
    return w0.map((a, k) =>
      divRound((BigInt(a) * (dt - tt) + BigInt(w1[k]) * tt) * scale, dt),
    );
  }
}
