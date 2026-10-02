/**
 * Demo: locate extrema of 1D rational Bezier curves exactly.
 * Run with: node examples/demo.ts   (Node >= 22.6, no build step)
 */
import { analyze, type AnalysisResult, type TRef } from "../src/index.ts";

function fmtT(t: TRef): string {
  if (t.exact) return `${t.exact} (exact)`;
  return `${t.approx}  in [${t.lo}, ${t.hi}]`;
}

function show(title: string, points: (number | string)[], weights: (number | string)[]): void {
  const r: AnalysisResult = analyze(points, weights);
  console.log("=".repeat(72));
  console.log(title);
  console.log(`  control points: [${points.join(", ")}]   weights: [${weights.join(", ")}]`);
  console.log(`  extrema equation: P(t) = ${r.derivativePolyString}   (integer coefficients)`);
  if (r.endpointRoots.length > 0) {
    console.log(`  derivative vanishes at endpoints: t = ${r.endpointRoots.join(", ")}`);
  }
  console.log("  monotone segments:");
  for (const s of r.segments) {
    console.log(`    t: ${s.from.approx} -> ${s.to.approx}   ${s.direction}`);
  }
  console.log("  interior critical points:");
  if (r.criticalPoints.length === 0) console.log("    (none)");
  for (const cp of r.criticalPoints) {
    console.log(`    ${cp.kind} at t = ${fmtT(cp.t)}`);
    console.log(`        value = ${cp.value}  certified in [${cp.valueLo}, ${cp.valueHi}]`);
  }
  console.log(
    `  GLOBAL max = ${r.globalMax.value} at t = ${r.globalMax.locations.map((t) => t.approx).join(", ")}`,
  );
  console.log(
    `  GLOBAL min = ${r.globalMin.value} at t = ${r.globalMin.locations.map((t) => t.approx).join(", ")}`,
  );
  console.log();
}

show("Example 1: symmetric weights -> exact rational extremum", [0, 1, 0], [1, 2, 1]);
show("Example 2: asymmetric weights -> irrational extremum, certified", [0, 1, 0], [1, 2, 3]);
show("Example 3: monotone curve, endpoints only", [0, 1, 2], [1, 1, 1]);
show("Example 4: cubic with two interior extrema", [0, 2, -1, 1], [1, 1, 1, 1]);
