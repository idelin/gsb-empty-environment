import type { Rational } from './rational.ts';
import {
  abs,
  add,
  ceilBig,
  cmp,
  div,
  eq,
  floorBig,
  fromBigInt,
  isZero,
  make,
  max as rmax,
  min as rmin,
  mul,
  ONE,
  sub,
  ZERO,
} from './rational.ts';
import type { Interval } from './interval.ts';
import { contains, intersect, isEmpty, mergeIntervals, pointInterval, wholeLine } from './interval.ts';
import { preimage } from './rounding.ts';
import type { Compiled, Piece, Term } from './compile.ts';
import { evalTerm } from './compile.ts';
import { evaluate } from './evaluate.ts';

const TWO: Rational = { n: 2n, d: 1n };
const MAX_ROUNDING_STEPS = 1_000_000n;

/**
 * Inverse solving: exact set of x with f(x) === y, as a list of disjoint intervals.
 *
 * Structure: f = finalRound ∘ clamp ∘ S, where S is the compiled core (sum of terms).
 *   f(x) = y  ⟺  S(x) ∈ clampPreimage(roundPreimage(y)).
 * On every atomic piece each term is linear, a rounding step, or a clamped linear,
 * so the solution inside a piece is computed exactly by enumerating step boundaries.
 */

function evalTerms(terms: readonly Term[], x: Rational): Rational {
  let s = ZERO;
  for (const t of terms) s = add(s, evalTerm(t, x));
  return s;
}

/** x-interval solving A*x + B ∈ J (no domain constraint). */
function linearPreimage(A: Rational, B: Rational, J: Interval): Interval {
  if (isZero(A)) return contains(J, B) ? wholeLine() : { lo: { value: ZERO, closed: false }, hi: { value: ZERO, closed: false } };
  let lo = J.lo.value === null ? { value: null, closed: false } : { value: div(sub(J.lo.value, B), A), closed: J.lo.closed };
  let hi = J.hi.value === null ? { value: null, closed: false } : { value: div(sub(J.hi.value, B), A), closed: J.hi.closed };
  if (cmp(A, ZERO) < 0) [lo, hi] = [hi, lo];
  return { lo, hi } as Interval;
}

function emitLinear(A: Rational, B: Rational, J: Interval, s: Rational, t: Rational, out: Interval[]): void {
  const dom: Interval = { lo: { value: s, closed: false }, hi: { value: t, closed: false } };
  const iv = intersect(linearPreimage(A, B, J), dom);
  if (!isEmpty(iv)) out.push(iv);
}

/** Effective linear form (ta*x + tb) of a term on an open sub-interval; mid is any interior point. */
function termLinearOn(t: Term, mid: Rational): { ta: Rational; tb: Rational } {
  if (isZero(t.a)) return { ta: ZERO, tb: evalTerm(t, mid) };
  if (t.rounding) return { ta: ZERO, tb: evalTerm(t, mid) }; // constant between rounding thresholds
  if (t.min !== undefined || t.max !== undefined) {
    const um = add(mul(t.a, mid), t.b);
    if (t.min !== undefined && cmp(um, t.min) <= 0) return { ta: ZERO, tb: mul(t.scale, t.min) };
    if (t.max !== undefined && cmp(um, t.max) >= 0) return { ta: ZERO, tb: mul(t.scale, t.max) };
    return { ta: mul(t.scale, t.a), tb: mul(t.scale, t.b) };
  }
  return { ta: mul(t.scale, t.a), tb: mul(t.scale, t.b) };
}

function roundingThresholdOffset(t: Term): Rational {
  const mode = t.rounding!.mode;
  if (mode === 'half-up' || mode === 'half-away-from-zero' || mode === 'half-even') {
    return make(t.rounding!.quantum.n, t.rounding!.quantum.d * 2n);
  }
  return ZERO;
}

/**
 * Exact solutions of S(x) ∈ J on the bounded open interval (u, v).
 * Enumerates every rounding threshold and clamp kink strictly inside, splits into
 * sub-intervals where S is linear, and evaluates boundary points exactly.
 */
