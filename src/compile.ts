import type { Rational } from './rational.ts';
import { add, cmp, eq, mul, sub, ZERO } from './rational.ts';
import type { RoundingSpec } from './rounding.ts';
import { maybeRound, roundTo } from './rounding.ts';
import type { CompositeSpec } from './rules.ts';
import { fixedOf, rateOf, validateComposite } from './rules.ts';
import { evaluate } from './evaluate.ts';

/**
 * One additive term of the compiled core function on an atomic open interval:
 *   term(x) = scale * clamp( maybeRound(a*x + b), min, max )
 * Rounding (per segment) and clamping (per bracket) stay attached to the term,
 * so they are never reordered with the final rounding of the composite.
 */
export interface Term {
  readonly a: Rational;
  readonly b: Rational;
  readonly rounding?: RoundingSpec;
  readonly min?: Rational;
  readonly max?: Rational;
  readonly scale: Rational;
}

/** Atomic piece: the open interval (lo, hi); lo/hi === null means unbounded. */
export interface Piece {
  readonly lo: Rational | null;
  readonly hi: Rational | null;
  readonly terms: Term[];
}

export interface Compiled {
  readonly spec: CompositeSpec;
  readonly breakpoints: Rational[]; // sorted; values at breakpoints come from direct evaluation
  readonly pieces: Piece[]; // open intervals between consecutive breakpoints, covering the line
  readonly min?: Rational;
  readonly max?: Rational;
  readonly finalRounding?: RoundingSpec;
}

export function evalTerm(t: Term, x: Rational): Rational {
  let v = add(mul(t.a, x), t.b);
  if (t.rounding) v = roundTo(v, t.rounding);
  if (t.min !== undefined && cmp(v, t.min) < 0) v = t.min;
  if (t.max !== undefined && cmp(v, t.max) > 0) v = t.max;
  return mul(t.scale, v);
}

function buildTerms(spec: CompositeSpec, lo: Rational | null, hi: Rational | null): Term[] {
  const terms: Term[] = [];
  for (const rule of spec.rules) {
    const scale = rule.discount ?? { n: 1n, d: 1n };
    for (const b of rule.brackets) {
      const l = b.lower.value;
      const u = b.upper.value;
      const rate = rateOf(b);
      const fixed = fixedOf(b);
      let a: Rational | null = null;
      let c: Rational = ZERO;
      if (rule.mode === 'whole') {
        const coversLo = l === null || (lo !== null && cmp(lo, l) >= 0);
        const coversHi = u === null || (hi !== null && cmp(hi, u) <= 0);
        if (coversLo && coversHi) {
          a = rate;
          c = fixed;
        }
      } else {
        if (l !== null && hi !== null && cmp(hi, l) <= 0) {
          // piece entirely at or below the bracket start: no overlap
        } else if (u !== null && lo !== null && cmp(lo, u) >= 0) {
          // piece entirely past the bracket: saturated constant
          a = ZERO;
          c = l === null ? fixed : add(fixed, mul(rate, sub(u, l)));
        } else if (l === null) {
          a = ZERO; // validated: rate is 0 for this bracket
          c = fixed;
        } else {
          a = rate;
          c = sub(fixed, mul(rate, l));
        }
      }
      if (a !== null) {
        terms.push({ a, b: c, rounding: rule.segmentRounding, min: b.min, max: b.max, scale });
      }
    }
  }
  return terms;
}

/** Compiles the composite into an exact piecewise representation. */
export function compile(spec: CompositeSpec): Compiled {
  validateComposite(spec);
  const bps: Rational[] = [];
  for (const rule of spec.rules) {
    for (const b of rule.brackets) {
      for (const v of [b.lower.value, b.upper.value]) {
        if (v !== null && !bps.some((p) => eq(p, v))) bps.push(v);
      }
    }
  }
  bps.sort(cmp);
  const pieces: Piece[] = [];
  const edges: (Rational | null)[] = [null, ...bps, null];
  for (let i = 0; i + 1 < edges.length; i++) {
    const lo = edges[i];
    const hi = edges[i + 1];
    pieces.push({ lo, hi, terms: buildTerms(spec, lo, hi) });
  }
  return { spec, breakpoints: bps, pieces, min: spec.min, max: spec.max, finalRounding: spec.finalRounding };
}

function clampTotal(v: Rational, lo: Rational | undefined, hi: Rational | undefined): Rational {
  if (lo !== undefined && cmp(v, lo) < 0) return lo;
  if (hi !== undefined && cmp(v, hi) > 0) return hi;
  return v;
}

/** Core value S(x): sum of terms, before global min/max and final rounding. */
export function evalCore(c: Compiled, x: Rational): Rational {
  for (const bp of c.breakpoints) {
    if (cmp(x, bp) === 0) return evaluate(c.spec, x).sum;
  }
  const piece = c.pieces.find(
    (p) => (p.lo === null || cmp(x, p.lo) > 0) && (p.hi === null || cmp(x, p.hi) < 0),
  );
  if (!piece) throw new Error('compile: no piece contains x (internal error)');
  let s = ZERO;
  for (const t of piece.terms) s = add(s, evalTerm(t, x));
  return s;
}

/** Full compiled evaluation: S(x), then global clamp, then final rounding. Must equal evaluate().output. */
export function evalCompiled(c: Compiled, x: Rational): Rational {
  for (const bp of c.breakpoints) {
    if (cmp(x, bp) === 0) return evaluate(c.spec, x).output;
  }
  const core = evalCore(c, x);
  return maybeRound(clampTotal(core, c.min, c.max), c.finalRounding);
}
