/**
 * Independent certificate checker. It re-derives the mathematics from
 * scratch (exact rational ranges for interval ops; exact coefficient
 * recomputation plus adaptive rational refinement for affine nonlinear
 * steps) and confirms:
 *   1. outward rounding direction: a reported lower bound never exceeds the
 *      true lower bound, a reported upper bound never falls below it;
 *   2. containment: every exact range piece lies inside a reported segment;
 *   3. affine remainders dominate the true nonlinear error.
 */

import { Decimal } from './decimal.ts';
import { Rational } from './rational.ts';
import type { IntervalOpCert } from './interval.ts';
import type { AffineCert, AffineMulCert, AffineRecipCert } from './affine.ts';
import { exactRange, fromInterval, containsRInterval, showR } from './exact-range.ts';
import type { RInterval } from './exact-range.ts';

export interface CheckResult {
  readonly ok: boolean;
  readonly reason?: string;
}

const OK: CheckResult = { ok: true };
function fail(reason: string): CheckResult {
  return { ok: false, reason };
}

export function verifyIntervalOp(cert: IntervalOpCert): CheckResult {
  if (cert.segments.some((s) => s.kind === 'empty') && cert.op !== 'div') {
    return fail(`${cert.op}: empty segment reported for a non-empty-domain operation`);
  }
  const pieces = exactRange(cert.op, cert.lhs, cert.rhs).filter((p) => p.kind !== 'empty');
  const segments = cert.segments.filter((s) => s.kind !== 'empty');
  if (pieces.length === 0) {
    return segments.length === 0
      ? OK
      : fail(`${cert.op}: true range is empty but ${segments.length} segment(s) reported`);
  }
  if (segments.length === 0) return fail(`${cert.op}: true range is non-empty but no segment reported`);
  for (const piece of pieces) {
    const covered = segments.some((s) => containsRInterval(fromInterval(s), piece));
    if (!covered) {
      return fail(
        `${cert.op}: exact range piece ${showR(piece)} is not contained in any reported ` +
          `segment (outward rounding direction violated or result too narrow)`,
      );
    }
  }
  return OK;
}

function termsOf(entries: readonly [number, Decimal][]): Map<number, Decimal> {
  return new Map(entries);
}

function sumAbs(terms: Map<number, Decimal>): Decimal {
  let r = Decimal.ZERO;
  for (const c of terms.values()) r = r.add(c.abs());
  return r;
}

export function verifyAffineMul(cert: AffineMulCert): CheckResult {
  const lt = termsOf(cert.lhsTerms);
  const rt = termsOf(cert.rhsTerms);
  const res = termsOf(cert.resultTerms);
  if (!cert.lhsCenter.mul(cert.rhsCenter).eq(cert.resultCenter)) {
    return fail('affine-mul: result center is not the exact product of input centers');
  }
  if (lt.has(cert.symbol) || rt.has(cert.symbol)) {
    return fail('affine-mul: remainder symbol collides with an input symbol');
  }
  const keys = new Set<number>([...lt.keys(), ...rt.keys()]);
  for (const k of keys) {
    const ai = lt.get(k) ?? Decimal.ZERO;
    const bi = rt.get(k) ?? Decimal.ZERO;
    const expected = cert.lhsCenter.mul(bi).add(cert.rhsCenter.mul(ai));
    const got = res.get(k) ?? Decimal.ZERO;
    if (!expected.eq(got)) {
      return fail(`affine-mul: linear coefficient of symbol ${k} is ${got}, expected exact ${expected}`);
    }
  }
  const bound = sumAbs(lt).mul(sumAbs(rt));
  if (cert.remainder.sign() < 0 || cert.remainder.cmp(bound) < 0) {
    return fail(`affine-mul: remainder ${cert.remainder} below exact nonlinear bound ${bound}`);
  }
  const gotRem = res.get(cert.symbol) ?? Decimal.ZERO;
  if (!gotRem.eq(cert.remainder)) {
    return fail('affine-mul: result does not carry the certified remainder coefficient');
  }
  for (const k of res.keys()) {
    if (!keys.has(k) && k !== cert.symbol) return fail(`affine-mul: unexpected symbol ${k} in result`);
  }
  return OK;
}

