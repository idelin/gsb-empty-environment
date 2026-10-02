/**
 * Runnable samples: node examples/demo.ts
 */
import { bestApprox, bestApproxExhaustive } from "../src/bestApprox.ts";
import { bestApproxInterval } from "../src/interval.ts";
import { parseRational as P, toDecimal, toString } from "../src/rational.ts";

const line = (s = "") => console.log(s);

line("=== 1. Point targets: pi at escalating denominator bounds ===");
const pi = P("3.14159265358979323846264338327950288419716939937510");
for (const q of [1n, 7n, 100n, 113n, 1000n, 33102n, 10n ** 6n, 10n ** 30n]) {
  const { best, error } = bestApprox(pi, q);
  line(`  Q=${q.toString().padStart(31)}  ${toString(best).padEnd(46)} err=${toDecimal(error, 30)}`);
}

line();
line("=== 2. Negatives, integers, exact rationals, ties ===");
for (const [s, q] of [["-3.14159265358979", 100n], ["5", 1n], ["-22/7", 7n], ["0.5", 1n], ["-0.5", 1n], ["2.5", 1n]] as const) {
  const { best, error } = bestApprox(P(s), q);
  line(`  x=${s.padEnd(20)} Q=${q}  ->  ${toString(best)}  (err ${toDecimal(error, 12)})`);
}
line("  (exact halves round toward -infinity: 0.5->0, -0.5->-1, 2.5->2)");

line();
line("=== 3. Cross-check: fast CF algorithm vs exhaustive scan ===");
{
  const x = P("-2.718281828459045");
  const fast = bestApprox(x, 50n);
  const slow = bestApproxExhaustive(x, 50n);
  line(`  x=${toDecimal(x, 15)} Q=50`);
  line(`  continued fractions: ${toString(fast.best)} err=${toDecimal(fast.error, 15)}`);
  line(`  exhaustive scan    : ${toString(slow.best)} err=${toDecimal(slow.error, 15)}  (match: ${toString(fast.best) === toString(slow.best)})`);
}

line();
line("=== 4. Interval targets with refinable precision (Q = 113) ===");
for (const [lo, hi] of [
  ["3.1", "3.2"],
  ["3.14", "3.15"],
  ["3.1415", "3.1416"],
  ["3.14158", "3.1416"],
] as const) {
  const r = bestApproxInterval({ lower: P(lo), upper: P(hi) }, 113n);
  if (r.status === "decided") {
    line(`  [${lo}, ${hi}]  DECIDED  ${toString(r.best)}  worstErr=${toDecimal(r.worstError, 12)}`);
  } else {
    const all = r.candidates.map((c) => toString(c.candidate));
    const shown = all.length > 8 ? all.slice(0, 4).join(", ") + ", ... (" + all.length + " total)" : all.join(", ");
    line(`  [${lo}, ${hi}]  ${r.status.toUpperCase()}  candidates: ${shown}`);
  }
}

line();
line("=== 5. Budget interruption on an astronomically large live set ===");
{
  const r = bestApproxInterval({ lower: P("3.1415926"), upper: P("3.1415927") }, 10n ** 40n, { budget: 5 });
  line(`  status=${r.status}  (${r.status === "exhausted" ? r.evidence : ""})`);
  // A 2e-50-wide interval around 22/7: any other p/q with q <= 1e40 is at
  // least 1/(7q) >= 1.4e-41 away, so 22/7 provably wins everywhere inside.
  const c = P("22/7");
  const eps = P("0." + "0".repeat(49) + "1");
  const tight = bestApproxInterval(
    { lower: { num: c.num * eps.den - eps.num * c.den, den: c.den * eps.den }, upper: { num: c.num * eps.den + eps.num * c.den, den: c.den * eps.den } },
    10n ** 40n,
  );
  line(`  22/7 +/- 1e-50, Q=1e40: status=${tight.status}${tight.status === "decided" ? "  best=" + toString(tight.best) + "  worstErr=" + toDecimal(tight.worstError, 52) : ""}`);
}
