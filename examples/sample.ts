/**
 * Runnable demo: exact rational simplex on the acceptance problem, plus the
 * unbounded and infeasible certificate paths. Run with: npm run sample
 */
import { Fraction } from "../src/fraction.ts";
import { solveLP, type LPProblem } from "../src/simplex.ts";

function show(title: string): void {
  console.log(`\n=== ${title} ${"=".repeat(Math.max(0, 60 - title.length))}`);
}

function dot(a: readonly Fraction[], b: readonly Fraction[]): Fraction {
  let sum = Fraction.ZERO;
  for (let i = 0; i < a.length; i++) sum = sum.add(a[i].mul(b[i]));
  return sum;
}

// ---------------------------------------------------------------------------
// 1. Acceptance problem: 3 inequality constraints, exact optimum 17/4.
//
//    max  3 x1 + 4 x2
//    s.t. 2 x1 +   x2 <= 2
//           x1 + 3 x2 <= 9/4
//           x1 +   x2 <= 2
//           x1, x2 >= 0
// ---------------------------------------------------------------------------
const problem: LPProblem = {
  objective: "max",
  coefficients: [3, 4],
  constraints: [
    { coefficients: [2, 1], sense: "<=", rhs: 2 },
    { coefficients: [1, 3], sense: "<=", rhs: "9/4" },
    { coefficients: [1, 1], sense: "<=", rhs: 2 },
  ],
};

show("1. Maximize with 3 inequality constraints (exact rational simplex)");
const result = solveLP(problem);
console.log(`status          : ${result.status}`);
console.log(`pivot iterations: ${result.iterations}`);
console.log(`optimal x       : (${result.primal!.map(String).join(", ")})`);
console.log(`maximum         : ${result.objectiveValue}  (exactly 17/4)`);
console.log(`dual prices y   : (${result.duals!.map(String).join(", ")})`);

// Dual price proof of optimality (strong duality).
const A = problem.constraints.map((c) => c.coefficients.map(Fraction.from));
const b = problem.constraints.map((c) => Fraction.from(c.rhs));
const c = problem.coefficients.map(Fraction.from);
const y = result.duals!;
console.log("\ndual price proof:");
for (let j = 0; j < c.length; j++) {
  let lhs = Fraction.ZERO;
  for (let i = 0; i < y.length; i++) lhs = lhs.add(y[i].mul(A[i][j]));
  console.log(`  (y^T A)_${j + 1} = ${lhs} >= c_${j + 1} = ${c[j]}  (dual feasible)`);
}
console.log(`  y >= 0 componentwise: ${y.every((v) => !v.isNegative())}`);
console.log(`  y^T b = ${dot(y, b)} = optimal value  (strong duality => x is optimal)`);

// ---------------------------------------------------------------------------
// 2. Unbounded LP: certificate ray.
// ---------------------------------------------------------------------------
show("2. Unbounded problem (ray certificate)");
const unbounded = solveLP({
  objective: "max",
  coefficients: [1, 0],
  constraints: [{ coefficients: [1, -1], sense: "<=", rhs: 1 }],
});
console.log(`status: ${unbounded.status}`);
console.log(`feasible point: (${unbounded.unboundedPoint!.map(String).join(", ")})`);
console.log(`ray direction : (${unbounded.unboundedRay!.map(String).join(", ")})`);
console.log("x(t) = point + t * ray stays feasible, objective -> +infinity");

// ---------------------------------------------------------------------------
// 3. Infeasible LP: Farkas certificate.
// ---------------------------------------------------------------------------
show("3. Infeasible problem (Farkas certificate)");
const infeasible = solveLP({
  objective: "max",
  coefficients: [1, 1],
  constraints: [
    { coefficients: [1, 1], sense: "<=", rhs: 1 },
    { coefficients: [1, 1], sense: ">=", rhs: 3 },
  ],
});
console.log(`status: ${infeasible.status}`);
const f = infeasible.farkasCertificate!;
console.log(`Farkas y: (${f.map(String).join(", ")})`);
console.log(
  `y^T b = ${dot(f, [Fraction.from(1), Fraction.from(3)])} < 0 while y^T A >= 0  => no feasible x`,
);
