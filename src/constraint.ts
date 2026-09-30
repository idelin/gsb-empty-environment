import { Rational } from './rational.ts';

/**
 * Linear constraint: sum(terms[var] * x[var])  SENSE  rhs.
 * All coefficients and rhs are exact rationals.
 * 'le'/'ge' are closed bounds, 'lt'/'gt' are open bounds.
 */
export type Sense = 'le' | 'lt' | 'ge' | 'gt';

export interface LinearConstraint {
  readonly id: string;
  readonly terms: ReadonlyMap<string, Rational>;
  readonly sense: Sense;
  readonly rhs: Rational;
}

export function constraint(
  id: string,
  terms: Readonly<Record<string, Rational | string | bigint>>,
  sense: Sense,
  rhs: Rational | string | bigint,
): LinearConstraint {
  const map = new Map<string, Rational>();
  for (const [k, v] of Object.entries(terms)) {
    const r = v instanceof Rational ? v : Rational.from(v as string | bigint);
    if (!r.isZero()) map.set(k, r);
  }
  return { id, terms: map, sense, rhs: rhs instanceof Rational ? rhs : Rational.from(rhs as string | bigint) };
}

/** Normalized row: coeffs <= rhs (strict when the original sense was open). */
export interface NormalizedRow {
  readonly constraintId: string;
  readonly coeffs: ReadonlyMap<string, Rational>;
  readonly rhs: Rational;
  readonly strict: boolean;
}

export function normalize(c: LinearConstraint): NormalizedRow {
  switch (c.sense) {
    case 'le':
      return { constraintId: c.id, coeffs: c.terms, rhs: c.rhs, strict: false };
    case 'lt':
      return { constraintId: c.id, coeffs: c.terms, rhs: c.rhs, strict: true };
    case 'ge':
    case 'gt': {
      const neg = new Map<string, Rational>();
      for (const [k, v] of c.terms) neg.set(k, v.neg());
      return { constraintId: c.id, coeffs: neg, rhs: c.rhs.neg(), strict: c.sense === 'gt' };
    }
  }
}

/** Exact check: does this assignment satisfy the constraint? */
export function satisfies(c: LinearConstraint, assignment: ReadonlyMap<string, Rational>): boolean {
  let lhs = Rational.zero();
  for (const [k, a] of c.terms) {
    const x = assignment.get(k);
    if (x === undefined) throw new Error(`satisfies: missing assignment for variable ${k}`);
    lhs = lhs.add(a.mul(x));
  }
  switch (c.sense) {
    case 'le':
      return lhs.le(c.rhs);
    case 'lt':
      return lhs.lt(c.rhs);
    case 'ge':
      return lhs.ge(c.rhs);
    case 'gt':
      return lhs.gt(c.rhs);
  }
}

export function evaluateLhs(c: LinearConstraint, assignment: ReadonlyMap<string, Rational>): Rational {
  let lhs = Rational.zero();
  for (const [k, a] of c.terms) {
    const x = assignment.get(k);
    if (x === undefined) throw new Error(`evaluateLhs: missing assignment for variable ${k}`);
    lhs = lhs.add(a.mul(x));
  }
  return lhs;
}
