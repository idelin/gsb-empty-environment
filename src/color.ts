import { Rational } from "./rational";

/** Non-premultiplied RGB color with exact rational channels channels in [0, 1]. */
export interface Color {
  readonly r: Rational;
  readonly g: Rational;
  readonly b: Rational;
}

export function rgb(r: Rational, g: Rational, b: Rational): Color {
  return { r, g, b };
}

export const BLACK: Color = {
  r: Rational.ZERO,
  g: Rational.ZERO,
  b: Rational.ZERO,
};

export function colorEquals(a: Color, b: Color): boolean {
  return a.r.equals(b.r) && a.g.equals(b.g) && a.b.equals(b.b);
}

export function colorKey(c: Color): string {
  return `${c.r.toString()},${c.g.toString()},${c.b.toString()}`;
}
