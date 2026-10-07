import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational, projectBezier } from '../src/index.ts';
import type {
  ProjectionInput,
  Vec2,
} from '../src/index.ts';

const R = Rational.from;

function vec(x: Rational | number | string, y: Rational | number | string) {
  return { x: R(x), y: R(y) };
}

function assertVec(actual: Vec2, x: Rational | number | string, y: Rational | number | string) {
  assert.ok(actual.x.equals(R(x)), `x: expected ${R(x)}, got ${actual.x}`);
  assert.ok(actual.y.equals(R(y)), `y: expected ${R(y)}, got ${actual.y}`);
}

/**
 * Hand-solved fixture: two segments, endpoints pinned, B0 and C1 fixed.
 * Edited handles: C0=(2,1) D0=(3,1) A1=(3,-1) B1=(4,-1), all weights 1.
 * x-axis already feasible -> unchanged. y-axis projection (solved by hand):
 *   C0.y=1, D0.y=0, A1.y=0, B1.y=-1, objective = 2.
 */
function handSolvedInput(weights: (number | string)[][] = [[1, 1, 1, 1], [1, 1, 1, 1]]): ProjectionInput {
  return {
    segments: [
      { points: [vec(0, 0), vec(1, 0), vec(2, 1), vec(3, 1)] },
      { points: [vec(3, -1), vec(4, -1), vec(5, 0), vec(6, 0)] },
    ],
    weights,
    free: [
      [true, false, true, true],
      [true, true, false, true],
    ],
  };
}

test('hand-solved C1 projection matches exact expected values', () => {
  const result = projectBezier(handSolvedInput());
  assert.equal(result.status, 'unique');
  if (result.status === 'infeasible') return;

  const [s0, s1] = result.points;
  assertVec(s0[0], 0, 0); // pinned start
  assertVec(s0[1], 1, 0); // fixed
  assertVec(s0[2], 2, 1); // C0
  assertVec(s0[3], 3, 0); // D0 (joint)
  assertVec(s1[0], 3, 0); // A1 (joint, coincident with D0)
  assertVec(s1[1], 4, -1); // B1
  assertVec(s1[2], 5, 0); // fixed
  assertVec(s1[3], 6, 0); // pinned end

  const v = result.verification;
  assert.ok(v.constraintsSatisfied);
  assert.ok(v.maxConstraintResidual.isZero());
  assert.ok(v.gradientOrthogonalToNullspace);
  assert.ok(v.maxGradientComponent.isZero());
  assert.ok(v.maxLagrangeResidual.isZero());
  assert.ok(v.objectiveValue.equals(R(2)), `objective ${v.objectiveValue}`);
});

test('conflicting fixed joint points yield a verified contradiction certificate', () => {
  const input: ProjectionInput = {
    segments: [
      { points: [vec(0, 0), vec(1, 0), vec(2, 0), vec(0, 0)] }, // D0 fixed at (0,0)
      { points: [vec(1, 0), vec(4, 0), vec(5, 0), vec(6, 0)] }, // A1 fixed at (1,0)
    ],
    weights: [
      [1, 1, 1, 1],
      [1, 1, 1, 1],
    ],
    free: [
      [true, true, true, false], // D0 fixed
      [false, true, true, true], // A1 fixed
    ],
  };
  const result = projectBezier(input);
  assert.equal(result.status, 'infeasible');
  if (result.status !== 'infeasible') return;
  const cert = result.certificate;
  assert.equal(cert.axis, 'x');
  assert.ok(!cert.contradictionValue.isZero());
  assert.ok(cert.verified, 'certificate self-check: combination.M = 0, combination.c != 0');
  assert.ok(cert.combination.length >= 2);
  // The coincidence row must participate in the contradiction.
  assert.ok(cert.combination.some((e) => e.label.includes('coincidence')));
});

test('zero weights on unconstrained null directions produce non-unique diagnostic', () => {
  // C0 and B1 carry zero weight; the direction (C0 +t, B1 -t) stays on the
  // constraint manifold at zero cost.
  const result = projectBezier(
    handSolvedInput([
      [1, 1, 0, 1],
      [1, 0, 1, 1],
    ]),
  );
  assert.equal(result.status, 'non-unique');
  if (result.status !== 'non-unique') return;

  assert.equal(result.nullDirections.length, 1);
  const dir = result.nullDirections[0];
  assert.deepEqual(dir.support, [
    { segment: 0, point: 2 },
    { segment: 1, point: 1 },
  ]);
  // Direction shape: C0 and B1 move by opposite amounts (both axes share it).
  const dC0 = dir.vector[0][2].x;
  const dB1 = dir.vector[1][1].x;
  assert.ok(dC0.equals(dB1.neg()), `${dC0} vs ${dB1}`);

  // The returned point is still an exact, verified minimizer.
  const v = result.verification;
  assert.ok(v.constraintsSatisfied);
  assert.ok(v.gradientOrthogonalToNullspace);
  assert.ok(v.maxLagrangeResidual.isZero());
});

