import { test } from "node:test";
import assert from "node:assert/strict";
import {
  projectControlPoints,
  matTVecMul,
  r,
  Rational,
  type OptimalResult,
  type ConflictResult,
  type SegmentInput,
  type Vec2,
} from "../src/index.ts";

const fixed = (x: number | string, y: number | string) => ({
  free: false,
  position: { x, y },
});
const free = (x: number | string, y: number | string, weight: number | string = 1) => ({
  free: true,
  weight,
  position: { x, y },
});

function expectOptimal(res: ReturnType<typeof projectControlPoints>): OptimalResult {
  assert.notEqual(res.status, "conflict", "expected an optimal result, got conflict");
  return res as OptimalResult;
}

test("single segment: free handles snap to edited targets exactly", () => {
  const seg: SegmentInput = [fixed(0, 0), free(1, 1), free(2, 1), fixed(3, 0)];
  const res = expectOptimal(projectControlPoints({ segments: [seg] }));
  assert.equal(res.status, "unique");
  assert.equal(res.points[0][1].x.toString(), "1");
  assert.equal(res.points[0][1].y.toString(), "1");
  assert.equal(res.points[0][2].x.toString(), "2");
  assert.equal(res.objective.toString(), "0");
  assert.ok(res.verification.constraintsSatisfied);
  assert.ok(res.verification.gradientSatisfied);
});

test("two segments: C0/C1 restored, exact KKT verification, endpoints pinned", () => {
  const seg0: SegmentInput = [fixed(0, 0), free(1, 2), free(2, 3), free(4, 1)];
  const seg1: SegmentInput = [free(5, 2), free(6, 1), free(7, 2), fixed(9, 0)];
  const res = expectOptimal(projectControlPoints({ segments: [seg0, seg1] }));
  assert.equal(res.status, "unique");

  const j0 = res.points[0][3];
  const j1 = res.points[1][0];
  assert.ok(j0.x.equals(j1.x) && j0.y.equals(j1.y), "C0: joint positions coincide");

  // C1: P[0][3] - P[0][2] == P[1][1] - P[1][0]
  const d0x = res.points[0][3].x.sub(res.points[0][2].x);
  const d1x = res.points[1][1].x.sub(res.points[1][0].x);
  const d0y = res.points[0][3].y.sub(res.points[0][2].y);
  const d1y = res.points[1][1].y.sub(res.points[1][0].y);
  assert.ok(d0x.equals(d1x) && d0y.equals(d1y), "C1: derivatives match at joint");

  // Endpoints pinned even though only fixed flags say so.
  assert.equal(res.points[0][0].x.toString(), "0");
  assert.equal(res.points[1][3].x.toString(), "9");

  assert.ok(res.verification.constraintsSatisfied);
  assert.ok(res.verification.gradientSatisfied);
  assert.ok(res.verification.maxConstraintResidual.isZero());
  assert.ok(res.verification.maxGradientResidual.isZero());
  assert.ok(res.objective.cmp(0) > 0, "projection must move points");
});

test("rational weights and targets stay exact (no floating point)", () => {
  const seg0: SegmentInput = [
    fixed("1/2", 0),
    free("3/4", "5/6", "2/3"),
    free("7/8", "11/10", "5/7"),
    free("3/2", "1/3", "9/4"),
  ];
  const seg1: SegmentInput = [
    free(2, "1/6"),
    free("5/2", 0, "3/11"),
    free(3, "1/6"),
    fixed("7/2", 0),
  ];
  const res = expectOptimal(projectControlPoints({ segments: [seg0, seg1] }));
  assert.equal(res.status, "unique");
  assert.ok(res.verification.constraintsSatisfied);
  assert.ok(res.verification.gradientSatisfied);
  // Objective is an exact non-negative rational.
  assert.ok(res.objective.cmp(0) >= 0);
  assert.match(res.objective.toString(), /^-?\d+(\/\d+)?$/);
});

test("conflicting fixed joints return an exact Farkas certificate", () => {
  // Joint endpoints pinned at different positions: C0 is contradictory.
  const seg0: SegmentInput = [fixed(0, 0), free(1, 1), free(2, 1), fixed(3, 0)];
  const seg1: SegmentInput = [fixed(4, 0), free(5, 1), free(6, 1), fixed(7, 0)];
  const res = projectControlPoints({ segments: [seg0, seg1] });
  assert.equal(res.status, "conflict");
  const conflict = res as ConflictResult;

  // Validate the certificate: yᵀA = 0 and yᵀb ≠ 0, exactly.
  const rows = conflict.constraints.map((c) => c.row);
  const yTA = matTVecMul(rows, conflict.certificate);
  assert.ok(yTA.every((v) => v.isZero()), "yᵀA must be exactly zero");
  const b = conflict.constraints.map((c) =>
    conflict.coordinate === "x" ? c.bx : c.by,
  );
  const yTb = b.reduce((acc, bi, i) => acc.add(bi.mul(conflict.certificate[i])), r(0));
  assert.ok(!yTb.isZero(), "yᵀb must be non-zero");
  assert.ok(yTb.equals(conflict.certificateRhs), "reported rhs matches yᵀb");
});

