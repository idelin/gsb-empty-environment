import { parse } from "./parser.ts";
import { check, formatMinimalErrors, formatTree, type Env } from "./checker.ts";
import { formatUnit, unitOf } from "./units.ts";

const env: Env = new Map([
  ["v", unitOf({ L: 1, T: -1 })],   // velocity
  ["t", unitOf({ T: 1 })],          // time
  ["d", unitOf({ L: 1 })],          // distance
  ["m", unitOf({ M: 1 })],          // mass
  ["a", unitOf({ L: 1, T: -2 })],   // acceleration
]);

const formulas = [
  "v * t + d",          // homogeneous: L
  "v * t + m",          // mismatch L vs M
  "(v / a)^2 * m",      // parentheses + power: T^2 * M
  "d / d",              // dimensionless
  "m * a / d^2",        // pressure-ish: M/(L*T^2)
  "v + 0",              // zero literal is dimensionless -> mismatch
  "1 / t^-1",           // negative power: T
  "(v + m) * a / d",    // minimal error is the inner (v + m)
];

for (const src of process.argv.slice(2).length > 0 ? process.argv.slice(2) : formulas) {
  console.log(`=== ${src}`);
  const result = check(parse(src), env);
  console.log(formatTree(result.tree));
  if (result.unit !== null) {
    console.log(`=> unit: ${formatUnit(result.unit)}`);
  } else {
    console.log(`minimal error:\n${formatMinimalErrors(src, result)}`);
  }
  console.log();
}
