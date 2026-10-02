/**
 * Demo: exact extremum location for rational Bezier curves.
 * Run: npm run sample
 */
import { Fraction } from "./fraction.ts";
import { analyze } from "./bezier.ts";
import { polyToString } from "./polynomial.ts";

const F = (s: string | number) => Fraction.from(s);

function show(title: string, ys: Fraction[], ws: Fraction[]): void {
  console.log("=".repeat(72));
  console.log(title);
  console.log("  control points y = [" + ys.map(String).join(", ") + "]");
  console.log("  weights        w = [" + ws.map(String).join(", ") + "]");
  const a = analyze(ys, ws);
  console.log(`  N(t) = ${polyToString(a.numerator)}`);
  console.log(`  D(t) = ${polyToString(a.denominator)}`);
  console.log(`  P(t) = N'D - ND' = ${a.derivativePolynomialString}`);
  if (a.constant) {
    console.log("  curve is constant on [0,1]");
    return;
  }
  console.log("  monotonicity:");
  for (const m of a.monotonicity) {
    console.log(`    [${m.from} , ${m.to}]  ${m.direction}`);
  }
  console.log("  critical points:");
  if (a.criticalPoints.length === 0) console.log("    (none in [0,1])");
  for (const c of a.criticalPoints) {
    const tStr = c.t !== null
      ? `t = ${c.t} (exact)`
      : `t in (${c.interval!.lo} , ${c.interval!.hi})  ~= ${c.tApprox}`;
    const vStr = c.value !== null
      ? `y = ${c.value} (exact)`
      : `y ~= ${c.valueApprox}`;
    console.log(
      `    ${c.kind.padEnd(10)} ${tStr}   ${vStr}` +
      `   [multiplicity ${c.multiplicity}, sign change: ${c.signChange}]`,
    );
  }
  const fmtG = (g: { t: string; value: Fraction | null; valueApprox: number } | null) =>
    g === null ? "n/a"
    : `t = ${g.t},  y = ${g.value !== null ? `${g.value} (exact)` : `~${g.valueApprox}`}`;
  console.log(`  global max: ${fmtG(a.globalMax)}`);
  console.log(`  global min: ${fmtG(a.globalMin)}`);
}

// 1. quadratic rational Bezier: exact rational extremum
show(
  "Example 1: quadratic rational Bezier, weights (1, 2, 1)",
  [F(0), F(1), F(0)],
  [F(1), F(2), F(1)],
);

// 2. asymmetric weights: irrational critical point, certified interval
show(
  "Example 2: quadratic rational Bezier, weights (1, 2, 3) -> irrational root",
  [F(0), F(1), F(0)],
  [F(1), F(2), F(3)],
);

// 3. rational control points with fractions
show(
  "Example 3: fractional control points (1/2, -3/4, 2), weights (2, 1/2, 3)",
  [F("1/2"), F("-3/4"), F(2)],
  [F(2), F("1/2"), F(3)],
);

// 4. cubic with a double (non-sign-changing) stationary point: y = (2t-1)^3
show(
  "Example 4: cubic Bezier = (2t-1)^3, double stationary point at t = 1/2",
  [F(-1), F(1), F(-1), F(1)],
  [F(1), F(1), F(1), F(1)],
);

// 5. derivative roots exactly on the boundary: y = 3t^2 - 2t^3
show(
  "Example 5: cubic Bezier = 3t^2 - 2t^3, stationary points at endpoints",
  [F(0), F(0), F(1), F(1)],
  [F(1), F(1), F(1), F(1)],
);
