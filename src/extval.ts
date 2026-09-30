import { Rational } from './rational.ts';

/** Extended rational: a finite rational or +/- infinity (for unknown-error bounds). */
export type ExtVal =
  | { readonly kind: 'finite'; readonly v: Rational }
  | { readonly kind: 'inf'; readonly sign: 1 | -1 };

export const finite = (v: Rational): ExtVal => ({ kind: 'finite', v });
export const posInf: ExtVal = { kind: 'inf', sign: 1 };
export const negInf: ExtVal = { kind: 'inf', sign: -1 };

export function cmpExt(a: ExtVal, b: ExtVal): number {
  if (a.kind === 'inf' && b.kind === 'inf') return a.sign - b.sign;
  if (a.kind === 'inf') return a.sign;
  if (b.kind === 'inf') return -b.sign;
  return a.v.cmp(b.v);
}

export function addExt(a: ExtVal, b: ExtVal): ExtVal {
  if (a.kind === 'inf') {
    if (b.kind === 'inf' && b.sign !== a.sign) throw new Error('ExtVal: inf + (-inf)');
    return a;
  }
  if (b.kind === 'inf') return b;
  return finite(a.v.add(b.v));
}

/** Multiply an extended value by a finite rational. */
export function mulExt(c: Rational, x: ExtVal): ExtVal {
  if (x.kind === 'finite') return finite(c.mul(x.v));
  const s = c.sign();
  if (s === 0) return finite(Rational.zero());
  return { kind: 'inf', sign: (s * x.sign) as 1 | -1 };
}

export function extToString(x: ExtVal): string {
  if (x.kind === 'inf') return x.sign > 0 ? '+inf' : '-inf';
  return x.v.toString();
}
