/**
 * Exact rational counterexample search. Sampling can never prove a bound,
 * but every sample is evaluated with exact rational arithmetic (no floats),
 * so any violation found is a genuine, exactly-representable counterexample.
 * Also provides `sampledTruthCheck`: the same expression is evaluated once
 * over affine forms (producing a certified enclosing interval) and once over
 * exact rationals at grid points, asserting every sampled truth lies inside.
 */

import { Decimal } from './decimal.ts';
import { Rational } from './rational.ts';
import type { Interval, IntervalOpCert } from './interval.ts';
import { containsPoint } from './interval.ts';
import type { AffineCert, AffineMulCert, AffineRecipCert } from './affine.ts';
import { AffineForm } from './affine.ts';
import { fromInterval, containsRational } from './exact-range.ts';

export interface Counterexample {
  readonly context: string;
  readonly points: readonly string[];
  readonly value: string; // exact rational
}

/** Deterministic candidate rationals inside (or representative of) an interval. */
function candidatePoints(iv: Interval, grid: number): Rational[] {
  if (iv.kind !== 'bounded') return [];
  const pts: Rational[] = [];
  const lo = iv.lo === null ? null : Rational.fromDecimal(iv.lo);
  const hi = iv.hi === null ? null : Rational.fromDecimal(iv.hi);
  if (lo !== null) pts.push(lo);
  if (hi !== null) pts.push(hi);
  if (lo !== null && hi !== null) {
    pts.push(lo.add(hi).div(Rational.TWO));
    for (let i = 1; i < grid - 1; i++) {
      pts.push(lo.add(hi.sub(lo).mul(Rational.of(BigInt(i), BigInt(grid - 1)))));
    }
  } else {
    pts.push(Rational.ZERO, Rational.ONE, Rational.ONE.neg());
    pts.push(Rational.of(1000n), Rational.of(-1000n));
  }
  if (containsPoint(iv, Decimal.ZERO)) pts.push(Rational.ZERO);
  return pts;
}

function evalOp(op: IntervalOpCert['op'], x: Rational, y: Rational): Rational | null {
  switch (op) {
    case 'add': return x.add(y);
    case 'sub': return x.sub(y);
    case 'mul': return x.mul(y);
    case 'div': return y.isZero() ? null : x.div(y);
  }
}

/** Searches for an exact sample whose true value escapes every reported segment. */
export function searchIntervalOp(cert: IntervalOpCert, grid = 7): Counterexample | null {
  const xs = candidatePoints(cert.lhs, grid);
  const ys = candidatePoints(cert.rhs, grid);
  const segments = cert.segments.filter((s) => s.kind !== 'empty').map(fromInterval);
  for (const x of xs) {
    for (const y of ys) {
      const v = evalOp(cert.op, x, y);
      if (v === null) continue; // sample outside the operation's domain
      if (!segments.some((s) => containsRational(s, v))) {
        return {
          context: `interval-op ${cert.op} @ scale ${cert.scale}`,
          points: [x.toString(), y.toString()],
          value: v.toString(),
        };
      }
    }
  }
  return null;
}

const EPS_VALUES = ['-1', '-0.5', '0', '0.5', '1'].map(Decimal.parse);

function* assignments(symbols: readonly number[], cap: number): Generator<Map<number, Decimal>> {
  if (symbols.length <= 3) {
    const total = EPS_VALUES.length ** symbols.length;
    for (let n = 0; n < total; n++) {
      const m = new Map<number, Decimal>();
      let k = n;
      for (const s of symbols) {
        m.set(s, EPS_VALUES[k % EPS_VALUES.length]!);
        k = Math.floor(k / EPS_VALUES.length);
      }
      yield m;
    }
  } else {
    let state = 0x9e3779b9n;
    for (let n = 0; n < cap; n++) {
      const m = new Map<number, Decimal>();
      for (const s of symbols) {
        state = (state * 6364136223846793005n + 1442695040888963407n) & ((1n << 64n) - 1n);
        m.set(s, EPS_VALUES[Number(state >> 33n) % EPS_VALUES.length]!);
      }
      yield m;
    }
  }
}

/** Samples noise assignments; a remainder that is too small shows up here. */
export function searchAffineMul(cert: AffineMulCert): Counterexample | null {
  const lhs = new AffineForm(cert.lhsCenter, new Map(cert.lhsTerms));
  const rhs = new AffineForm(cert.rhsCenter, new Map(cert.rhsTerms));
  // The claimed enclosure: center + linear terms + cert.remainder * eps_new.
  const linearTerms = cert.resultTerms.filter(([k]) => k !== cert.symbol);
  const res = new AffineForm(cert.resultCenter, new Map(linearTerms));
  const r = res.radius().add(cert.remainder.abs());
  const lo = cert.resultCenter.sub(r);
  const hi = cert.resultCenter.add(r);
  const symbols = [...new Set([...cert.lhsTerms, ...cert.rhsTerms].map(([k]) => k))];
  for (const assign of assignments(symbols, 2000)) {
    const x = lhs.evaluate(assign);
    const y = rhs.evaluate(assign);
    const v = x.mul(y);
    if (v.cmp(lo) < 0 || v.cmp(hi) > 0) {
      return {
        context: 'affine-mul remainder',
        points: [x.toString(), y.toString()],
        value: v.toString(),
      };
    }
  }
  return null;
}

