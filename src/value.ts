/**
 * Affine uncertain values: an exact centre plus named error terms.
 *
 *   value = centre + Σ eᵢ,   eᵢ ∈ [-radiusᵢ, +radiusᵢ]
 *
 * Each error term carries a provenance token. Terms sharing a token
 * denote the SAME underlying error variable, not two independent ones:
 * when two compared values both contain token t, that component cancels
 * in the difference instead of doubling the uncertainty. This is what
 * keeps "shared source" from being misread as independent error.
 */

import {
  type Rational,
  ZERO,
  add,
  sub,
  abs,
  cmp,
  isZero,
  toString as rationalToString,
} from './rational.ts';

export interface ErrorTerm {
  readonly token: string;
  readonly radius: Rational; // >= 0
}

export interface AffineValue {
  readonly centre: Rational;
  readonly errors: readonly ErrorTerm[];
}

export function exactValue(centre: Rational): AffineValue {
  return { centre, errors: [] };
}

export function uncertainValue(centre: Rational, token: string, radius: Rational): AffineValue {
  if (cmp(radius, ZERO) < 0) throw new RangeError('error radius must be non-negative');
  return { centre, errors: [{ token, radius }] };
}

/** Tight interval [lo, hi] implied by the affine form. */
export function bounds(v: AffineValue): { lo: Rational; hi: Rational } {
  let total = ZERO;
  for (const term of v.errors) total = add(total, term.radius);
  return { lo: sub(v.centre, total), hi: add(v.centre, total) };
}

export function totalRadius(v: AffineValue): Rational {
  let total = ZERO;
  for (const term of v.errors) total = add(total, term.radius);
  return total;
}

export function isExact(v: AffineValue): boolean {
  return v.errors.every((t) => isZero(t.radius));
}

export type Outcome = 'lt' | 'eq' | 'gt';

export type CompareResult =
  | { readonly kind: 'decided'; readonly outcome: Outcome }
  | { readonly kind: 'undetermined'; readonly reason: string };

export function decided(outcome: Outcome): CompareResult {
  return { kind: 'decided', outcome };
}

export function undetermined(reason: string): CompareResult {
  return { kind: 'undetermined', reason };
}

/**
 * Three-way comparison of two affine values.
 *
 * Let d = a.centre - b.centre and let R be the sum of radii of the
 * error terms that are NOT shared between a and b (shared tokens
 * cancel; for a shared token with unequal radii only the smaller
 * part cancels, the excess stays as residual uncertainty).
 *
 *   d >  R  →  gt        d < -R  →  lt
 *   d = 0 and R = 0  →  eq (exact equality evidence)
 *   otherwise        →  undetermined (intervals still overlap)
 *
 * Overlapping intervals are never reported as equal.
 */
export function compareAffine(a: AffineValue, b: AffineValue): CompareResult {
  const d = sub(a.centre, b.centre);
  const residual = residualRadius(a.errors, b.errors);

  if (cmp(d, residual) > 0) return decided('gt');
  if (cmp(neg_(d), residual) > 0) return decided('lt');
  if (isZero(d) && isZero(residual)) return decided('eq');
  return undetermined(
    `intervals overlap: difference centre ${rationalToString(d)} within residual radius ${rationalToString(residual)}`,
  );
}

function neg_(r: Rational): Rational {
  return { num: -r.num, den: r.den };
}

/**
 * Sum of radii that remain uncertain in a - b after cancelling
 * error terms whose tokens appear on both sides.
 */
export function residualRadius(
  aErrors: readonly ErrorTerm[],
  bErrors: readonly ErrorTerm[],
): Rational {
  const aMap = mergeByToken(aErrors);
  const bMap = mergeByToken(bErrors);
  const tokens = new Set<string>([...aMap.keys(), ...bMap.keys()]);
  let residual = ZERO;
  for (const token of tokens) {
    const ra = aMap.get(token);
    const rb = bMap.get(token);
    if (ra !== undefined && rb !== undefined) {
      // shared source: only the excess over the common part remains
      residual = add(residual, abs(sub(ra, rb)));
    } else {
      residual = add(residual, (ra ?? rb) as Rational);
    }
  }
  return residual;
}

function mergeByToken(terms: readonly ErrorTerm[]): Map<string, Rational> {
  const map = new Map<string, Rational>();
  for (const t of terms) map.set(t.token, add(map.get(t.token) ?? ZERO, t.radius));
  return map;
}

export function describe(v: AffineValue): string {
  const { lo, hi } = bounds(v);
  if (isExact(v)) return rationalToString(v.centre);
  return `${rationalToString(v.centre)} ∈ [${rationalToString(lo)}, ${rationalToString(hi)}]`;
}