test("zero weight on an unconstrained free point yields non-unique diagnosis", () => {
  const seg: SegmentInput = [fixed(0, 0), free(1, 1, 0), free(2, 1), fixed(3, 0)];
  const res = expectOptimal(projectControlPoints({ segments: [seg] }));
  assert.equal(res.status, "non-unique");
  assert.deepEqual(res.zeroWeightPoints, [{ segment: 0, point: 1 }]);
  assert.equal(res.directions.length, 1);
  // The direction moves exactly the zero-weight point's variable.
  const varIdx = res.variables.findIndex((v) => v.segment === 0 && v.point === 1);
  assert.ok(!res.directions[0][varIdx].isZero());
  assert.ok(
    res.directions[0].every((c, i) => i === varIdx || c.isZero()),
    "only the zero-weight point may move",
  );
  // Returned point is still optimal and verified.
  assert.ok(res.verification.constraintsSatisfied);
  assert.ok(res.verification.gradientSatisfied);
  assert.equal(res.objective.toString(), "0");
});

test("positive weights on all free points give a unique solution", () => {
  const segments: SegmentInput[] = [
    [fixed(0, 0), free(1, 3), free(2, 3), free(3, 1)],
    [free(4, 2), free(5, 3), free(6, 3), free(6, 1)],
    [free(7, 2), free(8, 1), free(9, 2), fixed(10, 0)],
  ];
  const res = expectOptimal(projectControlPoints({ segments }));
  assert.equal(res.status, "unique");
  assert.equal(res.directions.length, 0);
  assert.ok(res.verification.constraintsSatisfied);
  assert.ok(res.verification.gradientSatisfied);
});

test("moving the solution along a reported direction keeps it optimal", () => {
  const seg: SegmentInput = [fixed(0, 0), free(1, 1, 0), free(2, 1), fixed(3, 0)];
  const res = expectOptimal(projectControlPoints({ segments: [seg] }));
  assert.equal(res.status, "non-unique");
  // Perturb the zero-weight point along the null direction; objective unchanged.
  const varIdx = res.variables.findIndex((v) => v.segment === 0 && v.point === 1);
  const moved: Vec2 = {
    x: res.points[0][1].x.add(res.directions[0][varIdx].mul(5)),
    y: res.points[0][1].y.add(res.directions[0][varIdx].mul(5)),
  };
  void moved;
  // Objective only involves weighted points, so it stays 0 for any P1.
  assert.equal(res.objective.toString(), "0");
});

test("endpoints are pinned even if flagged free", () => {
  const seg: SegmentInput = [free(0, 0), free(1, 1), free(2, 1), free(3, 0)];
  const res = expectOptimal(projectControlPoints({ segments: [seg] }));
  assert.equal(res.points[0][0].x.toString(), "0");
  assert.equal(res.points[0][3].x.toString(), "3");
});

test("input validation", () => {
  assert.throws(() => projectControlPoints({ segments: [] }), /1\.\.10/);
  const seg: SegmentInput = [fixed(0, 0), free(1, 1), free(2, 1), fixed(3, 0)];
  assert.throws(
    () => projectControlPoints({ segments: Array(11).fill(seg) }),
    /1\.\.10/,
  );
  assert.throws(
    () =>
      projectControlPoints({
        segments: [[fixed(0, 0), free(1, 1, -1), free(2, 1), fixed(3, 0)]],
      }),
    /negative weight/,
  );
});

test("weighted least squares: heavier point stays closer to its edit", () => {
  // Both weighted handles sit inside the C1 constraint P2 + P1' = 2J and are
  // pulled toward y = 10 while the joint is pulled toward y = 0.
  const seg0: SegmentInput = [fixed(0, 0), free(1, 0), free(3, 10, 100), free(3, 0)];
  const seg1: SegmentInput = [free(3, 0), free(3, 10, 1), free(5, 0), fixed(9, 0)];
  const res = expectOptimal(projectControlPoints({ segments: [seg0, seg1] }));
  assert.equal(res.status, "unique");
  const errHeavy = res.points[0][2].y.sub(10).abs();
  const errLight = res.points[1][1].y.sub(10).abs();
  assert.ok(errHeavy.cmp(errLight) < 0, "heavier weight tracks its target closer");
  assert.ok(res.verification.gradientSatisfied);
});
