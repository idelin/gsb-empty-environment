/**
 * Projection of edited cubic Bézier control polygons back onto the constraint
 * manifold of a vector editor:
 *
 *   - the two curve endpoints stay pinned ("端点固定"),
 *   - at every interior joint the segments meet (C0) and share the same first
 *     derivative (C1):  P_i,3 = P_i+1,0  and
 *     P_i,3 - P_i,2 = P_i+1,1 - P_i+1,0.
 *
 * Only points flagged `free` are moved. The projection minimises the weighted
 * squared distance  sum_i w_i * |P_i - Q_i|^2  to the edited positions `Q_i`
 * with non-negative rational weights, solved *exactly* with rational
 * arithmetic via the KKT system
 *
 *   [ 2W  A^T ] [ x ]   [ 2W q ]
 *   [ A    0  ] [ λ ] = [  b   ].
 *
 * Outcomes:
 *   - "conflict":   the linear constraints themselves are contradictory; an
 *                   exact certificate y with yᵀA = 0, yᵀb ≠ 0 is returned.
 *   - "non-unique": the optimum is not unique (only possible when some free
 *                   point carries weight 0); one optimum plus the exact
 *                   null-space directions are returned.
 *   - "unique":     the unique optimum, verified by back-substitution into
 *                   the constraint equations and by the KKT gradient.
 */

import { Rational, type RationalInput } from "./rational.ts";
import {
  matVecMul,
  matTVecMul,
  solveLinear,
  zeroMatrix,
  type Matrix,
  type Vector,
} from "./linalg.ts";

export interface Vec2Input {
  x: RationalInput;
  y: RationalInput;
}

export interface Vec2 {
  x: Rational;
  y: Rational;
}

export interface ControlPointInput {
  /** May the editor move this point? The two curve endpoints are always fixed. */
  free: boolean;
  /**
   * Non-negative rational weight of this point's squared-distance term.
   * Defaults to 1. A zero weight is allowed and may lead to a "non-unique"
   * diagnosis. Ignored for fixed points.
   */
  weight?: RationalInput;
  /**
   * Fixed points: the position the point is pinned at.
   * Free points: the edited position the projection is pulled towards.
   */
  position: Vec2Input;
}

export type SegmentInput = [
  ControlPointInput,
  ControlPointInput,
  ControlPointInput,
  ControlPointInput,
];

export interface ProjectionInput {
  /** 1..10 cubic Bézier segments. */
  segments: SegmentInput[];
}

export interface ConstraintInfo {
  /** Human-readable description, e.g. "C1 derivative match at joint 0". */
  label: string;
  /** Coefficients over the free variables (same for both coordinates). */
  row: Rational[];
  /** Right-hand side contributed by fixed points, per coordinate. */
  bx: Rational;
  by: Rational;
}

export interface Verification {
  /** True when A x - b is exactly zero in both coordinates. */
  constraintsSatisfied: boolean;
  /** True when 2W(x - q) + Aᵀλ is exactly zero in both coordinates. */
  gradientSatisfied: boolean;
  /** Largest |residual| over all constraint rows and coordinates (exactly 0). */
  maxConstraintResidual: Rational;
  /** Largest |KKT gradient component| over all variables and coordinates (exactly 0). */
  maxGradientResidual: Rational;
}

export interface VariableRef {
  segment: number;
  point: number;
}

export interface ConflictResult {
  status: "conflict";
  /** Coordinate in which the contradiction was found. */
  coordinate: "x" | "y";
  /**
   * Farkas-style certificate: multipliers y (one per constraint, in the order
   * of `constraints`) with Σ y_i row_i = 0 but Σ y_i b_i = certificateRhs ≠ 0.
   */
  certificate: Rational[];
  certificateRhs: Rational;
  constraints: ConstraintInfo[];
}

