import { divRound, rational, subR, type Rational, type RoundingMode } from './rational.ts';
import type { FrameRate } from './rate.ts';

// Frame <-> audio sample conversion at a given sample rate.
// The exact position is always a rational; rounding to an integer happens
// once, with the caller's explicit mode, and the residual is returned as a
// rational error (rounded value minus exact value).

export type FrameToSamples = {
  readonly samples: bigint; // rounded sample number
  readonly exact: Rational; // exact sample position (frames * sampleRate * den / num)
  readonly error: Rational; // samples - exact, in sample units
};

export function frameToSamples(
  frame: bigint,
  rate: FrameRate,
  sampleRate: bigint,
  mode: RoundingMode,
): FrameToSamples {
  if (sampleRate <= 0n) throw new RangeError('sampleRate must be positive');
  const exact = rational(frame * sampleRate * rate.den, rate.num);
  const samples = divRound(exact.n, exact.d, mode);
  return { samples, exact, error: subR(rational(samples), exact) };
}

export type SamplesToFrame = {
  readonly frame: bigint; // rounded frame number
  readonly exact: Rational; // exact frame position (samples * num / (sampleRate * den))
  readonly error: Rational; // frame - exact, in frame units
};

export function samplesToFrame(
  samples: bigint,
  rate: FrameRate,
  sampleRate: bigint,
  mode: RoundingMode,
): SamplesToFrame {
  if (sampleRate <= 0n) throw new RangeError('sampleRate must be positive');
  const exact = rational(samples * rate.num, sampleRate * rate.den);
  const frame = divRound(exact.n, exact.d, mode);
  return { frame, exact, error: subR(rational(frame), exact) };
}
