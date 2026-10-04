// ---------------------------------------------------------------------------
// 入口：对每个用例打印原树、优化树（带来源对应），并做穷举小环境验证。
// ---------------------------------------------------------------------------

import { formatTree, parse } from './ast.ts';
import { CASES } from './cases.ts';
import { srcOf, type OptNode } from './fold.ts';
import { formatOutcome, verifyCase } from './verify.ts';
import { fuzz } from './fuzz.ts';

let failed = 0;

for (const c of CASES) {
  const original = parse(c.src);
  const report = verifyCase(c.name, c.src, original);

  console.log('='.repeat(72));
  console.log(`CASE ${c.name}: ${c.src}`);
  console.log('-- original --------------------------------------------------');
  console.log(formatTree(original));
  console.log('-- optimized (with source map) -------------------------------');
  console.log(formatTree(report.optimized, (n) => srcOf(n as OptNode)));
  console.log(`-- exhaustive check: ${report.envCount} envs, ` +
    `${report.mismatches.length === 0 ? 'ALL MATCH' : `${report.mismatches.length} MISMATCHES`}`);

  for (const m of report.mismatches.slice(0, 3)) {
    console.log(`  MISMATCH env=${JSON.stringify(m.env)}`);
    console.log(`    original : ${formatOutcome(m.original)}`);
    console.log(`    optimized: ${formatOutcome(m.optimized)}`);
  }
  if (report.mismatches.length > 0) failed++;
}

console.log('='.repeat(72));
const FUZZ_COUNT = 3000;
const fuzzFailures = fuzz(FUZZ_COUNT, 0xc0ffee, 4);
console.log(`fuzz: ${FUZZ_COUNT} random exprs x exhaustive envs, ${fuzzFailures} mismatch(es)`);

console.log(failed === 0 && fuzzFailures === 0
  ? `OK: all ${CASES.length} cases + fuzz preserve value / first error / access sequence.`
  : `FAILED: ${failed} case(s), ${fuzzFailures} fuzz mismatch(es).`);
process.exit(failed === 0 && fuzzFailures === 0 ? 0 : 1);
