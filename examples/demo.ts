/**
 * Run with:  node examples/demo.ts
 * Everything below is exact: BigInt arithmetic, no Number-mediated rounding.
 */

import {
  add,
  cmp,
  div,
  frac,
  mul,
  roundToDecimal,
  sub,
  toFractionString,
} from "../src/fraction.ts";
import {
  expand,
  expandText,
  format,
  locateCycle,
  pageDigits,
  parseExact,
} from "../src/decimal.ts";
import { RationalDAG } from "../src/expr.ts";

function heading(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function formatPair(f: { n: bigint; d: bigint }): string {
  return `${f.n.toString()}/${f.d.toString()}`;
}

heading("1. Canonical fractions (sign on numerator, 0 is 0/1)");
for (const f of [frac(2, 4), frac(1, -6), frac(-3, -9), frac(0, -7)]) {
  console.log(formatPair(f));
}

heading("2. Exact four operations (1/6 and 1/7)");
const sixth = frac(1n, 6n);
const seventh = frac(1n, 7n);
console.log("1/6 + 1/7 =", toFractionString(add(sixth, seventh)));
console.log("1/6 - 1/7 =", toFractionString(sub(sixth, seventh)));
console.log("1/6 * 1/7 =", toFractionString(mul(sixth, seventh)));
console.log("1/6 / 1/7 =", toFractionString(div(sixth, seventh)));

heading("3. Safe comparison via cross multiplication (100-digit integers)");
const bigA = frac(10n ** 100n + 3n, 10n ** 100n + 1n);
const bigB = frac(10n ** 100n + 2n, 10n ** 100n + 1n);
console.log("cmp(A, B) =", cmp(bigA, bigB), "(no decimal string expanded)");

heading("4. Repeating decimals: prefix and period");
for (const [n, d] of [[1n, 6n], [1n, 7n], [1n, 28n], [1n, 202n], [-1n, 6n], [1n, 2n]] as const) {
  const e = expand(frac(n, d));
  console.log(
    `${n.toString()}/${d.toString()} = ${format(e)}`.padEnd(18),
    `kind=${e.kind}`,
    `prefixLen=${e.preperiodLength}`,
    `periodLen=${e.periodLength}`,
  );
}

heading("5. Cycle located with a bounded state table (no unbounded storage)");
console.log("locateCycle(1/7) =", locateCycle(frac(1n, 7n)));
const tight = expand(frac(1n, 100003n), { maxDigits: 12 });
console.log("1/100003 within 12 digits =>", format(tight), "kind =", tight.kind);

heading("6. Pagination of fractional digits (deep pages use index arithmetic)");
console.log("1/7 digits 0..5  =", pageDigits(frac(1n, 7n), 0, 6).digits);
console.log("1/7 digits 6..11 =", pageDigits(frac(1n, 7n), 6, 6).digits);
console.log("1/7 at offset 1,000,000 (7 digits) =", pageDigits(frac(1n, 7n), 1_000_000, 7).digits);
console.log("1/8 after end pads zeros =", pageDigits(frac(1n, 8n), 0, 8).digits);

heading("7. Exact parse round-trip (truncated '...' is rejected)");
for (const text of ["0.1(6)", "0.(142857)", "0.03(571428)", "-2.5", "1.(9)"]) {
  console.log(`${text.padEnd(12)} -> ${toFractionString(parseExact(text))}`);
}
try {
  parseExact("0.333...");
} catch (err) {
  console.log("parse 0.333... rejected:", (err as Error).message);
}

heading("8. Explicit rounding rules (result stays an exact Fraction)");
const third = frac(1n, 3n);
for (const mode of ["trunc", "floor", "ceil", "half-up"] as const) {
  console.log(`1/3 @ 2 digits [${mode}] = ${toFractionString(roundToDecimal(third, 2, mode))}`);
}

heading("9. Lazy-reduction DAG with a bit-length budget");
const dag = new RationalDAG();
const a = dag.frac(frac(1n, 6n));
const b = dag.frac(frac(1n, 7n));
const sum = dag.add(a, b);
const shared = dag.mul(sum, sum);
console.log("(1/6+1/7)^2 =", toFractionString(dag.evaluate(shared).value!));
console.log("compare(1/6, 1/7) on DAG =", dag.compare(a, b).result);

const bigNode = dag.leaf(2n ** 200n);
console.log("2^200 under 64-bit budget:", dag.evaluate(bigNode, 64).status, "(over budget -> incomplete)");
console.log("2^200 under 1000-bit budget:", dag.evaluate(bigNode, 1000).status);

let product = dag.leaf(2n ** 30n);
for (let i = 0; i < 6; i += 1) product = dag.mul(product, dag.leaf(2n ** 30n));
const grown = dag.evaluate(product, 128);
console.log("a growing product chain @128 bits =>", grown.status, "peakBits =", grown.peakBits);

heading("10. expandText convenience");
console.log(expandText(frac(22n, 7n)));
console.log(expandText(frac(1n, 23n)));