function enumerateBounded(terms: readonly Term[], u: Rational, v: Rational, J: Interval, out: Interval[]): void {
  const bounds: Rational[] = [];
  for (const t of terms) {
    if (isZero(t.a)) continue;
    if (t.rounding) {
      const q = t.rounding.quantum;
      const t0 = roundingThresholdOffset(t);
      const iu = add(mul(t.a, u), t.b);
      const iv = add(mul(t.a, v), t.b);
      const loI = rmin(iu, iv);
      const hiI = rmax(iu, iv);
      const kmin = floorBig(div(sub(loI, t0), q)) + 1n;
      const kmax = ceilBig(div(sub(hiI, t0), q)) - 1n;
      if (kmax - kmin > MAX_ROUNDING_STEPS) {
        throw new Error('invert: too many rounding steps in domain; widen the quantum or bound the brackets');
      }
      for (let k = kmin; k <= kmax; k++) {
        const x = div(sub(add(mul(q, fromBigInt(k)), t0), t.b), t.a);
        if (cmp(x, u) > 0 && cmp(x, v) < 0) bounds.push(x);
      }
    } else if (t.min !== undefined || t.max !== undefined) {
      for (const c of [t.min, t.max]) {
        if (c === undefined) continue;
        const x = div(sub(c, t.b), t.a);
        if (cmp(x, u) > 0 && cmp(x, v) < 0) bounds.push(x);
      }
    }
  }
  bounds.sort(cmp);
  const xs: Rational[] = [];
  for (const b of bounds) {
    if (xs.length === 0 || !eq(xs[xs.length - 1], b)) xs.push(b);
  }
  // open sub-intervals: S is linear on each
  const edges = [u, ...xs, v];
  for (let i = 0; i + 1 < edges.length; i++) {
    const s = edges[i];
    const t2 = edges[i + 1];
    if (cmp(s, t2) >= 0) continue;
    const mid = div(add(s, t2), TWO);
    let A = ZERO;
    let B = ZERO;
    for (const t of terms) {
      const { ta, tb } = termLinearOn(t, mid);
      A = add(A, ta);
      B = add(B, tb);
    }
    emitLinear(A, B, J, s, t2, out);
  }
  // boundary points: exact evaluation
  for (const x of xs) {
    if (contains(J, evalTerms(terms, x))) out.push(pointInterval(x));
  }
}

interface Classified {
  La: Rational; // slope of the unbounded linear part
  Lb: Rational;
  err: Rational; // total bounded deviation |S - (La*x + Lb)|
  zones: Interval[]; // finite closed zones where clamped terms are non-constant
}

function zoneOf(t: Term): Interval {
  const q = t.rounding ? t.rounding.quantum : ZERO;
  const loI = sub(t.min!, q);
  const hiI = add(t.max!, q);
  const x1 = div(sub(loI, t.b), t.a);
  const x2 = div(sub(hiI, t.b), t.a);
  return cmp(x1, x2) <= 0
    ? { lo: { value: x1, closed: true }, hi: { value: x2, closed: true } }
    : { lo: { value: x2, closed: true }, hi: { value: x1, closed: true } };
}

/** x-range where a one-sided-clamped term is non-constant (a half-line). */
function oneSidedZone(t: Term, bound: Rational, isMax: boolean, q: Rational): Interval {
  const c = isMax ? add(bound, q) : sub(bound, q);
  const thr = div(sub(c, t.b), t.a);
  const aPos = cmp(t.a, ZERO) > 0;
  // max-only: non-constant where inner < c; min-only: where inner > c
  const below = isMax ? aPos : !aPos;
  return below
    ? { lo: { value: null, closed: false }, hi: { value: thr, closed: true } }
    : { lo: { value: thr, closed: true }, hi: { value: null, closed: false } };
}

function classify(piece: Piece): Classified {
  let La = ZERO;
  let Lb = ZERO;
  let err = ZERO;
  const zones: Interval[] = [];
  for (const t of piece.terms) {
    const sa = mul(t.scale, t.a);
    const sb = mul(t.scale, t.b);
    const hasMin = t.min !== undefined;
    const hasMax = t.max !== undefined;
    if (!t.rounding && !hasMin && !hasMax) {
      La = add(La, sa);
      Lb = add(Lb, sb);
    } else if (t.rounding && !hasMin && !hasMax) {
      La = add(La, sa);
      Lb = add(Lb, sb);
      err = add(err, mul(abs(sa), t.rounding.quantum));
    } else if (hasMin && hasMax) {
      if (!isZero(t.a)) zones.push(zoneOf(t));
      err = add(err, mul(abs(t.scale), rmax(abs(t.min!), abs(t.max!))));
    } else {
      // one-sided clamp: saturating on the unbounded side -> finite non-constant zone;
      // growing on the unbounded side -> absorbed into the linear part with bounded deviation
      if (isZero(t.a)) continue;
      const isMax = hasMax;
      const bound = (hasMax ? t.max : t.min)!;
      const q = t.rounding ? t.rounding.quantum : ZERO;
      const domainInterval: Interval = {
        lo: { value: piece.lo, closed: false },
        hi: { value: piece.hi, closed: false },
      };
      const z = intersect(oneSidedZone(t, bound, isMax, q), domainInterval);
      if (!isEmpty(z) && z.lo.value !== null && z.hi.value !== null) {
        zones.push(z);
        const u1 = add(mul(t.a, z.lo.value), t.b);
        const u2 = add(mul(t.a, z.hi.value), t.b);
        const m = rmax(rmax(abs(u1), abs(u2)), abs(bound));
        err = add(err, mul(abs(t.scale), add(m, q)));
      } else if (isEmpty(z)) {
        // never non-constant on this piece: contributes a bounded constant
        err = add(err, mul(abs(t.scale), abs(bound)));
      } else {
        const finiteEnd = piece.lo !== null ? piece.lo : piece.hi;
        if (finiteEnd === null) {
          throw new Error(
            'invert: one-sided bracket clamp with nonzero rate on the whole line is not supported; give the bracket both min and max or a finite bound',
          );
        }
        La = add(La, sa);
        Lb = add(Lb, sb);
        const uEnd = add(mul(t.a, finiteEnd), t.b);
        const dev = isMax ? sub(uEnd, bound) : sub(bound, uEnd);
        err = add(err, mul(abs(t.scale), rmax(rmax(q, dev), ZERO)));
      }
    }
  }
  return { La, Lb, err, zones };
}

