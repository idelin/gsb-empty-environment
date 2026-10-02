/**
 * Continued fractions over exact rationals (BigInt only).
 * Terms use the floor convention, so negative targets work too:
 * e.g. -3.5 = [-4; 2] because -3.5 = -4 + 1/2.
 */

import { floor, make, type Rational } from "./rational.ts";

/** Simple continued-fraction terms [a0; a1, a2, ...] of an exact rational. Finite. */
export function cfTerms(x: Rational): bigint[] {
  const terms: bigint[] = [];
  let num = x.num;
  let den = x.den;
  // Invariant: current tail value is num/den with den > 0.
  for (;;) {
    const a = floor(make(num, den));
    terms.push(a);
    const restNum = num - a * den; // 0 <= restNum < den
    if (restNum === 0n) return terms;
    num = den;
    den = restNum;
  }
}

export interface Convergent {
  readonly p: bigint;
  readonly q: bigint;
}

/**
 * Walk the convergents of x. Yields p_{-1}/q_{-1} = 1/0 first as a sentinel
 * (useful for semiconvergent jumps), then p0/q0, p1/q1, ...
 */
export function* convergents(terms: bigint[]): Generator<Convergent> {
  let pm2 = 0n;
  let pm1 = 1n;
  let qm2 = 1n;
  let qm1 = 0n;
  yield { p: pm1, q: qm1 };
  for (const a of terms) {
    const p = a * pm1 + pm2;
    const q = a * qm1 + qm2;
    yield { p, q };
    pm2 = pm1;
    pm1 = p;
    qm2 = qm1;
    qm1 = q;
  }
}