export interface OptimalResult {
  status: "unique" | "non-unique";
  /** Optimal positions of every control point, mirroring the input shape. */
  points: Vec2[][];
  /** Exact minimal value of Σ w_i |P_i - Q_i|². */
  objective: Rational;
  verification: Verification;
  constraints: ConstraintInfo[];
  /** Maps free-variable index to its control point. */
  variables: VariableRef[];
  /**
   * "non-unique" only: exact basis of the displacement directions along which
   * the optimum can move without changing the objective. Each entry maps a
   * free-variable index to its displacement (valid independently per
   * coordinate). Empty for "unique".
   */
  directions: Rational[][];
  /** "non-unique" only: free points whose zero weight permits the slack. */
  zeroWeightPoints: VariableRef[];
}

export type ProjectionResult = ConflictResult | OptimalResult;

interface FlatPoint {
  segment: number;
  point: number;
  free: boolean;
  weight: Rational;
  position: Vec2;
}

const MAX_SEGMENTS = 10;

export function projectControlPoints(input: ProjectionInput): ProjectionResult {
  const { segments } = input;
  if (segments.length < 1 || segments.length > MAX_SEGMENTS) {
    throw new Error(`expected 1..${MAX_SEGMENTS} segments, got ${segments.length}`);
  }

  // Flatten; the two curve endpoints are always pinned (端点固定).
  const flat: FlatPoint[] = [];
  segments.forEach((seg, si) => {
    if (seg.length !== 4) throw new Error(`segment ${si} must have 4 control points`);
    seg.forEach((p, pi) => {
      const isEndpoint = (si === 0 && pi === 0) || (si === segments.length - 1 && pi === 3);
      const weight = Rational.from(p.weight ?? 1);
      if (weight.isNegative()) {
        throw new Error(`negative weight at segment ${si} point ${pi}`);
      }
      flat.push({
        segment: si,
        point: pi,
        free: p.free && !isEndpoint,
        weight,
        position: { x: Rational.from(p.position.x), y: Rational.from(p.position.y) },
      });
    });
  });

  const variables: VariableRef[] = [];
  const varIndex = new Map<number, number>(); // flat point index -> variable index
  flat.forEach((p, i) => {
    if (p.free) {
      varIndex.set(i, variables.length);
      variables.push({ segment: p.segment, point: p.point });
    }
  });
  const nvars = variables.length;

  // Assemble geometric constraints as sparse rows over all flat points.
  interface SparseRow {
    label: string;
    coeffs: Array<[number, Rational]>; // flat point index -> coefficient
  }
  const sparse: SparseRow[] = [];
  const flatIndex = (si: number, pi: number) => si * 4 + pi;
  for (let si = 0; si < segments.length - 1; si++) {
    const one = Rational.one();
    const neg = one.neg();
    sparse.push({
      label: `C0 position match at joint ${si}`,
      coeffs: [
        [flatIndex(si, 3), one],
        [flatIndex(si + 1, 0), neg],
      ],
    });
    sparse.push({
      label: `C1 derivative match at joint ${si}`,
      coeffs: [
        [flatIndex(si, 2), neg],
        [flatIndex(si, 3), one],
        [flatIndex(si + 1, 0), one],
        [flatIndex(si + 1, 1), neg],
      ],
    });
  }

  // Substitute fixed points into the right-hand side.
  const constraints: ConstraintInfo[] = sparse.map(({ label, coeffs }) => {
    const row: Rational[] = Array.from({ length: nvars }, () => Rational.zero());
    let bx = Rational.zero();
    let by = Rational.zero();
    for (const [fi, coeff] of coeffs) {
      const p = flat[fi];
      const vi = varIndex.get(fi);
      if (vi !== undefined) {
        row[vi] = row[vi].add(coeff);
      } else {
        bx = bx.sub(coeff.mul(p.position.x));
        by = by.sub(coeff.mul(p.position.y));
      }
    }
    return { label, row, bx, by };
  });

  const a: Matrix = constraints.map((c) => c.row);
  const bx: Vector = constraints.map((c) => c.bx);
  const by: Vector = constraints.map((c) => c.by);

  // 1. Consistency of the linear constraints, per coordinate.
  for (const [coord, b] of [
    ["x", bx],
    ["y", by],
  ] as const) {
    const check = solveLinear(a, b);
    if (!check.consistent) {
      return {
        status: "conflict",
        coordinate: coord,
        certificate: check.certificate!,
        certificateRhs: check.certificateRhs!,
        constraints,
      };
    }
  }

  // 2. KKT system [2W Aᵀ; A 0] [x; λ] = [2W q; b], exact.
  const m = constraints.length;
  const weights: Rational[] = variables.map((v) => flat[flatIndex(v.segment, v.point)].weight);
  const kkt: Matrix = zeroMatrix(nvars + m, nvars + m);
  for (let i = 0; i < nvars; i++) kkt[i][i] = weights[i].mul(2);
  for (let ci = 0; ci < m; ci++) {
    for (let j = 0; j < nvars; j++) {
      if (a[ci][j].isZero()) continue;
      kkt[j][nvars + ci] = a[ci][j];
      kkt[nvars + ci][j] = a[ci][j];
    }
  }

  const solveCoord = (q: Vector, b: Vector) => {
    const rhs: Vector = [
      ...q.map((qv, i) => weights[i].mul(2).mul(qv)),
      ...b,
    ];
    const solved = solveLinear(kkt, rhs);
    if (!solved.consistent) {
      // Unreachable for a consistent convex QP; guards against internal bugs.
      throw new Error("internal error: KKT system inconsistent despite consistent constraints");
    }
    const x = solved.particular!.slice(0, nvars);
    const lambda = solved.particular!.slice(nvars);
    return { x, lambda };
  };

  const qx: Vector = variables.map((v) => flat[flatIndex(v.segment, v.point)].position.x);
  const qy: Vector = variables.map((v) => flat[flatIndex(v.segment, v.point)].position.y);
  const solX = solveCoord(qx, bx);
  const solY = solveCoord(qy, by);

  // 3. Uniqueness: null space of the (coordinate-independent) KKT matrix.
  //    A displacement (dx, dλ) with dx ≠ 0 means positions can move.
  const kktNull = solveLinear(
    kkt,
    Array.from({ length: nvars + m }, () => Rational.zero()),
  ).nullspace;
  const directions = kktNull
    .map((v) => v.slice(0, nvars))
    .filter((dx) => dx.some((c) => !c.isZero()));
  const unique = directions.length === 0;

  // 4. Verification: back-substitution into A x = b and the KKT gradient.
  const maxAbs = (vals: Rational[]) =>
    vals.reduce((acc, v) => (v.abs().cmp(acc) > 0 ? v.abs() : acc), Rational.zero());
  const resCx = matVecMul(a, solX.x).map((v, i) => v.sub(bx[i]));
  const resCy = matVecMul(a, solY.x).map((v, i) => v.sub(by[i]));
  const gradX = matTVecMul(a, solX.lambda).map((v, i) =>
    weights[i].mul(2).mul(solX.x[i].sub(qx[i])).add(v),
  );
  const gradY = matTVecMul(a, solY.lambda).map((v, i) =>
    weights[i].mul(2).mul(solY.x[i].sub(qy[i])).add(v),
  );
  const maxConstraintResidual = maxAbs([...resCx, ...resCy]);
  const maxGradientResidual = maxAbs([...gradX, ...gradY]);
  const verification: Verification = {
    constraintsSatisfied: maxConstraintResidual.isZero(),
    gradientSatisfied: maxGradientResidual.isZero(),
    maxConstraintResidual,
    maxGradientResidual,
  };

  // 5. Assemble points and the exact objective value.
  const points: Vec2[][] = segments.map((_, si) =>
    Array.from({ length: 4 }, (_, pi) => {
      const fi = flatIndex(si, pi);
      const vi = varIndex.get(fi);
      if (vi === undefined) return flat[fi].position;
      return { x: solX.x[vi], y: solY.x[vi] };
    }),
  );
  let objective = Rational.zero();
  for (let i = 0; i < nvars; i++) {
    const dx = solX.x[i].sub(qx[i]);
    const dy = solY.x[i].sub(qy[i]);
    objective = objective.add(weights[i].mul(dx.mul(dx).add(dy.mul(dy))));
  }

  const zeroWeightPoints = variables.filter(
    (v) => flat[flatIndex(v.segment, v.point)].weight.isZero(),
  );

  return {
    status: unique ? "unique" : "non-unique",
    points,
    objective,
    verification,
    constraints,
    variables,
    directions,
    zeroWeightPoints,
  };
}
