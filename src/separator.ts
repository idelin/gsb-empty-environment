import { Fraction, dot } from './fraction.ts';
import type { RationalInput } from './fraction.ts';
import { solveLP } from './lp.ts';
import type { LPConstraint } from './lp.ts';
import { formatHyperplane } from './format.ts';

export type InputPoint = RationalInput[];

export interface Separated {
  separable: true;
  /** Exact rational normal vector a of the separating hyperplane. */
  normal: Fraction[];
  /** Offset b with a·p < b for all p in P and a·q > b for all q in Q. */
  offset: Fraction;
  /** Human-readable equation, e.g. "2x - 3y + 5 = 0". */
  hyperplane: string;
}

export interface Intersecting {
  separable: false;
  /** A rational point lying in both convex hulls (conv(P) ∩ conv(Q)). */
  point: Fraction[];
  /** Barycentric coordinates of `point` w.r.t. P (convex combination). */
  coeffsP: Fraction[];
  /** Barycentric coordinates of `point` w.r.t. Q (convex combination). */
  coeffsQ: Fraction[];
}

export type SeparationResult = Separated | Intersecting;

function toFractionPoints(points: InputPoint[], label: string): Fraction[][] {
  return points.map((p, i) => {
    if (p.length === 0) throw new Error(`${label}[${i}]: points must have dimension >= 1`);
    return p.map((v) => Fraction.from(v));
  });
}

function checkDimension(points: Fraction[][], dim: number, label: string): void {
  points.forEach((p, i) => {
    if (p.length !== dim) {
      throw new Error(`${label}[${i}]: expected dimension ${dim}, got ${p.length}`);
    }
  });
}

function convexCombination(points: Fraction[][], coeffs: Fraction[]): Fraction[] {
  const dim = points[0].length;
  const acc: Fraction[] = new Array(dim).fill(Fraction.ZERO);
  for (let i = 0; i < points.length; i++) {
    for (let k = 0; k < dim; k++) acc[k] = acc[k].add(coeffs[i].mul(points[i][k]));
  }
  return acc;
}

/**
 * Decide strict linear separability of two finite point sets P, Q in R^d.
 *
 * Separable  -> returns an exact rational hyperplane a·x = b with
 *               a·p < b for all p in P and a·q > b for all q in Q.
 *               Found as a feasibility LP  a·p <= -1, a·q >= 1  (any strict
 *               separator can be rescaled to unit margin), solved with the
 *               exact rational simplex method.
 *
 * Inseparable -> by Farkas' lemma the convex hulls intersect; a second LP
 *               finds lambda, mu >= 0, sum lambda = sum mu = 1 with
 *               sum lambda_i p_i = sum mu_j q_j, yielding an exact rational
 *               common point together with its barycentric coordinates.
 */
