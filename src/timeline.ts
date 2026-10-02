// Cross-rule-version edit splicing. Each segment carries its own compiled
// rule (its own fps and drop table), so a timeline can mix e.g. 29.97 DF
// and 23.976 NDF material. Cut times are exact rational sums of segment
// durations; audio sample alignment snaps a cut to the sample grid with an
// explicit rounding mode and reports the residual error as a Rational.

import { add, mul, rat, sub, type Rational, type RoundingMode, roundToBigInt } from "./rational.ts";
import type { CompiledRule } from "./timecode.ts";

export interface Segment {
  readonly rule: CompiledRule;
  readonly sourceStartFrame: bigint; // first source frame used, in the segment rule's numbering
  readonly frames: bigint; // segment duration in frames (> 0)
}

export interface LocatedFrame {
  readonly segmentIndex: number;
  readonly sourceFrame: bigint;
  readonly offsetInSegment: bigint;
}

export interface AudioCut {
  readonly time: Rational; // exact cut time in seconds
  readonly sample: bigint; // aligned sample index
  readonly error: Rational; // sample/sampleRate - time, in seconds (signed)
}

export class Timeline {
  readonly segments: readonly Segment[];
  readonly totalFrames: bigint;
  private readonly starts: readonly bigint[]; // output frame where each segment begins

  constructor(segments: readonly Segment[]) {
    if (segments.length === 0) throw new RangeError("timeline needs at least one segment");
    const starts: bigint[] = [];
    let acc = 0n;
    for (const seg of segments) {
      if (seg.frames <= 0n) throw new RangeError("segment frames must be > 0");
      if (seg.sourceStartFrame < 0n) throw new RangeError("negative time is not supported");
      starts.push(acc);
      acc += seg.frames;
    }
    this.segments = Object.freeze(segments.slice());
    this.starts = Object.freeze(starts);
    this.totalFrames = acc;
  }

  // Output frame -> (segment, source frame). Binary search over segment
  // starts; segment count is small, frame counts may be astronomically large.
  locate(outFrame: bigint): LocatedFrame {
    if (outFrame < 0n || outFrame >= this.totalFrames) {
      throw new RangeError(`output frame ${outFrame} outside [0, ${this.totalFrames})`);
    }
    let lo = 0;
    let hi = this.segments.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.starts[mid] <= outFrame) lo = mid;
      else hi = mid - 1;
    }
    const offset = outFrame - this.starts[lo];
    return { segmentIndex: lo, sourceFrame: this.segments[lo].sourceStartFrame + offset, offsetInSegment: offset };
  }

  // Exact duration of one segment in seconds: frames / fps.
  segmentDuration(index: number): Rational {
    const seg = this.segments[index];
    return rat(seg.frames * seg.rule.fps.den, seg.rule.fps.num);
  }

  // Exact time in seconds of the cut at an output-frame boundary
  // (0 .. totalFrames). Sums whole-segment durations, then adds the
  // fractional segment — rational all the way, no float accumulation.
  cutTime(boundary: bigint): Rational {
    if (boundary < 0n || boundary > this.totalFrames) {
      throw new RangeError(`cut boundary ${boundary} outside [0, ${this.totalFrames}]`);
    }
    let remaining = boundary;
    let time = rat(0n);
    for (let i = 0; i < this.segments.length && remaining > 0n; i++) {
      const seg = this.segments[i];
      const take = remaining < seg.frames ? remaining : seg.frames;
      time = add(time, rat(take * seg.rule.fps.den, seg.rule.fps.num));
      remaining -= take;
    }
    return time;
  }

  // Snap a cut to the audio sample grid. Error is exact:
  //   error = alignedSample / sampleRate - cutTime   (seconds, signed)
  audioCut(boundary: bigint, sampleRate: bigint, mode: RoundingMode): AudioCut {
    if (sampleRate <= 0n) throw new RangeError("sampleRate must be positive");
    const time = this.cutTime(boundary);
    const sample = roundToBigInt(mul(time, rat(sampleRate)), mode);
    const error = sub(rat(sample, sampleRate), time);
    return { time, sample, error };
  }
}
