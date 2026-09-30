// CLI: generate the finite trace suite, run every trace through the arbiter,
// and check all invariants independently. Exits non-zero on any failure.

import { readFileSync } from 'node:fs';
import { generateTraces } from './traceGen.ts';
import { checkTrace } from './invariants.ts';
import { makeArbiter, runTrace, type SceneFixture } from './harness.ts';
import { DEFAULT_THRESHOLDS } from './types.ts';

const fixture = JSON.parse(readFileSync(new URL('../fixtures/scene.json', import.meta.url), 'utf8')) as SceneFixture;

const traces = generateTraces(DEFAULT_THRESHOLDS);
let failed = 0;
for (const trace of traces) {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace);
  const failures = checkTrace(trace.name, trace.ops, run.steps);
  const commits = run.outputs.filter((e) => e.phase === 'commit').map((e) => e.kind);
  const status = failures.length === 0 ? 'ok  ' : 'FAIL';
  console.log(`${status} ${trace.name}  (commits: ${commits.join(', ') || 'none'})`);
  for (const f of failures) {
    failed++;
    console.log(`     [${f.rule}] step ${f.step}: ${f.detail}`);
  }
}
console.log(`\n${traces.length} traces, ${failed} invariant failures`);
process.exit(failed === 0 ? 0 : 1);
