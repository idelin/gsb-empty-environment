import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { solve, type LPInput, type Sense } from '../src/simplex.ts';

const R = (s: string) => Rational.parse(s);
const ZERO = Rational.zero();

/** Checks primal feasibility of x against the original LP. */
function assertPrimalFeasible(lp: LPInput, x: Rational[]): void {
  for (const v of x) assert.ok(v.sign() >= 0, `x must be nonnegative, got ${v}`);
  lp.constraints.forEach((con, i) => {
    let lhs = ZERO;
    con.coeffs.forEach((a, j) => {
      lhs = lhs.add(a.mul(x[j]));
    });
    const cmp = lhs.cmp(con.rhs);
    const ok =
      (con.sense === '<=' && cmp <= 0) ||
      (con.sense === '>=' && cmp >= 0) ||
      (con.sense === '=' && cmp === 0);
    assert.ok(ok, `constraint ${i + 1} violated: ${lhs} ${con.sense} ${con.rhs}`);
  });
}

/** Checks dual feasibility, sign restrictions and strong duality. */
function assertDualProof(lp: LPInput, duals: Rational[], objective: Rational): void {
  // Sign restrictions: y_i >= 0 for '<=', y_i <= 0 for '>=', free for '='.
  lp.constraints.forEach((con, i) => {
    if (con.sense === '<=') assert.ok(duals[i].sign() >= 0, `dual ${i + 1} sign`);
    if (con.sense === '>=') assert.ok(duals[i].sign() <= 0, `dual ${i + 1} sign`);
  });
  // Dual feasibility: A^T y >= c (maximization, x >= 0).
  for (let j = 0; j < lp.objective.length; j++) {
    let lhs = ZERO;
    lp.constraints.forEach((con, i) => {
      lhs = lhs.add(con.coeffs[j].mul(duals[i]));
    });
    assert.ok(lhs.cmp(lp.objective[j]) >= 0, `dual constraint ${j + 1}: ${lhs} >= ${lp.objective[j]}`);
  }
  // Strong duality: b^T y == c^T x.
  let dualObj = ZERO;
  lp.constraints.forEach((con, i) => {
    dualObj = dualObj.add(con.rhs.mul(duals[i]));
  });
  assert.ok(dualObj.eq(objective), `strong duality: ${dualObj} == ${objective}`);
}

test('acceptance: 3-constraint maximization with exact optimum 17/4', () => {
  const lp: LPInput = {
    objective: [R('1'), R('1')],
    constraints: [
      { coeffs: [R('1'), R('3')], sense: '<=', rhs: R('8') },
      { coeffs: [R('3'), R('1')], sense: '<=', rhs: R('9') },
      { coeffs: [R('1'), R('1')], sense: '<=', rhs: R('5') },
    ],
  };
  const result = solve(lp);
  assert.equal(result.status, 'optimal');
  if (result.status !== 'optimal') return;

  // Exact global optimum: z = 17/4 at x = (19/8, 15/8).
  assert.equal(result.objective.toString(), '17/4');
  assert.deepEqual(result.x.map(String), ['19/8', '15/8']);
  // Exact dual (shadow) prices.
  assert.deepEqual(result.duals.map(String), ['1/4', '1/4', '0']);

  // Basis replacement happened entirely in rational arithmetic.
  assert.ok(result.steps.length > 0);
  for (const step of result.steps) {
    assert.ok(step.pivot instanceof Rational);
    assert.ok(!step.pivot.isZero());
  }

  assertPrimalFeasible(lp, result.x);
  assertDualProof(lp, result.duals, result.objective);

  // Complementary slackness: slack constraint 3 has dual price 0.
  let lhs3 = ZERO;
  lp.constraints[2].coeffs.forEach((a, j) => {
    lhs3 = lhs3.add(a.mul(result.x[j]));
  });
  assert.ok(lhs3.cmp(lp.constraints[2].rhs) < 0);
  assert.ok(result.duals[2].isZero());
});

test('unbounded: returns an exact feasible point and recession ray', () => {
  // max x1 + x2  s.t.  x1 - x2 <= 1  (x2 = t -> unbounded along (1, 1))
  const lp: LPInput = {
    objective: [R('1'), R('1')],
    constraints: [{ coeffs: [R('1'), R('-1')], sense: '<=', rhs: R('1') }],
  };
  const result = solve(lp);
  assert.equal(result.status, 'unbounded');
  if (result.status !== 'unbounded') return;

  assertPrimalFeasible(lp, result.point);
  // Certificate: ray >= 0, A * ray <= 0 (per sense), c^T ray > 0.
  for (const d of result.ray) assert.ok(d.sign() >= 0);
  lp.constraints.forEach((con) => {
    let lhs = ZERO;
    con.coeffs.forEach((a, j) => {
      lhs = lhs.add(a.mul(result.ray[j]));
    });
    assert.ok(lhs.sign() <= 0, `recession ray must satisfy A d ${con.sense} 0`);
  });
  let gain = ZERO;
  lp.objective.forEach((c, j) => {
    gain = gain.add(c.mul(result.ray[j]));
  });
  assert.ok(gain.sign() > 0, 'ray must strictly improve the objective');
});