function solveOnPiece(piece: Piece, J: Interval, out: Interval[]): void {
  const u = piece.lo;
  const v = piece.hi;
  if (u !== null && v !== null) {
    enumerateBounded(piece.terms, u, v, J, out);
    return;
  }
  const domain: Interval = { lo: { value: u, closed: false }, hi: { value: v, closed: false } };
  const cl = classify(piece);
  if (!isZero(cl.La)) {
    // guaranteed region: L(x) well inside J implies S(x) ∈ J
    const shrunk: Interval = {
      lo: J.lo.value === null ? { value: null, closed: false } : { value: add(J.lo.value, cl.err), closed: J.lo.closed },
      hi: J.hi.value === null ? { value: null, closed: false } : { value: sub(J.hi.value, cl.err), closed: J.hi.closed },
    };
    if (!isEmpty(shrunk)) {
      const sure = intersect(linearPreimage(cl.La, cl.Lb, shrunk), domain);
      if (!isEmpty(sure)) out.push(sure);
    }
    // boundary strips: L(x) within err of each finite J endpoint, expanded by 1 for exactness
    for (const side of ['lo', 'hi'] as const) {
      const edge = J[side].value;
      if (edge === null) continue;
      const strip: Interval = {
        lo: { value: sub(sub(edge, cl.err), ONE), closed: true },
        hi: { value: add(add(edge, cl.err), ONE), closed: true },
      };
      const region = intersect(linearPreimage(cl.La, cl.Lb, strip), domain);
      if (isEmpty(region) || region.lo.value === null || region.hi.value === null) continue;
      enumerateBounded(piece.terms, region.lo.value, region.hi.value, J, out);
    }
    return;
  }
  // La === 0: S is bounded on this piece
  if (cl.zones.length === 0) {
    const sample = u === null ? (v === null ? ZERO : sub(v, ONE)) : add(u, ONE);
    if (contains(J, evalTerms(piece.terms, sample))) out.push(domain);
    return;
  }
  let hullLo = cl.zones[0].lo.value!;
  let hullHi = cl.zones[0].hi.value!;
  for (const z of cl.zones) {
    hullLo = rmin(hullLo, z.lo.value!);
    hullHi = rmax(hullHi, z.hi.value!);
  }
  // left constant ray (u, hullLo]
  if (u === null || cmp(u, hullLo) < 0) {
    const sample = u === null ? sub(hullLo, ONE) : div(add(u, hullLo), TWO);
    if (contains(J, evalTerms(piece.terms, sample))) {
      out.push({ lo: { value: u, closed: false }, hi: { value: hullLo, closed: true } });
    }
  }
  // right constant ray [hullHi, v)
  if (v === null || cmp(hullHi, v) < 0) {
    const sample = v === null ? add(hullHi, ONE) : div(add(hullHi, v), TWO);
    if (contains(J, evalTerms(piece.terms, sample))) {
      out.push({ lo: { value: hullHi, closed: true }, hi: { value: v, closed: false } });
    }
  }
  const mu = u !== null && cmp(u, hullLo) > 0 ? u : hullLo;
  const mv = v !== null && cmp(v, hullHi) < 0 ? v : hullHi;
  if (cmp(mu, mv) < 0) enumerateBounded(piece.terms, mu, mv, J, out);
}

/** Preimage of interval I under clamp(·, gmin, gmax): I ∩ [gmin,gmax], plus rays when gmin/gmax ∈ I. */
function clampPreimage(I: Interval, gmin: Rational | undefined, gmax: Rational | undefined): Interval[] {
  const parts: Interval[] = [];
  const range: Interval = {
    lo: { value: gmin ?? null, closed: true },
    hi: { value: gmax ?? null, closed: true },
  };
  const core = intersect(I, range);
  if (!isEmpty(core)) parts.push(core);
  if (gmin !== undefined && contains(I, gmin)) {
    parts.push({ lo: { value: null, closed: false }, hi: { value: gmin, closed: true } });
  }
  if (gmax !== undefined && contains(I, gmax)) {
    parts.push({ lo: { value: gmax, closed: true }, hi: { value: null, closed: false } });
  }
  return mergeIntervals(parts);
}

/** All x with f(x) === y, as disjoint sorted intervals (platforms may yield several). */
export function invert(c: Compiled, y: Rational): Interval[] {
  const I = c.finalRounding ? preimage(y, c.finalRounding) : pointInterval(y);
  if (I === null) return [];
  const Js = clampPreimage(I, c.min, c.max);
  const found: Interval[] = [];
  for (const J of Js) {
    for (const piece of c.pieces) solveOnPiece(piece, J, found);
  }
  for (const bp of c.breakpoints) {
    if (eq(evaluate(c.spec, bp).output, y)) found.push(pointInterval(bp));
  }
  return mergeIntervals(found);
}
