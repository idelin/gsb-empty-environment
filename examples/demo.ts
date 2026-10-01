import {
  approximateInterval,
  bestApprox,
  bestApproxExhaustive,
  Budget,
  formatDecimal,
  intervalFromRoundedDecimal,
  makeInterval,
  makeRational,
  parseDecimal,
  rationalToString,
  type ApproxResult,
  type IntervalResult,
  type Rational,
} from "../src/index.ts";

const PI = parseDecimal("3.14159265358979323846264338327950288"); // exact decimal, no Number

function showApprox(label: string, r: ApproxResult): void {
  if (r.status === "budget-exhausted") {
    console.log(
      `  ${label}: BUDGET EXHAUSTED (last convergent: ${
        r.partial.lastConvergent ? rationalToString(r.partial.lastConvergent) : "none"
      })`,
    );
    return;
  }
  const tied =
    r.tied.length > 1 ? `  tied=[${r.tied.map(rationalToString).join(", ")}]` : "";
  console.log(
    `  ${label}: ${rationalToString(r.value)}  error=${rationalToString(r.error)}` +
      ` ≈ ${formatDecimal(r.error, 12, "half-up")}${r.exact ? "  (exact)" : ""}${tied}`,
  );
}

function showInterval(label: string, r: IntervalResult): void {
  if (r.status === "budget-exhausted") {
    console.log(`  ${label}: BUDGET EXHAUSTED at ${r.stage}`);
    return;
  }
  if (r.status === "decided") {
    console.log(
      `  ${label}: DECIDED ${rationalToString(r.value)}` +
        `  worstError=${rationalToString(r.worstError)} ≈ ${formatDecimal(r.worstError, 12)}`,
    );
    return;
  }
  console.log(`  ${label}: UNDECIDED (${r.reason.join("; ")})`);
  for (const e of r.evidence) {
    console.log(
      `    candidate ${rationalToString(e.candidate)}` +
        `  err@lo=${formatDecimal(e.errorAtLo, 8)} err@hi=${formatDecimal(e.errorAtHi, 8)}` +
        `  optimalAtLo=${e.optimalAtLo} optimalAtHi=${e.optimalAtHi}`,
    );
  }
  if (r.boundaries.length > 0) {
    console.log(`    decision boundaries inside interval: ${r.boundaries.map(rationalToString).join(", ")}`);
  }
}

console.log("== 1. pi under growing denominator bounds (continued fractions) ==");
for (const n of [1n, 7n, 100n, 113n, 1_000_000n]) {
  showApprox(`maxDen=${n}`, bestApprox(PI, n));
}

console.log("\n== 2. huge bound 10^30 (semiconvergent jump, BigInt only) ==");
showApprox("maxDen=10^30", bestApprox(PI, 10n ** 30n));

console.log("\n== 3. negatives, integers, exact rationals ==");
showApprox("-pi, maxDen=100", bestApprox(parseDecimal("-3.14159265358979323846264338327950288"), 100n));
showApprox("355/113, maxDen=113", bestApprox(makeRational(355n, 113n), 113n));
showApprox("42, maxDen=1", bestApprox(makeRational(42n, 1n), 1n));

console.log("\n== 4. equidistant tie-breaking (smaller denominator, then smaller |p|) ==");
showApprox("1/2, maxDen=1", bestApprox(makeRational(1n, 2n), 1n));
showApprox("7/2, maxDen=1", bestApprox(makeRational(7n, 2n), 1n));
showApprox("-7/2, maxDen=1", bestApprox(makeRational(-7n, 2n), 1n));

console.log("\n== 5. interval target, refined by more digits (maxDen=7) ==");
showInterval("[3.1, 3.2]", approximateInterval(makeInterval(parseDecimal("3.1"), parseDecimal("3.2")), 7n));
showInterval('"3.1"  rounded', approximateInterval(intervalFromRoundedDecimal("3.1"), 7n));
showInterval('"3.14" rounded', approximateInterval(intervalFromRoundedDecimal("3.14"), 7n));
showInterval('"3.1416" rounded', approximateInterval(intervalFromRoundedDecimal("3.1416"), 7n));

console.log("\n== 6. budget interruption ==");
const tight = bestApprox(PI, 10n ** 30n, new Budget(3));
showApprox("pi, maxDen=10^30, budget=3", tight);
const exh = bestApproxExhaustive(PI, 1_000_000n, new Budget(50_000));
if (exh.status === "budget-exhausted") {
  console.log(
    `  exhaustive pi, maxDen=10^6, budget=50000: BUDGET EXHAUSTED` +
      ` after ${exh.partial.checkedDenominators} denominators,` +
      ` best so far ${exh.partial.bestSoFar ? rationalToString(exh.partial.bestSoFar) : "none"}`,
  );
}

console.log("\n== 7. explicit display rounding of 1/8 at 2 places ==");
const eighth: Rational = makeRational(1n, 8n);
for (const mode of ["half-up", "half-even", "truncate"] as const) {
  console.log(`  ${mode}: ${formatDecimal(eighth, 2, mode)}`);
}
