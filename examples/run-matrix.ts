/**
 * Executable entry point: run the full normative scenario matrix and print
 * a compact report. Exits non-zero if any cell regresses.
 */

import { matrixSummary, runScenarioMatrix } from '../src/index.ts';

const results = runScenarioMatrix();
const summary = matrixSummary(results);

for (const result of results) {
  const status = result.pass ? 'PASS' : 'FAIL';
  console.log(`${status}  ${result.id.padEnd(12)} ${result.scenario}`);
}

console.log('\nBy category:');
for (const [category, bucket] of Object.entries(summary.byCategory)) {
  console.log(`  ${category.padEnd(24)} ${bucket.passed}/${bucket.total}`);
}

console.log(
  `\nTotal ${summary.total}, passed ${summary.passed}, failed ${summary.failed.length}`,
);

if (summary.failed.length > 0) {
  process.exitCode = 1;
}
