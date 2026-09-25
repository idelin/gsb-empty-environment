/**
 * Runnable sample:
 *   1. full reduction with a generous budget (expects a 1-minimal certificate);
 *   2. the same reduction with a tiny budget (must claim only a local minimum);
 *   3. resume from the starved journal with more budget (recovers the candidate).
 */
import { rmSync } from 'node:fs';
import { Reducer } from '../src/reducer.ts';
import { differentialOracle, judge } from '../src/policy.ts';
import { accessSchema, policyV1, policyV2, failingInput } from './scenario.ts';

const oracle = differentialOracle(policyV1, policyV2);
const ruleVersion = `${policyV1.version}+${policyV2.version}`;

function show(title: string, value: unknown): void {
  console.log(`\n=== ${title} ===`);
  console.log(typeof value === 'string' ? value : JSON.stringify(value, null, 2));
}

rmSync('/tmp/reducer-sample', { recursive: true, force: true });

console.log(`failing input: ${JSON.stringify(failingInput)}`);
console.log(`v1 judges: ${judge(policyV1, failingInput)}, v2 judges: ${judge(policyV2, failingInput)}`);

// --- run 1: generous budget ---------------------------------------------------
const full = new Reducer({
  schema: accessSchema, oracle, ruleVersion,
  budget: 500, journalPath: '/tmp/reducer-sample/full.jsonl', runId: 'full',
}).reduce(failingInput);

for (const [i, s] of full.steps.entries()) {
  console.log(
    `step ${i + 1} [${s.op}] ${s.detail}\n` +
    `  before: ${JSON.stringify(s.before)}\n` +
    `  after : ${JSON.stringify(s.after)}\n` +
    `  budget: used=${s.oracleUsed} left=${s.budgetLeft}`,
  );
}
show('reduced candidate', full.candidate);
console.log(`status: ${full.status} (${full.reason})`);
console.log(`oracle calls: ${full.oracleCalls}/${full.budget}`);
show('1-minimal certificate', full.certificate);

// --- run 2: tiny budget ---------------------------------------------------------
const starved = new Reducer({
  schema: accessSchema, oracle, ruleVersion,
  budget: 6, journalPath: '/tmp/reducer-sample/starved.jsonl', runId: 'starved',
}).reduce(failingInput);
show('starved-budget candidate', starved.candidate);
console.log(`status: ${starved.status} (${starved.reason})`);
console.log(`oracle calls: ${starved.oracleCalls}/${starved.budget}`);

// --- run 3: resume the starved run with a bigger budget -------------------------
const resumed = new Reducer({
  schema: accessSchema, oracle, ruleVersion,
  budget: 500, journalPath: '/tmp/reducer-sample/starved.jsonl', runId: 'resumed', resume: true,
}).reduce(failingInput);
show('resumed candidate (checkpoint recovered, not lost)', resumed.candidate);
console.log(`status: ${resumed.status} (${resumed.reason})`);
console.log(`oracle calls (incl. previous run): ${resumed.oracleCalls}/${resumed.budget}`);
