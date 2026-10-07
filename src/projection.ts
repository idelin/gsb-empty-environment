/**
 * Weighted Euclidean projection of edited cubic Bezier control points back
 * onto the constraint manifold of a curve chain:
 *
 *   - endpoint constraints: the chain's first and last points stay pinned
 *     (optional, on by default), plus any user-fixed control points;
 *   - joint constraints between consecutive segments i and i+1:
 *       coincidence:  D_i - A_{i+1}           = 0
 *       C1 (equal first derivatives):
 *                     -C_i + D_i + A_{i+1} - B_{i+1} = 0
 *     where a segment's control points are (A, B, C, D) = (P0, P1, P2, P3)
 *     and the derivative of a cubic at its ends is 3*(B-A) / 3*(D-C).
 *
 * Only points marked `free` may move. The solver minimizes
 *     sum_free  w_p * ||P_p - P~_p||^2
 * with non-negative rational weights, using exact rational arithmetic.
 *
 * Outcomes:
 *   - 'infeasible'  : the linear constraints (including fixed-point rows)
 *                     contradict each other; a Farkas certificate is returned.
 *   - 'unique'      : the unique minimizer, verified by back-substitution
 *                     into the constraints and by the KKT gradient condition.
 *   - 'non-unique'  : zero weights leave some constraint-nullspace directions
 *                     cost-free; one minimizer plus the degenerate directions
 *                     are returned.
 */

import { Rational, type RationalLike } from './rational.ts';
import {
  dot,
  matVecMul,
  maxAbs,
  solveLinearSystem,
  transpose,
} from './matrix.ts';

export interface Vec2Input {
  x: RationalLike;
  y: RationalLike;
}

export interface Vec2 {
  x: Rational;
  y: Rational;
}

export interface SegmentInput {
  /** Control points [P0, P1, P2, P3] at their edited (target) positions. */
  points: [Vec2Input, Vec2Input, Vec2Input, Vec2Input];
}

export interface ProjectionInput {
  /** 1..10 cubic segments forming a chain. */
  segments: SegmentInput[];
  /** weights[s][p]: non-negative rational weight of point p of segment s. */
  weights: RationalLike[][];
  /** free[s][p]: true if the point may move; fixed points stay at input. */
  free: boolean[][];
  /** Pin the chain's first P0 and last P3 at their input positions.
   *  Default true. */
  pinEndpoints?: boolean;
}

export interface ConstraintTerm {
  segment: number;
  point: number;
  coefficient: Rational;
}

export interface ContradictionCertificate {
  /** Coordinate axis in which the contradiction was found. */
  axis: 'x' | 'y';
  /** Nonzero combination of constraint rows: sum coeff_i * row_i = 0 on the
   *  left-hand side, but sum coeff_i * rhs_i = contradictionValue != 0. */
  combination: { row: number; label: string; coefficient: Rational }[];
  contradictionValue: Rational;
  /** Self-check: combination . M = 0 and combination . c = value. */
  verified: boolean;
}

export interface NullDirection {
  /** direction[segment][point]: displacement of that control point (same
   *  shape for both axes; x and y may be displaced independently). */
  vector: Vec2[][];
  /** Points with nonzero displacement. */
  support: { segment: number; point: number }[];
}

export interface Verification {
  /** All constraint residuals M x* - c are exactly zero (both axes). */
  constraintsSatisfied: boolean;
  maxConstraintResidual: Rational;
  /** Objective gradient lies in the row space of M (KKT stationarity):
   *  checked as orthogonality of the gradient to null(M). */
  gradientOrthogonalToNullspace: boolean;
  maxGradientComponent: Rational;
  /** Lagrange multipliers lambda with M^T lambda = -gradient (per axis,
   *  one entry per constraint row, in row order). */
  lagrangeMultipliers: { x: Rational[]; y: Rational[] };
  maxLagrangeResidual: Rational;
  /** Exact optimal value of the weighted squared-distance objective. */
  objectiveValue: Rational;
}

