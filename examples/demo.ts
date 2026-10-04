/**
 * Demo: stitch a 6x4 overlap where the left side favours image A and the
 * right side favours image B, with the boundary columns pinned. Run with:
 *   node examples/demo.ts        (Node >= 22.6)
 */
import { solveSeam } from '../src/seamSolver.ts';
import { verifyOptimality } from '../src/bruteForce.ts';

const width = 6;
const height = 4;

// Source costs: A is cheap on the left, B is cheap on the right,
// with a noisy band in the middle.
const costA = [
  [1, 1, 4, 8, 9, 9],
  [1, 2, 9, 3, 9, 9],
  [1, 1, 5, 8, 8, 9],
  [2, 1, 3, 9, 9, 8],
];
const costB = costA.map((row) => [...row].reverse());

const problem = {
  width,
  height,
  costA,
  costB,
  switchCost: 3,
  fixed: costA.map((row, y) =>
    row.map((_, x) => (x === 0 ? ('A' as const) : x === width - 1 ? ('B' as const) : null)),
  ),
};

const { result, bruteForce, optimal } = verifyOptimality(problem);

console.log('segmentation (A = left image, B = right image):');
for (const row of result.labels) console.log('  ' + row.join(' '));
console.log('\ncost breakdown:');
console.log(`  source cost (A): ${result.costs.sourceCostA}`);
console.log(`  source cost (B): ${result.costs.sourceCostB}`);
console.log(`  switch cost:     ${result.costs.switchCost} (${result.costs.switchCount} switches)`);
console.log(`  total:           ${result.costs.total}`);
console.log(`\nbrute-force optimum: ${bruteForce.minCost} (optimal labellings: ${bruteForce.optimalCount})`);
console.log(`solver matches exhaustive search: ${optimal}`);
