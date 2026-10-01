/**
 * Runnable samples:  node examples/demo.ts   (or: npm run demo)
 */
import { isqrtRem } from "../src/integer-roots.ts";
import { rootFixed, sqrtFixed } from "../src/fixed-root.ts";
import type { FixedRootResult } from "../src/fixed-root.ts";

function fmt(v: bigint, scale: number): string {
  const s = v.toString().padStart(scale + 1, "0");
  if (scale === 0) return s;
  return `${s.slice(0, -scale)}.${s.slice(-scale)}`;
}

function show(label: string, res: FixedRootResult): void {
  console.log(`${label}`);
  console.log(
    `  rounded   = ${fmt(res.rounded, res.outScale)}  (${res.mode}, ulp err <= ${res.ulpErrorBound})`,
  );
  console.log(
    `  bracket   = [${fmt(res.floor, res.outScale)}, ${fmt(res.ceil, res.outScale)}]  exact=${res.exact}`,
  );
  console.log(
    `  remainder = ${res.remainder === 0n ? "0" : `${res.remainder.toString().slice(0, 24)}${res.remainder.toString().length > 24 ? "..." : ""}`}  newton iterations=${res.iterations}`,
  );
}

console.log("== sqrt(2), 40 decimal places, round-half-even ==");
show("sqrt(2) @ 40", sqrtFixed(2n, 0, 40, "halfEven"));

console.log("\n== exact midpoint: sqrt(0.25) = 0.5 at 0 places ==");
show("halfEven (0.5 -> 0)", sqrtFixed(25n, 2, 0, "halfEven"));
show("halfUp   (0.5 -> 1)", sqrtFixed(25n, 2, 0, "halfUp"));

console.log("\n== cube root of 2, 20 places ==");
show("cbrt(2) @ 20", rootFixed(2n, 0, 20, 3, "halfEven"));

console.log("\n== tiny input: sqrt(1e-100) at 50 places (no collapse to 0) ==");
show("sqrt(1e-100) @ 50", sqrtFixed(1n, 100, 50, "halfEven"));

console.log("\n== perfect square: sqrt(123456789^2) at 10 places ==");
show("sqrt(123456789^2) @ 10", sqrtFixed(123456789n ** 2n, 0, 10, "halfEven"));

console.log("\n== big: sqrt(2^1000) at 0 places ==");
show("sqrt(2^1000) @ 0", sqrtFixed(1n << 1000n, 0, 0, "halfEven"));

console.log("\n== integer square root with remainder ==");
const { root, remainder, iterations } = isqrtRem(10n ** 30n + 12345n);
console.log(`isqrt(10^30 + 12345) = ${root}`);
console.log(`remainder = ${remainder} (iterations=${iterations})`);
console.log(`check: root^2 + remainder == input -> ${root * root + remainder === 10n ** 30n + 12345n}`);

console.log("\n== negative input is rejected ==");
try {
  sqrtFixed(-4n, 0, 2);
} catch (err) {
  console.log(`  sqrtFixed(-4) threw: ${(err as Error).message}`);
}
