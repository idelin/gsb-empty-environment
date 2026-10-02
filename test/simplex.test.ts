import { test } from "node:test";
import assert from "node:assert/strict";
import { Fraction, frac } from "../src/fraction.ts";
import { solveLP, type LPProblem } from "../src/simplex.ts";

function dot(a: readonly Fraction[], b: readonly Fraction[]): Fraction {
  let sum = Fraction.ZERO;
  for (let i = 0; i < a.length; i++) sum = sum.add(a[i].mul(b[i]));
  return sum;
}

/**
 * Acceptance problem: a maximization LP with 3 inequality constraints whose
 * exact optimum is 17/4.
 *
 *   max  3 x1 + 4 x2
 *   s.t. 2 x1 +   x2 <= 2
 *          x1 + 3 x2 <= 9/4
 *          x1 +   x2 <= 2
 *          x1, x2 >= 0
 *
 * Optimal vertex: x = (3/4, 1/2) with objective 9/4 + 2 = 17/4.
 * Dual prices:    y = (1, 1, 0) with y^T b = 2 + 9/4 = 17/4.
 */
const ACCEPTANCE: LPProblem = {
  objective: "max",
  coefficients: [3, 4],
  constraints: [
    { coefficients: [2, 1], sense: "<=", rhs: 2 },
    { coefficients: [1, 3], sense: "<=", rhs: "9/4" },
    { coefficients: [1, 1], sense: "<=", rhs: 2 },
  ],
};

test("acceptance: 3-constraint maximization solves to exactly 17/4", () => {
  const result = solveLP(ACCEPTANCE);
  assert.equal(result.status, "optimal");
  assert.ok(result.iterations > 0, "performs at least one basis exchange");

  // Exact rational optimal solution vector.
  assert.deepEqual(
    result.primal!.map(String),
    ["3/4", "1/2"],
  );

  // Exact optimal objective value: 17/4.
  assert.equal(result.objectiveValue!.toString(), "17/4");
  assert.equal(
    dot(Fraction.array(ACCEPTANCE.coefficients), result.primal!).toString(),
    "17/4",
  );
});

test("acceptance: dual prices prove optimality via strong duality", () => {
  const result = solveLP(ACCEPTANCE);
  const y = result.duals!;
  assert.equal(y.length, 3);
  assert.deepEqual(y.map(String), ["1", "1", "0"]);

  const A = ACCEPTANCE.constraints.map((c) => Fraction.array(c.coefficients));
  const b = ACCEPTANCE.constraints.map((c) => frac(c.rhs));
  const c = Fraction.array(ACCEPTANCE.coefficients);

  // Dual feasibility for a max problem with "<=" rows: y >= 0 and y^T A >= c.
  for (const yi of y) assert.ok(!yi.isNegative(), "y >= 0");
  for (let j = 0; j < c.length; j++) {
    let lhs = Fraction.ZERO;
    for (let i = 0; i < y.length; i++) lhs = lhs.add(y[i].mul(A[i][j]));
    assert.ok(
      lhs.compare(c[j]) >= 0,
      `dual constraint ${j}: (y^T A)_j = ${lhs} >= c_j = ${c[j]}`,
    );
  }

  // Strong duality: y^T b equals the primal optimum 17/4 — the dual prices
  // certify that no feasible solution can do better.
  assert.equal(dot(y, b).toString(), "17/4");
  assert.ok(dot(y, b).equals(result.objectiveValue!));

  // Complementary slackness: the third constraint is loose (5/4 < 2), so
  // its shadow price is zero; the binding constraints price out at 1 each.
  const slack2 = b[2].sub(dot(A[2], result.primal!));
  assert.ok(slack2.isPositive());
  assert.ok(y[2].isZero());
});