test('zero weight on a constraint-pinned point stays unique', () => {
  // B0 is fixed (pinned by a row); its zero weight cannot create freedom.
  const result = projectBezier(
    handSolvedInput([
      [1, 0, 1, 1],
      [1, 1, 1, 1],
    ]),
  );
  assert.equal(result.status, 'unique');
});

test('exact rational weights and positions keep residuals exactly zero', () => {
  const input: ProjectionInput = {
    segments: [
      {
        points: [
          vec('1/7', '2/9'),
          vec('3/5', '-4/11'),
          vec('7/3', '5/6'),
          vec('11/13', '1/2'),
        ],
      },
      {
        points: [
          vec('11/13', '-1/2'),
          vec('2/17', '9/4'),
          vec('5/3', '7/8'),
          vec('19/5', '3/7'),
        ],
      },
    ],
    weights: [
      ['1/3', '2/7', '5/11', '3/2'],
      ['7/5', '11/13', '1/6', '2/3'],
    ],
    free: [
      [true, true, true, true],
      [true, true, true, true],
    ],
  };
  const result = projectBezier(input);
  assert.equal(result.status, 'unique');
  if (result.status === 'infeasible') return;
  const v = result.verification;
  assert.ok(v.constraintsSatisfied);
  assert.ok(v.maxConstraintResidual.isZero());
  assert.ok(v.gradientOrthogonalToNullspace);
  assert.ok(v.maxGradientComponent.isZero());
  assert.ok(v.maxLagrangeResidual.isZero());
  assert.ok(v.objectiveValue.sign() > 0);
  // Endpoints pinned at their (fractional) input positions.
  assertVec(result.points[0][0], '1/7', '2/9');
  assertVec(result.points[1][3], '19/5', '3/7');
  // Joint coincidence holds exactly.
  assert.ok(result.points[0][3].x.equals(result.points[1][0].x));
  assert.ok(result.points[0][3].y.equals(result.points[1][0].y));
});

test('single segment with pinned endpoints keeps endpoints exactly fixed', () => {
  const input: ProjectionInput = {
    segments: [
      { points: [vec(0, 0), vec(1, 2), vec(2, 2), vec(3, 0)] },
    ],
    weights: [[1, 1, 1, 1]],
    free: [[true, true, true, true]],
  };
  const result = projectBezier(input);
  assert.equal(result.status, 'unique');
  if (result.status === 'infeasible') return;
  assertVec(result.points[0][0], 0, 0);
  assertVec(result.points[0][3], 3, 0);
  // No joint constraints: interior points are already optimal, nothing moves.
  assertVec(result.points[0][1], 1, 2);
  assertVec(result.points[0][2], 2, 2);
  assert.ok(result.verification.objectiveValue.isZero());
});

test('ten-segment chain solves uniquely and verifies', () => {
  const segments = Array.from({ length: 10 }, (_, s) => ({
    points: [
      vec(4 * s, 0),
      vec(4 * s + 1, s % 2 === 0 ? 2 : -2),
      vec(4 * s + 2, s % 2 === 0 ? 3 : -3),
      vec(4 * s + 3, 1), // edited joint: deliberately off the next A
    ] as [ReturnType<typeof vec>, ReturnType<typeof vec>, ReturnType<typeof vec>, ReturnType<typeof vec>],
  }));
  const input: ProjectionInput = {
    segments,
    weights: Array.from({ length: 10 }, (_, s) =>
      Array.from({ length: 4 }, (_, p) => `${s + p + 1}/7`),
    ),
    free: Array.from({ length: 10 }, () => [true, true, true, true]),
  };
  const result = projectBezier(input);
  assert.equal(result.status, 'unique');
  if (result.status === 'infeasible') return;
  const v = result.verification;
  assert.ok(v.constraintsSatisfied);
  assert.ok(v.gradientOrthogonalToNullspace);
  assert.ok(v.maxLagrangeResidual.isZero());
  assertVec(result.points[0][0], 0, 0);
  assertVec(result.points[9][3], 39, 1);
  // Every joint coincides exactly.
  for (let j = 0; j < 9; j++) {
    assert.ok(result.points[j][3].x.equals(result.points[j + 1][0].x));
    assert.ok(result.points[j][3].y.equals(result.points[j + 1][0].y));
  }
});

test('pinEndpoints=false leaves endpoints to the weights', () => {
  const input: ProjectionInput = {
    segments: [
      { points: [vec(0, 0), vec(1, 1), vec(2, 1), vec(3, 0)] },
      { points: [vec(3, 0), vec(4, -1), vec(5, 1), vec(6, 0)] },
    ],
    weights: [
      [1, 1, 1, 1],
      [1, 1, 1, 1],
    ],
    free: [
      [true, true, true, true],
      [true, true, true, true],
    ],
    pinEndpoints: false,
  };
  const result = projectBezier(input);
  assert.equal(result.status, 'unique');
  if (result.status === 'infeasible') return;
  // Already feasible input: nothing moves, objective is exactly zero.
  assert.ok(result.verification.objectiveValue.isZero());
  assertVec(result.points[0][0], 0, 0);
});