export type ProjectionResult =
  | { status: 'infeasible'; certificate: ContradictionCertificate }
  | { status: 'unique'; points: Vec2[][]; verification: Verification }
  | {
      status: 'non-unique';
      points: Vec2[][];
      nullDirections: NullDirection[];
      verification: Verification;
    };

const POINT_NAMES = ['P0', 'P1', 'P2', 'P3'];

interface Row {
  /** Sparse coefficients: variable index (segment*4 + point) -> coefficient. */
  terms: [number, Rational][];
  label: string;
}

function pointLabel(s: number, p: number): string {
  return `seg${s}.${POINT_NAMES[p]}`;
}

export function projectBezier(input: ProjectionInput): ProjectionResult {
  const { segments } = input;
  const nSeg = segments.length;
  if (nSeg < 1 || nSeg > 10) {
    throw new Error(`projectBezier: expected 1..10 segments, got ${nSeg}`);
  }
  const pinEndpoints = input.pinEndpoints ?? true;
  if (input.weights.length !== nSeg || input.free.length !== nSeg) {
    throw new Error('projectBezier: weights/free must match segments');
  }

  const nVar = nSeg * 4;
  const target: Vec2[] = [];
  const weight: Rational[] = [];
  const isFree: boolean[] = [];
  for (let s = 0; s < nSeg; s++) {
    const pts = segments[s].points;
    if (input.weights[s].length !== 4 || input.free[s].length !== 4) {
      throw new Error(`projectBezier: segment ${s} needs 4 weights/free flags`);
    }
    for (let p = 0; p < 4; p++) {
      target.push({ x: Rational.from(pts[p].x), y: Rational.from(pts[p].y) });
      const w = Rational.from(input.weights[s][p]);
      if (w.sign() < 0) {
        throw new Error(`projectBezier: negative weight at ${pointLabel(s, p)}`);
      }
      weight.push(w);
      isFree.push(input.free[s][p]);
    }
  }

  // ---- Constraint rows (identical coefficient structure for x and y). ----
  const rows: Row[] = [];
  const rhsFixed: (Vec2 | null)[] = []; // rhs per row for fixed-point rows

  const v = (s: number, p: number) => s * 4 + p;

  for (let j = 0; j < nSeg - 1; j++) {
    rows.push({
      terms: [
        [v(j, 3), Rational.ONE],
        [v(j + 1, 0), Rational.ONE.neg()],
      ],
      label: `joint[${j}].coincidence(${pointLabel(j, 3)}=${pointLabel(j + 1, 0)})`,
    });
    rhsFixed.push(null);
    rows.push({
      terms: [
        [v(j, 2), Rational.ONE.neg()],
        [v(j, 3), Rational.ONE],
        [v(j + 1, 0), Rational.ONE],
        [v(j + 1, 1), Rational.ONE.neg()],
      ],
      label: `joint[${j}].c1(equal-derivative)`,
    });
    rhsFixed.push(null);
  }

  const fixedAt = (s: number, p: number, label: string) => {
    rows.push({ terms: [[v(s, p), Rational.ONE]], label });
    rhsFixed.push(target[v(s, p)]);
  };

  for (let s = 0; s < nSeg; s++) {
    for (let p = 0; p < 4; p++) {
      if (!isFree[v(s, p)]) fixedAt(s, p, `fixed(${pointLabel(s, p)})`);
    }
  }
  if (pinEndpoints) {
    if (isFree[v(0, 0)]) fixedAt(0, 0, 'pinned-endpoint(start)');
    if (isFree[v(nSeg - 1, 3)]) fixedAt(nSeg - 1, 3, 'pinned-endpoint(end)');
  }

  const nRows = rows.length;
  const M: Rational[][] = rows.map((row) => {
    const dense = new Array<Rational>(nVar).fill(Rational.ZERO);
    for (const [idx, coef] of row.terms) dense[idx] = coef;
    return dense;
  });
  const labels = rows.map((r) => r.label);

  // ---- Solve per coordinate axis. ----
  interface AxisSolution {
    x: Rational[];
    gradient: Rational[];
    lambda: Rational[];
    maxResidual: Rational;
    maxLagrangeResidual: Rational;
    gradientOrthogonal: boolean;
    maxGradientComponent: Rational;
    objective: Rational;
  }

  let nullDirections: NullDirection[] | null = null;

  const solveAxis = (axis: 'x' | 'y'): AxisSolution | ContradictionCertificate => {
    const c = rows.map((_, i) => {
      const fixed = rhsFixed[i];
      return fixed === null ? Rational.ZERO : fixed[axis];
    });
    const xTilde = target.map((t) => t[axis]);

    const solved = solveLinearSystem(M, c);
    if (!solved.consistent) {
      const combination = solved.certificate
        .map((coefficient, row) => ({ row, label: labels[row], coefficient }))
        .filter((e) => !e.coefficient.isZero());
      // Self-check the certificate: y.M = 0, y.c = value.
      const yM = new Array<Rational>(nVar).fill(Rational.ZERO);
      let yc = Rational.ZERO;
      for (const e of combination) {
        for (let j = 0; j < nVar; j++) {
          if (!M[e.row][j].isZero()) {
            yM[j] = yM[j].add(e.coefficient.mul(M[e.row][j]));
          }
        }
        yc = yc.add(e.coefficient.mul(c[e.row]));
      }
      const verified =
        yM.every((x) => x.isZero()) && yc.equals(solved.contradictionValue);
      return {
        axis,
        combination,
        contradictionValue: solved.contradictionValue,
        verified,
      };
    }

    const { particular: x0, nullspace: N } = solved;
    const k = N.length;

    // Weighted objective only over free points (fixed ones are pinned by rows).
    const w = weight.map((wi, i) => (isFree[i] ? wi : Rational.ZERO));

    let xStar: Rational[];
    if (k === 0) {
      xStar = x0;
    } else {
      // Minimize ||W^{1/2}(x0 + N z - x~)||^2 over z:
      // normal equations (N^T W N) z = N^T W (x~ - x0).
      const wn = N.map((col) => col.map((x, i) => w[i].mul(x)));
      const G: Rational[][] = Array.from({ length: k }, (_, a) =>
        Array.from({ length: k }, (_, b) => dot(N[a], wn[b])),
      );
      const r: Rational[] = N.map((col) => {
        let acc = Rational.ZERO;
        for (let i = 0; i < nVar; i++) {
          const d = xTilde[i].sub(x0[i]);
          if (!d.isZero()) acc = acc.add(col[i].mul(w[i]).mul(d));
        }
        return acc;
      });
      const normal = solveLinearSystem(G, r);
      if (!normal.consistent) {
        // Normal equations are always consistent; guard anyway.
        throw new Error('projectBezier: inconsistent normal equations');
      }
      if (normal.nullspace.length > 0 && nullDirections === null) {
        // Zero-weight directions inside null(M): cost-free displacements.
        nullDirections = normal.nullspace.map((zDir) => {
          const dir = new Array<Rational>(nVar).fill(Rational.ZERO);
          for (let a = 0; a < k; a++) {
            if (zDir[a].isZero()) continue;
            for (let i = 0; i < nVar; i++) {
              if (!N[a][i].isZero()) dir[i] = dir[i].add(zDir[a].mul(N[a][i]));
            }
          }
          return dir;
        }).filter((d) => d.some((x) => !x.isZero()))
          .map((d) => toNullDirection(d, nSeg));
      }
      xStar = x0.slice();
      for (let a = 0; a < k; a++) {
        const za = normal.particular[a];
        if (za.isZero()) continue;
        for (let i = 0; i < nVar; i++) {
          if (!N[a][i].isZero()) xStar[i] = xStar[i].add(za.mul(N[a][i]));
        }
      }
    }

    // ---- Verification 1: back-substitution into the constraints. ----
    const residual = matVecMul(M, xStar).map((val, i) => val.sub(c[i]));
    const maxResidual = maxAbs(residual);

    // ---- Verification 2: KKT stationarity of the gradient. ----
    // gradient g = 2 W (x* - x~); must be orthogonal to null(M).
    const gradient = xStar.map((x, i) =>
      w[i].mul(x.sub(xTilde[i])).mul(Rational.of(2n)),
    );
    let maxGradientComponent = Rational.ZERO;
    let gradientOrthogonal = true;
    for (const col of N) {
      const comp = dot(col, gradient);
      maxGradientComponent = Rational.max(maxGradientComponent, comp.abs());
      if (!comp.isZero()) gradientOrthogonal = false;
    }

    // Lagrange multipliers: solve M^T lambda = -g (overdetermined, consistent
    // iff the KKT condition holds).
    const MT = transpose(M);
    const negG = gradient.map((g) => g.neg());
    const lamSolve = solveLinearSystem(MT, negG);
    let lambda: Rational[] = new Array<Rational>(nRows).fill(Rational.ZERO);
    let maxLagrangeResidual = Rational.ZERO;
    if (lamSolve.consistent) {
      lambda = lamSolve.particular;
      const lagResidual = matVecMul(MT, lambda).map((val, i) =>
        val.add(gradient[i]),
      );
      maxLagrangeResidual = maxAbs(lagResidual);
    } else {
      maxLagrangeResidual = Rational.ONE; // should not happen
    }

    let objective = Rational.ZERO;
    for (let i = 0; i < nVar; i++) {
      const d = xStar[i].sub(xTilde[i]);
      objective = objective.add(w[i].mul(d).mul(d));
    }

    return {
      x: xStar,
      gradient,
      lambda,
      maxResidual,
      maxLagrangeResidual,
      gradientOrthogonal,
      maxGradientComponent,
      objective,
    };
  };

  const sx = solveAxis('x');
  if (!('x' in sx)) return { status: 'infeasible', certificate: sx };
  const sy = solveAxis('y');
  if (!('x' in sy)) return { status: 'infeasible', certificate: sy };

  const points: Vec2[][] = [];
  for (let s = 0; s < nSeg; s++) {
    const seg: Vec2[] = [];
    for (let p = 0; p < 4; p++) {
      const i = v(s, p);
      seg.push({ x: sx.x[i], y: sy.x[i] });
    }
    points.push(seg);
  }

  const verification: Verification = {
    constraintsSatisfied: sx.maxResidual.isZero() && sy.maxResidual.isZero(),
    maxConstraintResidual: Rational.max(sx.maxResidual, sy.maxResidual),
    gradientOrthogonalToNullspace:
      sx.gradientOrthogonal && sy.gradientOrthogonal,
    maxGradientComponent: Rational.max(
      sx.maxGradientComponent,
      sy.maxGradientComponent,
    ),
    lagrangeMultipliers: { x: sx.lambda, y: sy.lambda },
    maxLagrangeResidual: Rational.max(
      sx.maxLagrangeResidual,
      sy.maxLagrangeResidual,
    ),
    objectiveValue: sx.objective.add(sy.objective),
  };

  if (nullDirections !== null && nullDirections.length > 0) {
    return { status: 'non-unique', points, nullDirections, verification };
  }
  return { status: 'unique', points, verification };
}

function toNullDirection(dir: Rational[], nSeg: number): NullDirection {
  const vector: Vec2[][] = [];
  const support: { segment: number; point: number }[] = [];
  for (let s = 0; s < nSeg; s++) {
    const seg: Vec2[] = [];
    for (let p = 0; p < 4; p++) {
      const d = dir[s * 4 + p];
      seg.push({ x: d, y: d });
      if (!d.isZero()) support.push({ segment: s, point: p });
    }
    vector.push(seg);
  }
  return { vector, support };
}