test("unbounded problem returns an exact ray certificate", () => {
  // max x1  s.t. x1 - x2 <= 1  (x2 -> infinity drags x1 -> infinity)
  const result = solveLP({
    objective: "max",
    coefficients: [1, 0],
    constraints: [{ coefficients: [1, -1], sense: "<=", rhs: 1 }],
  });
  assert.equal(result.status, "unbounded");

  const point = result.unboundedPoint!;
  const ray = result.unboundedRay!;
  const c = [frac(1), frac(0)];
  const row = [frac(1), frac(-1)];

  // Certificate validity: c^T d > 0, d >= 0, and point + t*d stays feasible.
  assert.ok(dot(c, ray).isPositive());
  for (const d of ray) assert.ok(!d.isNegative());
  for (const t of ["0", "3", "1000/7"]) {
    const xt = point.map((p, j) => p.add(ray[j].mul(t)));
    assert.ok(dot(row, xt).compare(1) <= 0, `feasible at t=${t}`);
    for (const x of xt) assert.ok(!x.isNegative());
  }
});

test("infeasible problem returns a Farkas certificate", () => {
  // x1 + x2 <= 1  and  x1 + x2 >= 3  cannot both hold.
  const problem: LPProblem = {
    objective: "max",
    coefficients: [1, 1],
    constraints: [
      { coefficients: [1, 1], sense: "<=", rhs: 1 },
      { coefficients: [1, 1], sense: ">=", rhs: 3 },
    ],
  };
  const result = solveLP(problem);
  assert.equal(result.status, "infeasible");

  const y = result.farkasCertificate!;
  assert.equal(y.length, 2);

  // Farkas sign conditions: y_i >= 0 on "<=" rows, y_i <= 0 on ">=" rows.
  assert.ok(!y[0].isNegative());
  assert.ok(!y[1].isPositive());

  // y^T A >= 0 componentwise, yet y^T b < 0: contradiction => infeasible.
  const A = problem.constraints.map((cstr) => Fraction.array(cstr.coefficients));
  const b = problem.constraints.map((cstr) => frac(cstr.rhs));
  for (let j = 0; j < 2; j++) {
    let combo = Fraction.ZERO;
    for (let i = 0; i < y.length; i++) combo = combo.add(y[i].mul(A[i][j]));
    assert.ok(!combo.isNegative(), `(y^T A)_${j} = ${combo} >= 0`);
  }
  assert.ok(dot(y, b).isNegative(), `y^T b = ${dot(y, b)} < 0`);
});

test("equality constraints and minimization are exact", () => {
  // min x1 + x2  s.t. x1 + 2 x2 = 4, 3 x1 + x2 >= 3
  // Vertex (2/5, 9/5): objective 11/5.
  const result = solveLP({
    objective: "min",
    coefficients: [1, 1],
    constraints: [
      { coefficients: [1, 2], sense: "=", rhs: 4 },
      { coefficients: [3, 1], sense: ">=", rhs: 3 },
    ],
  });
  assert.equal(result.status, "optimal");
  assert.deepEqual(result.primal!.map(String), ["2/5", "9/5"]);
  assert.equal(result.objectiveValue!.toString(), "11/5");

  // Duals: y^T b matches the optimum (strong duality for the min form).
  const b = [frac(4), frac(3)];
  assert.ok(dot(result.duals!, b).equals(result.objectiveValue!));
});

test("negative right-hand sides are normalized", () => {
  // max x1  s.t. -x1 >= -2  (i.e. x1 <= 2)
  const result = solveLP({
    objective: "max",
    coefficients: [1],
    constraints: [{ coefficients: [-1], sense: ">=", rhs: -2 }],
  });
  assert.equal(result.status, "optimal");
  assert.equal(result.objectiveValue!.toString(), "2");
  assert.deepEqual(result.primal!.map(String), ["2"]);
});

test("degenerate problem terminates with the exact optimum (Bland's rule)", () => {
  // Classic Beale cycling example; Bland's rule must not cycle.
  const result = solveLP({
    objective: "max",
    coefficients: ["3/4", -150, "1/50", -6],
    constraints: [
      { coefficients: ["1/4", -60, "-1/25", 9], sense: "<=", rhs: 0 },
      { coefficients: ["1/2", -90, "-1/50", 3], sense: "<=", rhs: 0 },
      { coefficients: [0, 0, 1, 0], sense: "<=", rhs: 1 },
    ],
  });
  assert.equal(result.status, "optimal");
  assert.equal(result.objectiveValue!.toString(), "1/20");
  assert.deepEqual(result.primal!.map(String), ["1/25", "0", "1", "0"]);
});
