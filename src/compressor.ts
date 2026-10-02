import {
  angularDiffDeg,
  circumradiusM,
  haversineM,
} from './geo.ts';
import {
  DEFAULT_OPTIONS,
  type CompressorOptions,
  type KeptPoint,
  type TrajectoryPoint,
} from './types.ts';

const MANEUVER_REASONS = new Set(['turn-rate', 'radius', 'curve']);

/**
 * Streaming adaptive trajectory sampler for low-bandwidth vehicle terminals.
 *
 * Feed raw GNSS/IMU samples via {@link push}; retained points are returned
 * as they are decided. Call {@link flush} at end of stream to emit the
 * final boundary point.
 *
 * Sampling policy (evaluated on every incoming point):
 *  - dense sampling while |yaw rate| exceeds `turnRateDegPerSec`
 *    (sharp turns, lane changes, intersections);
 *  - keep when the accumulated heading change since the last kept point
 *    exceeds `headingAccumDeg` (gentle curves);
 *  - keep when the instantaneous turn radius drops below `minRadiusM`;
 *  - keep when the elevation delta versus the last kept point exceeds
 *    `eleStepM` (overpasses, ramps, tunnels);
 *  - keep when `maxGapM` / `maxGapMs` would be exceeded (error bound);
 *  - when a maneuver is detected, the sample immediately preceding it is
 *    also retained (`maneuver-entry`) so the curve is bracketed by its
 *    exact raw endpoints and the reconstructed turn radius stays exact;
 *  - straight-line driving — including straight-line acceleration — keeps
 *    only the segment endpoints.
 *
 * Retained points are passed through untouched: heading, position,
 * elevation and speed are bit-exact copies of the raw samples, so heading
 * angle and turn radius at retained (critical) points are zero-distortion.
 */
export class TrajectoryCompressor {
  private readonly opts: Required<CompressorOptions>;
  private lastKept: TrajectoryPoint | null = null;
  private prev: TrajectoryPoint | null = null;
  private prevPrev: TrajectoryPoint | null = null;
  private accumHeadingDeg = 0;
  private distSinceKeepM = 0;
  private emittedCount = 0;
  private receivedCount = 0;

  constructor(options: CompressorOptions = {}) {
    this.opts = { ...DEFAULT_OPTIONS, ...options };
  }

  /** Number of raw points received so far. */
  get received(): number {
    return this.receivedCount;
  }

  /** Number of points emitted so far. */
  get emitted(): number {
    return this.emittedCount;
  }

  /**
   * Push one raw sample. Returns the points retained as a consequence of
   * this sample (possibly empty; two when a maneuver entry is bracketed).
   */
  push(p: TrajectoryPoint): KeptPoint[] {
    this.receivedCount += 1;

    if (this.lastKept === null) {
      const first = this.keep(p, ['boundary']);
      this.prev = p;
      return [first];
    }

    const prev = this.prev as TrajectoryPoint;
    const dtSec = (p.t - prev.t) / 1000;
    const stepM = haversineM(prev.lat, prev.lon, p.lat, p.lon);
    this.distSinceKeepM += stepM;

    const reasons: string[] = [];

    if (dtSec > 0) {
      const yawRate = angularDiffDeg(p.heading, prev.heading) / dtSec;
      if (Math.abs(yawRate) >= this.opts.turnRateDegPerSec) {
        reasons.push('turn-rate');
      }
    }

    this.accumHeadingDeg += angularDiffDeg(p.heading, prev.heading);
    if (Math.abs(this.accumHeadingDeg) >= this.opts.headingAccumDeg) {
      reasons.push('curve');
    }

    if (this.prevPrev !== null && dtSec > 0) {
      const radius = circumradiusM(this.prevPrev, prev, p);
      if (radius < this.opts.minRadiusM) {
        reasons.push('radius');
      }
    }

    if (Math.abs(p.ele - this.lastKept.ele) >= this.opts.eleStepM) {
      reasons.push('elevation');
    }

    if (this.distSinceKeepM >= this.opts.maxGapM) {
      reasons.push('max-gap');
    }
    if (p.t - this.lastKept.t >= this.opts.maxGapMs) {
      reasons.push('max-gap');
    }

    const out: KeptPoint[] = [];
    if (reasons.length > 0) {
      const isManeuver = reasons.some((r) => MANEUVER_REASONS.has(r));
      if (isManeuver && prev !== this.lastKept) {
        out.push(this.keep(prev, ['maneuver-entry']));
      }
      out.push(this.keep(p, reasons));
    }

    this.prevPrev = prev;
    this.prev = p;
    return out;
  }

  /** End of stream: always retain the final sample as a boundary point. */
  flush(): KeptPoint | null {
    if (this.prev === null || this.prev === this.lastKept) {
      return null;
    }
    return this.keep(this.prev, ['boundary']);
  }

  private keep(p: TrajectoryPoint, reasons: string[]): KeptPoint {
    const kept: KeptPoint = {
      t: p.t,
      lat: p.lat,
      lon: p.lon,
      ele: p.ele,
      speed: p.speed,
      heading: p.heading,
      reasons,
    };
    this.lastKept = p;
    this.accumHeadingDeg = 0;
    this.distSinceKeepM = 0;
    this.emittedCount += 1;
    return kept;
  }
}

/** Convenience batch wrapper around the streaming compressor. */
export function compress(
  points: TrajectoryPoint[],
  options: CompressorOptions = {},
): KeptPoint[] {
  const compressor = new TrajectoryCompressor(options);
  const out: KeptPoint[] = [];
  for (const p of points) {
    out.push(...compressor.push(p));
  }
  const tail = compressor.flush();
  if (tail !== null) out.push(tail);
  return out;
}