/** Samples x in [lo, hi]; checks |1/x - alpha*x - zeta| <= delta exactly. */
export function searchAffineRecip(cert: AffineRecipCert, samples = 64): Counterexample | null {
  const lo = Rational.fromDecimal(cert.lo);
  const hi = Rational.fromDecimal(cert.hi);
  const alpha = Rational.fromDecimal(cert.alpha);
  const zeta = Rational.fromDecimal(cert.zeta);
  const delta = Rational.fromDecimal(cert.delta);
  for (let i = 0; i <= samples; i++) {
    const x = lo.add(hi.sub(lo).mul(Rational.of(BigInt(i), BigInt(samples))));
    if (x.isZero()) continue;
    const g = Rational.ONE.div(x).sub(alpha.mul(x)).sub(zeta).abs();
    if (g.cmp(delta) > 0) {
      return {
        context: 'affine-recip remainder',
        points: [x.toString()],
        value: `|g|=${g.toString()} > delta=${delta.toString()}`,
      };
    }
  }
  return null;
}

export function searchAffine(cert: AffineCert): Counterexample | null {
  return cert.kind === 'affine-mul' ? searchAffineMul(cert) : searchAffineRecip(cert);
}

/** Generic arithmetic so one expression runs on affine forms and on rationals. */
export interface Arith<T> {
  add(a: T, b: T): T;
  sub(a: T, b: T): T;
  mul(a: T, b: T): T;
  div(a: T, b: T): T;
  constant(text: string): T;
}

class DivByZero extends Error {}

export interface SampledTruthResult {
  readonly output: Interval;
  readonly certs: readonly AffineCert[];
  readonly checked: number;
  readonly skipped: number;
  readonly violations: readonly string[];
}

/**
 * Evaluates `fn` on affine forms built from `inputIntervals` (each input gets
 * one shared noise symbol), then re-evaluates it exactly at rational grid
 * points and verifies every sampled truth lies in the output interval.
 */
export function sampledTruthCheck(
  fn: <T>(ops: Arith<T>, inputs: T[]) => T,
  inputIntervals: Interval[],
  scale: number,
  grid = 5,
): SampledTruthResult {
  const certs: AffineCert[] = [];
  const affineOps: Arith<AffineForm> = {
    add: (a, b) => a.add(b),
    sub: (a, b) => a.sub(b),
    mul: (a, b) => {
      const r = a.mul(b, scale);
      certs.push(r.cert);
      return r.form;
    },
    div: (a, b) => {
      const r = a.div(b, scale);
      certs.push(...r.certs);
      return r.form;
    },
    constant: (t) => AffineForm.constant(Decimal.parse(t)),
  };
  const affineInputs = inputIntervals.map((iv) => AffineForm.fromInterval(iv));
  const output = fn(affineOps, affineInputs).toInterval(scale);

  const rationalOps: Arith<Rational> = {
    add: (a, b) => a.add(b),
    sub: (a, b) => a.sub(b),
    mul: (a, b) => a.mul(b),
    div: (a, b) => {
      if (b.isZero()) throw new DivByZero();
      return a.div(b);
    },
    constant: (t) => Rational.fromDecimal(Decimal.parse(t)),
  };

  const perInput = inputIntervals.map((iv) => {
    if (iv.kind !== 'bounded' || iv.lo === null || iv.hi === null) {
      throw new RangeError('sampledTruthCheck: inputs must be finite bounded intervals');
    }
    return candidatePoints(iv, grid);
  });

  const out = fromInterval(output);
  const violations: string[] = [];
  let checked = 0;
  let skipped = 0;
  const combo = new Array<Rational>(perInput.length);
  const walk = (i: number): void => {
    if (i === perInput.length) {
      let v: Rational;
      try {
        v = fn(rationalOps, combo.slice());
      } catch (e) {
        if (e instanceof DivByZero) {
          skipped++;
          return;
        }
        throw e;
      }
      checked++;
      if (!containsRational(out, v)) {
        violations.push(`f(${combo.map((c) => c.toString()).join(', ')}) = ${v.toString()} not in output`);
      }
      return;
    }
    for (const p of perInput[i]!) {
      combo[i] = p;
      walk(i + 1);
    }
  };
  walk(0);
  return { output, certs, checked, skipped, violations };
}