/** Rational range bound of g(x) = 1/x - alpha*x - zeta on [u, v] (same sign). */
function boundG(
  u: Rational,
  v: Rational,
  alpha: Rational,
  zeta: Rational,
): { lo: Rational; hi: Rational } {
  const rLo = Rational.ONE.div(v); // 1/x is decreasing on a same-sign interval
  const rHi = Rational.ONE.div(u);
  const au = alpha.mul(u);
  const av = alpha.mul(v);
  const aLo = au.cmp(av) <= 0 ? au : av;
  const aHi = au.cmp(av) <= 0 ? av : au;
  return { lo: rLo.sub(aHi).sub(zeta), hi: rHi.sub(aLo).sub(zeta) };
}

/** Adaptive rigorous proof that |g| <= delta on [lo, hi] via bisection. */
function coversRemainder(
  lo: Rational,
  hi: Rational,
  alpha: Rational,
  zeta: Rational,
  delta: Rational,
  depth: number,
): boolean {
  const b = boundG(lo, hi, alpha, zeta);
  if (b.lo.cmp(delta.neg()) >= 0 && b.hi.cmp(delta) <= 0) return true;
  if (depth === 0) return false;
  const mid = lo.add(hi).div(Rational.TWO);
  return (
    coversRemainder(lo, mid, alpha, zeta, delta, depth - 1) &&
    coversRemainder(mid, hi, alpha, zeta, delta, depth - 1)
  );
}

export function verifyAffineRecip(cert: AffineRecipCert): CheckResult {
  const { lo, hi } = cert;
  if (lo.cmp(hi) > 0) return fail('affine-recip: empty input range');
  if (lo.sign() === 0 || hi.sign() === 0 || lo.sign() !== hi.sign()) {
    return fail('affine-recip: input range contains zero');
  }
  const it = termsOf(cert.inputTerms);
  const res = termsOf(cert.resultTerms);
  const expectedCenter = cert.alpha.mul(cert.inputCenter).add(cert.zeta);
  if (!expectedCenter.eq(cert.resultCenter)) {
    return fail('affine-recip: result center is not alpha*center + zeta');
  }
  for (const [k, c] of it) {
    const got = res.get(k) ?? Decimal.ZERO;
    if (!cert.alpha.mul(c).eq(got)) {
      return fail(`affine-recip: coefficient of symbol ${k} is not alpha-scaled exactly`);
    }
  }
  const gotDelta = res.get(cert.symbol) ?? Decimal.ZERO;
  if (it.has(cert.symbol)) return fail('affine-recip: remainder symbol collides with an input symbol');
  if (!gotDelta.eq(cert.delta)) return fail('affine-recip: result does not carry the certified delta');
  for (const k of res.keys()) {
    if (!it.has(k) && k !== cert.symbol) return fail(`affine-recip: unexpected symbol ${k} in result`);
  }
  if (cert.delta.sign() < 0) return fail('affine-recip: negative delta');
  const rLo = Rational.fromDecimal(lo);
  const rHi = Rational.fromDecimal(hi);
  const rAlpha = Rational.fromDecimal(cert.alpha);
  const rZeta = Rational.fromDecimal(cert.zeta);
  const rDelta = Rational.fromDecimal(cert.delta);
  if (!coversRemainder(rLo, rHi, rAlpha, rZeta, rDelta, 24)) {
    return fail('affine-recip: delta does not provably cover |1/x - alpha*x - zeta| on the input range');
  }
  return OK;
}

export function verifyAffine(cert: AffineCert): CheckResult {
  return cert.kind === 'affine-mul' ? verifyAffineMul(cert) : verifyAffineRecip(cert);
}

export type Certificate = IntervalOpCert | AffineCert;

export function verify(cert: Certificate): CheckResult {
  return cert.kind === 'interval-op' ? verifyIntervalOp(cert) : verifyAffine(cert);
}

/** Convenience: verify a batch and report the first failure, if any. */
export function verifyAll(certs: readonly Certificate[]): CheckResult {
  for (const c of certs) {
    const r = verify(c);
    if (!r.ok) return r;
  }
  return OK;
}
