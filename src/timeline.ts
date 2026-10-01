import {
  addR,
  cmpR,
  divRound,
  mulR,
  rational,
  subR,
  type Rational,
  type RoundingMode,
} from './rational.ts';
import type { FrameRate } from './rate.ts';
import type { DropFrameRule } from './dropframe.ts';
import { Timecode, type NegativePolicy } from './timecode.ts';

// Cross-rule-version edit splicing. Each segment carries its own frame rate
// and drop-frame rule ("rule version"); the timeline concatenates segments by
// frame count and keeps audio alignment exact: segment boundaries in sample
// space are rationals, and any rounding to integer samples reports its
// residual as a rational error.

export type Segment = {
  readonly name?: string;
  readonly rate: FrameRate;
  readonly rule?: DropFrameRule | null;
  readonly frames: bigint | number; // segment duration in its own frames
  readonly negative?: NegativePolicy;
};

type NormSegment = {
  readonly name: string;
  readonly tc: Timecode;
  readonly frames: bigint;
  readonly start: bigint; // global frame number where this segment begins
};

export type LocatedFrame = {
  readonly index: number;
  readonly segment: string;
  readonly localFrame: bigint;
  readonly label: string;
};

export type SampleLocation = LocatedFrame & {
  readonly exactLocalFrames: Rational; // exact local frame position
  readonly sampleError: Rational; // input samples minus exact samples of the rounded frame
};

export class Timeline {
  readonly segments: readonly NormSegment[];
  readonly totalFrames: bigint;

  constructor(segments: readonly Segment[]) {
    if (segments.length === 0) throw new RangeError('timeline needs at least one segment');
    let start = 0n;
    this.segments = segments.map((s, i) => {
      const frames = BigInt(s.frames);
      if (frames <= 0n) throw new RangeError(`segment ${i} has non-positive duration ${frames}`);
      const norm: NormSegment = {
        name: s.name ?? `seg${i}`,
        tc: new Timecode({ rate: s.rate, rule: s.rule ?? null, negative: s.negative ?? 'reject' }),
        frames,
        start,
      };
      start += frames;
      return norm;
    });
    this.totalFrames = start;
  }

  locate(globalFrame: bigint): LocatedFrame {
    if (globalFrame < 0n || globalFrame >= this.totalFrames) {
      throw new RangeError(`frame ${globalFrame} outside [0, ${this.totalFrames})`);
    }
    let lo = 0;
    let hi = this.segments.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.segments[mid].start <= globalFrame) lo = mid;
      else hi = mid - 1;
    }
    const seg = this.segments[lo];
    const localFrame = globalFrame - seg.start;
    return { index: lo, segment: seg.name, localFrame, label: seg.tc.frameToLabel(localFrame) };
  }

  labelAt(globalFrame: bigint): string {
    return this.locate(globalFrame).label;
  }

  // Exact cumulative sample position of every segment boundary (length
  // segments+1; first is 0, last is the timeline end). Always rational.
  boundarySamples(sampleRate: bigint): Rational[] {
    if (sampleRate <= 0n) throw new RangeError('sampleRate must be positive');
    const out: Rational[] = [rational(0n)];
    let acc = rational(0n);
    for (const seg of this.segments) {
      acc = addR(acc, rational(seg.frames * sampleRate * seg.tc.rate.den, seg.tc.rate.num));
      out.push(acc);
    }
    return out;
  }

  // Boundary positions rounded to integer samples with an explicit mode;
  // each entry carries its rational rounding residual.
  boundarySamplesAligned(
    sampleRate: bigint,
    mode: RoundingMode,
  ): { samples: bigint; error: Rational }[] {
    return this.boundarySamples(sampleRate).map((exact) => {
      const samples = divRound(exact.n, exact.d, mode);
      return { samples, error: subR(rational(samples), exact) };
    });
  }

  // Audio sample -> frame alignment: finds the segment containing the sample
  // offset, converts to a local frame with the explicit rounding mode, and
  // reports the residual misalignment as a rational number of samples.
  locateBySamples(samples: bigint, sampleRate: bigint, mode: RoundingMode): SampleLocation {
    const bounds = this.boundarySamples(sampleRate);
    const end = bounds[bounds.length - 1];
    if (samples < 0n || cmpR(rational(samples), end) >= 0) {
      throw new RangeError(`sample ${samples} outside [0, ${end.n}/${end.d})`);
    }
    let lo = 0;
    let hi = this.segments.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (cmpR(bounds[mid], rational(samples)) <= 0) lo = mid;
      else hi = mid - 1;
    }
    const seg = this.segments[lo];
    const offset = subR(rational(samples), bounds[lo]);
    const exactLocal = mulR(offset, rational(seg.tc.rate.num, sampleRate * seg.tc.rate.den));
    let localFrame = divRound(exactLocal.n, exactLocal.d, mode);
    if (localFrame >= seg.frames) {
      // Rounding carried us past the segment end: land on the first frame of
      // the next segment (or fail at the timeline end).
      if (lo === this.segments.length - 1) {
        throw new RangeError(`sample ${samples} rounds past the timeline end`);
      }
      const next = this.segments[lo + 1];
      return {
        index: lo + 1,
        segment: next.name,
        localFrame: 0n,
        label: next.tc.frameToLabel(0n),
        exactLocalFrames: rational(0n),
        sampleError: subR(rational(samples), bounds[lo + 1]),
      };
    }
    const exactSamples = addR(
      bounds[lo],
      rational(localFrame * sampleRate * seg.tc.rate.den, seg.tc.rate.num),
    );
    return {
      index: lo,
      segment: seg.name,
      localFrame,
      label: seg.tc.frameToLabel(localFrame),
      exactLocalFrames: exactLocal,
      sampleError: subR(rational(samples), exactSamples),
    };
  }
}
