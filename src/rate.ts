import { divRound } from './rational.ts';

// Frame rate as an exact rational: num/den frames per second.
// e.g. NTSC "29.97" is 30000/1001, never a floating-point 29.97.
export type FrameRate = { readonly num: bigint; readonly den: bigint };

export function frameRate(num: bigint | number | string, den: bigint | number | string = 1n): FrameRate {
  const n = BigInt(num);
  const d = BigInt(den);
  if (n <= 0n || d <= 0n) throw new RangeError(`frame rate must be positive, got ${n}/${d}`);
  return { num: n, den: d };
}

export function parseFrameRate(text: string): FrameRate {
  const m = /^(\d+)(?:\/(\d+))?$/.exec(text.trim());
  if (!m) throw new SyntaxError(`invalid frame rate ${JSON.stringify(text)}; expected "30000/1001" or "24"`);
  return frameRate(m[1], m[2] ?? 1n);
}

// Nominal (integer) frame rate used for label fields: nearest integer to
// num/den, ties toward positive infinity. 30000/1001 -> 30, 24000/1001 -> 24.
export function nominalFps(rate: FrameRate): bigint {
  return divRound(rate.num, rate.den, 'nearest-half-up');
}

export const FPS_23_976: FrameRate = frameRate(24000, 1001);
export const FPS_24: FrameRate = frameRate(24);
export const FPS_25: FrameRate = frameRate(25);
export const FPS_29_97: FrameRate = frameRate(30000, 1001);
export const FPS_30: FrameRate = frameRate(30);
export const FPS_59_94: FrameRate = frameRate(60000, 1001);
export const FPS_60: FrameRate = frameRate(60);
