import { Fraction } from './fraction.ts';

export const CHANNEL_MAX = 255;

export interface Rgba8 {
  r: number;
  g: number;
  b: number;
  a: number;
}

export interface ExactPremult {
  r: Fraction;
  g: Fraction;
  b: Fraction;
  a: Fraction;
}

const isByte = (v: number): boolean => Number.isInteger(v) && v >= 0 && v <= CHANNEL_MAX;

export function assertRgba8(c: Rgba8): void {
  if (!isByte(c.r) || !isByte(c.g) || !isByte(c.b) || !isByte(c.a)) {
    throw new Error(`Invalid 8-bit RGBA color: ${JSON.stringify(c)}`);
  }
}

export function transparentExact(): ExactPremult {
  const z = Fraction.zero();
  return { r: z, g: z, b: z, a: z };
}

// Zero alpha is handled separately: a fully transparent straight pixel has no
// meaningful hue, so it maps to transparent black regardless of its RGB bytes.
export function fromStraight8(c: Rgba8): ExactPremult {
  assertRgba8(c);
  if (c.a === 0) return transparentExact();
  const alpha = Fraction.of(c.a);
  const scale = Fraction.of(1, CHANNEL_MAX);
  return {
    r: Fraction.of(c.r).mul(alpha).mul(scale),
    g: Fraction.of(c.g).mul(alpha).mul(scale),
    b: Fraction.of(c.b).mul(alpha).mul(scale),
    a: alpha,
  };
}

export function fromPremult8(c: Rgba8): ExactPremult {
  assertRgba8(c);
  if (c.r > c.a || c.g > c.a || c.b > c.a) {
    throw new Error(`Invalid premultiplied color (channel > alpha): ${JSON.stringify(c)}`);
  }
  return {
    r: Fraction.of(c.r),
    g: Fraction.of(c.g),
    b: Fraction.of(c.b),
    a: Fraction.of(c.a),
  };
}

// Porter-Duff "over" in a linear color space, on the 0..255 scale, exact.
export function over(top: ExactPremult, bottom: ExactPremult): ExactPremult {
  const remain = Fraction.of(CHANNEL_MAX).sub(top.a);
  const combine = (t: Fraction, b: Fraction): Fraction =>
    t.add(b.mul(remain).div(Fraction.of(CHANNEL_MAX)));
  return {
    r: combine(top.r, bottom.r),
    g: combine(top.g, bottom.g),
    b: combine(top.b, bottom.b),
    a: combine(top.a, bottom.a),
  };
}

export function roundHalfUp(value: Fraction): number {
  const doubled = 2n * value.num + value.den;
  const q = doubled / (2n * value.den);
  return Number(q);
}

export function quantizePremult8(c: ExactPremult): Rgba8 {
  return {
    r: roundHalfUp(c.r),
    g: roundHalfUp(c.g),
    b: roundHalfUp(c.b),
    a: roundHalfUp(c.a),
  };
}

// Zero alpha is handled separately: un-premultiplying would divide by zero,
// so a fully transparent result is reported as transparent black.
export function quantizeStraight8(c: ExactPremult): Rgba8 {
  const a = roundHalfUp(c.a);
  if (c.a.isZero()) return { r: 0, g: 0, b: 0, a: 0 };
  const alpha = c.a;
  const unpremult = (ch: Fraction): number =>
    roundHalfUp(ch.mul(Fraction.of(CHANNEL_MAX)).div(alpha));
  return { r: unpremult(c.r), g: unpremult(c.g), b: unpremult(c.b), a };
}

export function rgba8Equals(x: Rgba8, y: Rgba8): boolean {
  return x.r === y.r && x.g === y.g && x.b === y.b && x.a === y.a;
}