test('infeasible: returns a Farkas certificate of infeasibility', () => {
  // x1 + x2 <= 1  and  x1 + x2 >= 3  cannot both hold.
  const lp: LPInput = {
    objective: [R('1'), R('1')],
    constraints: [
      { coeffs: [R('1'), R('1')], sense: '<=', rhs: R('1') },
      { coeffs: [R('1'), R('1')], sense: '>=', rhs: R('3') },
    ],
  };
  const result = solve(lp);
  assert.equal(result.status, 'infeasible');
  if (result.status !== 'infeasible') return;

  const y = result.farkas;
  assert.equal(y.length, lp.constraints.length);
  // Sign restrictions per sense.
  lp.constraints.forEach((con, i) => {
    if (con.sense === '<=') assert.ok(y[i].sign() >= 0);
    if (con.sense === '>=') assert.ok(y[i].sign() <= 0);
  });
  // A^T y >= 0 (since x >= 0) ...
  for (let j = 0; j < lp.objective.length; j++) {
    let lhs = ZERO;
    lp.constraints.forEach((con, i) => {
      lhs = lhs.add(con.coeffs[j].mul(y[i]));
    });
    assert.ok(lhs.sign() >= 0, `Farkas: (A^T y)_${j} = ${lhs} >= 0`);
  }
  // ... yet b^T y < 0, proving infeasibility.
  let yb = ZERO;
  lp.constraints.forEach((con, i) => {
    yb = yb.add(con.rhs.mul(y[i]));
  });
  assert.ok(yb.sign() < 0, `Farkas: b^T y = ${yb} < 0`);
});

test('equality constraints and mixed senses solve exactly', () => {
  // max 3 x1 + 2 x2  s.t. x1 + x2 = 4, 2 x1 + x2 <= 5, x1, x2 >= 0
  // optimum: x = (1, 3), z = 9
  const lp: LPInput = {
    objective: [R('3'), R('2')],
    constraints: [
      { coeffs: [R('1'), R('1')], sense: '=', rhs: R('4') },
      { coeffs: [R('2'), R('1')], sense: '<=', rhs: R('5') },
    ],
  };
  const result = solve(lp);
  assert.equal(result.status, 'optimal');
  if (result.status !== 'optimal') return;
  assert.equal(result.objective.toString(), '9');
  assert.deepEqual(result.x.map(String), ['1', '3']);
  assertPrimalFeasible(lp, result.x);
  assertDualProof(lp, result.duals, result.objective);
});

test("Beale's cycling example terminates under Bland's rule", () => {
  // Classic degenerate LP that cycles under Dantzig's rule.
  const lp: LPInput = {
    objective: [R('3/4'), R('-150'), R('1/50'), R('-6')],
    constraints: [
      { coeffs: [R('1/4'), R('-60'), R('-1/25'), R('9')], sense: '<=', rhs: R('0') },
      { coeffs: [R('1/2'), R('-90'), R('-1/50'), R('3')], sense: '<=', rhs: R('0') },
      { coeffs: [R('0'), R('0'), R('1'), R('0')], sense: '<=', rhs: R('1') },
    ],
  };
  const result = solve(lp);
  assert.equal(result.status, 'optimal');
  if (result.status !== 'optimal') return;
  assert.equal(result.objective.toString(), '1/20');
  assertPrimalFeasible(lp, result.x);
  assertDualProof(lp, result.duals, result.objective);
});

test('negative rhs rows are normalized and duals map back correctly', () => {
  // max x1  s.t. -x1 - x2 >= -4 (i.e. x1 + x2 <= 4), x1 <= 3
  const lp: LPInput = {
    objective: [R('1'), R('0')],
    constraints: [
      { coeffs: [R('-1'), R('-1')], sense: '>=', rhs: R('-4') },
      { coeffs: [R('1'), R('0')], sense: '<=', rhs: R('3') },
    ],
  };
  const result = solve(lp);
  assert.equal(result.status, 'optimal');
  if (result.status !== 'optimal') return;
  assert.equal(result.objective.toString(), '3');
  assertPrimalFeasible(lp, result.x);
  assertDualProof(lp, result.duals, result.objective);
});
