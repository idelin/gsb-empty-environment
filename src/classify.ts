import { Rational } from './rational.ts';
import type { ExtVal } from './extval.ts';
import { finite, addExt, mulExt, extToString } from './extval.ts';
import type { Box } from './measurement.ts';
import type { LinearConstraint } from './constraint.ts';
import { normalize } from './constraint.ts';

export type SingleStatus = 'pass' | 'fail' | 'review';

export type Assignment = ReadonlyMap<string, Rational>;

export interface SingleVerdict {
  readonly constraintId: string;
  readonly status: SingleStatus;
  /** Exact min/max of the LHS over the box (may be +/-inf with unknown error). */
  readonly min: ExtVal;
  readonly max: ExtVal;
  /** Corner assignments attaining min/max; null when an unbounded variable is involved. */
  readonly minCorner: Assignment | null;
  readonly maxCorner: Assignment | null;
}

function cornerPick(lo: ExtVal, hi: ExtVal): Rational | null {
  if (lo.kind === 'finite') return lo.v;
  if (hi.kind === 'finite') return hi.v;
  return null;
}

/**
 * Classify one linear constraint over the box of measurement intervals.
 *
 * - pass:   worst case still satisfies (closed/open sense respected exactly)
 * - fail:   best case already violates
 * - review: the error interval straddles the threshold — never decided by
 *           the center value alone.
 */
export function classifyConstraint(c: LinearConstraint, box: Box): SingleVerdict {
  const row = normalize(c);
  let min: ExtVal = finite(Rational.zero());
  let max: ExtVal = finite(Rational.zero());
  const minCorner = new Map<string, Rational>();
  const maxCorner = new Map<string, Rational>();
  let minCornerOk = true;
  let maxCornerOk = true;

  for (const [varId, coeff] of row.coeffs) {
    const iv = box.get(varId);
    if (iv === undefined) throw new Error(`classify: no measurement/box entry for variable ${varId}`);
    const loTerm = mulExt(coeff, iv.lo);
    const hiTerm = mulExt(coeff, iv.hi);
    // coeff * x ranges between the two endpoint products.
    const loEnd = coeff.sign() >= 0 ? loTerm : hiTerm;
    const hiEnd = coeff.sign() >= 0 ? hiTerm : loTerm;
    min = addExt(min, loEnd);
    max = addExt(max, hiEnd);

    const loPick = cornerPick(iv.lo, iv.hi);
    const hiPick = cornerPick(iv.hi, iv.lo);
    if (loPick === null || hiPick === null) {
      minCornerOk = false;
      maxCornerOk = false;
    } else {
      minCorner.set(varId, coeff.sign() >= 0 ? loPick : hiPick);
      maxCorner.set(varId, coeff.sign() >= 0 ? hiPick : loPick);
    }
  }

  const cmpMax = compareWithRhs(max, row.rhs);
  const cmpMin = compareWithRhs(min, row.rhs);

  let status: SingleStatus;
  if (!row.strict) {
    // lhs <= rhs (closed)
    if (cmpMax !== null && cmpMax <= 0) status = 'pass';
    else if (cmpMin !== null && cmpMin > 0) status = 'fail';
    else status = 'review';
  } else {
    // lhs < rhs (open)
    if (cmpMax !== null && cmpMax < 0) status = 'pass';
    else if (cmpMin !== null && cmpMin >= 0) status = 'fail';
    else status = 'review';
  }

  return {
    constraintId: c.id,
    status,
    min,
    max,
    minCorner: minCornerOk ? minCorner : null,
    maxCorner: maxCornerOk ? maxCorner : null,
  };
}

/** cmp of ExtVal against a finite rhs; null when infinite. */
function compareWithRhs(x: ExtVal, rhs: Rational): number | null {
  if (x.kind === 'inf') return null;
  return x.v.cmp(rhs);
}

export function describeVerdict(v: SingleVerdict): string {
  return `${v.constraintId}: ${v.status} (lhs in [${extToString(v.min)}, ${extToString(v.max)}])`;
}