export function separate(P: InputPoint[], Q: InputPoint[]): SeparationResult {
  const ptsP = toFractionPoints(P, 'P');
  const ptsQ = toFractionPoints(Q, 'Q');
  const dim = ptsP.length > 0 ? ptsP[0].length : ptsQ.length > 0 ? ptsQ[0].length : 0;
  if (dim === 0) throw new Error('separate: at least one non-empty point set is required');
  checkDimension(ptsP, dim, 'P');
  checkDimension(ptsQ, dim, 'Q');

  // Degenerate: an empty set is vacuously separable.
  if (ptsP.length === 0 || ptsQ.length === 0) {
    const normal = [Fraction.ONE, ...new Array(dim - 1).fill(Fraction.ZERO)];
    let offset = Fraction.ZERO;
    if (ptsP.length > 0) offset = maxDot(ptsP, normal).add(Fraction.ONE);
    if (ptsQ.length > 0) offset = minDot(ptsQ, normal).sub(Fraction.ONE);
    return {
      separable: true,
      normal,
      offset,
      hyperplane: formatHyperplane(normal, offset.neg()),
    };
  }

  // --- Separation LP: find a, t with a·p - t <= -1 (p in P) and
  // a·q - t >= 1 (q in Q); any strict separator a·x = t can be rescaled to
  // this unit-margin form. Free variables a_k, t are split into
  // non-negative parts: [a+, t+] and [a-, t-].
  const numFree = dim + 1;
  const sepConstraints: LPConstraint[] = [];
  for (const p of ptsP) {
    sepConstraints.push({
      coeffs: [...p, Fraction.ONE.neg(), ...p.map((c) => c.neg()), Fraction.ONE],
      sense: '<=',
      rhs: Fraction.ONE.neg(),
    });
  }
  for (const q of ptsQ) {
    sepConstraints.push({
      coeffs: [...q, Fraction.ONE.neg(), ...q.map((c) => c.neg()), Fraction.ONE],
      sense: '>=',
      rhs: Fraction.ONE,
    });
  }
  const zeroObj = new Array(2 * numFree).fill(Fraction.ZERO);
  const sep = solveLP(2 * numFree, zeroObj, sepConstraints, false);

  if (sep.status === 'optimal') {
    const normal: Fraction[] = [];
    for (let k = 0; k < dim; k++) normal.push(sep.x[k].sub(sep.x[numFree + k]));
    const lo = maxDot(ptsP, normal);
    const hi = minDot(ptsQ, normal);
    const offset = lo.add(hi).div(new Fraction(2n, 1n));
    return {
      separable: true,
      normal,
      offset,
      hyperplane: formatHyperplane(normal, offset.neg()),
    };
  }

  // --- Intersection LP: find a common point of conv(P) and conv(Q). ---
  const nP = ptsP.length;
  const nQ = ptsQ.length;
  const intConstraints: LPConstraint[] = [];
  for (let k = 0; k < dim; k++) {
    intConstraints.push({
      coeffs: [...ptsP.map((p) => p[k]), ...ptsQ.map((q) => q[k].neg())],
      sense: '=',
      rhs: Fraction.ZERO,
    });
  }
  intConstraints.push({
    coeffs: [...new Array(nP).fill(Fraction.ONE), ...new Array(nQ).fill(Fraction.ZERO)],
    sense: '=',
    rhs: Fraction.ONE,
  });
  intConstraints.push({
    coeffs: [...new Array(nP).fill(Fraction.ZERO), ...new Array(nQ).fill(Fraction.ONE)],
    sense: '=',
    rhs: Fraction.ONE,
  });
  const inter = solveLP(nP + nQ, new Array(nP + nQ).fill(Fraction.ZERO), intConstraints, false);
  if (inter.status !== 'optimal') {
    // Cannot happen for finite sets: Farkas' lemma gives exactly one of the two.
    throw new Error('separate: internal error — neither separable nor intersecting');
  }
  const coeffsP = inter.x.slice(0, nP);
  const coeffsQ = inter.x.slice(nP);
  const point = convexCombination(ptsP, coeffsP);
  return { separable: false, point, coeffsP, coeffsQ };
}

function maxDot(points: Fraction[][], normal: Fraction[]): Fraction {
  let best = dot(points[0], normal);
  for (const p of points) {
    const v = dot(p, normal);
    if (v.cmp(best) > 0) best = v;
  }
  return best;
}

function minDot(points: Fraction[][], normal: Fraction[]): Fraction {
  let best = dot(points[0], normal);
  for (const p of points) {
    const v = dot(p, normal);
    if (v.cmp(best) < 0) best = v;
  }
  return best;
}

/** Check that a·p < offset for all p in P and a·q > offset for all q in Q. */
export function verifySeparation(
  P: InputPoint[],
  Q: InputPoint[],
  normal: Fraction[],
  offset: Fraction,
): boolean {
  const ptsP = toFractionPoints(P, 'P');
  const ptsQ = toFractionPoints(Q, 'Q');
  for (const p of ptsP) if (dot(p, normal).cmp(offset) >= 0) return false;
  for (const q of ptsQ) if (dot(q, normal).cmp(offset) <= 0) return false;
  return true;
}

/**
 * Check that `point` is a genuine common point: coefficients are
 * non-negative, each family sums to 1, and both convex combinations
 * equal `point` exactly.
 */
export function verifyCommonPoint(
  P: InputPoint[],
  Q: InputPoint[],
  point: Fraction[],
  coeffsP: Fraction[],
  coeffsQ: Fraction[],
): boolean {
  const ptsP = toFractionPoints(P, 'P');
  const ptsQ = toFractionPoints(Q, 'Q');
  if (coeffsP.length !== ptsP.length || coeffsQ.length !== ptsQ.length) return false;
  if (coeffsP.some((c) => c.sign() < 0) || coeffsQ.some((c) => c.sign() < 0)) return false;
  const sum = (cs: Fraction[]) => cs.reduce((a, c) => a.add(c), Fraction.ZERO);
  if (!sum(coeffsP).equals(Fraction.ONE) || !sum(coeffsQ).equals(Fraction.ONE)) return false;
  const fromP = convexCombination(ptsP, coeffsP);
  const fromQ = convexCombination(ptsQ, coeffsQ);
  for (let k = 0; k < point.length; k++) {
    if (!fromP[k].equals(point[k]) || !fromQ[k].equals(point[k])) return false;
  }
  return true;
}
