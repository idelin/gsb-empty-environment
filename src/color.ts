import { Fraction, frac } from "./fraction";

/**
 * Straight (non-premultiplied) RGBA color with exact rational channels.
 * Each channel is expected in [0, 1].
 */
export interface StraightColor {
  readonly r: Fraction;
  readonly g: Fraction;
  readonly b: Fraction;
  readonly a: Fraction;
}

/**
 * Premultiplied RGBA with exact rational channels. Compositing in this form
 * needs no division, so results stay canonical.
 */
export interface PremulColor {
  readonly r: Fraction;
  readonly g: Fraction;
  readonly b: Fraction;
  readonly a: Fraction;
}

export function straight(r: Fraction, g: Fraction, b: Fraction, a: Fraction): StraightColor {
  return { r, g, b, a };
}

export function toPremul(c: StraightColor): PremulColor {
  return { r: c.r.mul(c.a), g: c.g.mul(c.a), b: c.b.mul(c.a), a: c.a };
}

/** Straighten a premultiplied color; transparent black straightens to 0/0/0/0. */
export function toStraight(c: PremulColor): StraightColor {
  if (c.a.isZero) return { r: frac(0), g: frac(0), b: frac(0), a: frac(0) };
  return { r: c.r.div(c.a), g: c.g.div(c.a), b: c.b.div(c.a), a: c.a };
}

export const TRANSPARENT: PremulColor = {
  r: frac(0), g: frac(0), b: frac(0), a: frac(0),
};

/**
 * Porter-Duff "over": `top` composited over `bottom`, premultiplied form.
 *   a = at + ab*(1-at)
 *   P = Pt + Pb*(1-at)   (per channel)
 */
export function over(top: PremulColor, bottom: PremulColor): PremulColor {
  const oneMinusAt = frac(1).sub(top.a);
  return {
    r: top.r.add(bottom.r.mul(oneMinusAt)),
    g: top.g.add(bottom.g.mul(oneMinusAt)),
    b: top.b.add(bottom.b.mul(oneMinusAt)),
    a: top.a.add(bottom.a.mul(oneMinusAt)),
  };
}

/** Scale a premultiplied color by a factor (used for group opacity). */
export function scale(c: PremulColor, k: Fraction): PremulColor {
  return { r: c.r.mul(k), g: c.g.mul(k), b: c.b.mul(k), a: c.a.mul(k) };
}

export function equalsPremul(a: PremulColor, b: PremulColor): boolean {
  return a.r.equals(b.r) && a.g.equals(b.g) && a.b.equals(b.b) && a.a.equals(b.a);
}

export function premulKey(c: PremulColor): string {
  return `${c.r.toString()},${c.g.toString()},${c.b.toString()},${c.a.toString()}`;
}

export function premulToString(c: PremulColor): string {
  return `(${c.r.toString()}, ${c.g.toString()}, ${c.b.toString()}; a=${c.a.toString()})`;
}
