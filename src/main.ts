import { runScenario } from './simulator';

const results = [
  runScenario({
    name: 'Scenario 1: baseline migration (reordered + duplicated deltas)',
    seed: 1,
    keySpace: 20,
    prefill: 30,
    clientOps: 60,
    postSwitchOps: 10,
    deleteProb: 0.3,
    retryProb: 0.3,
    baseChunkSize: 3,
    verbose: true,
  }),
  runScenario({
    name: 'Scenario 2: delete-resurrection stress (hot keys, heavy deletes)',
    seed: 7,
    keySpace: 5,
    prefill: 20,
    clientOps: 120,
    postSwitchOps: 10,
    deleteProb: 0.5,
    retryProb: 0.3,
    baseChunkSize: 2,
    verbose: false,
  }),
  runScenario({
    name: 'Scenario 3: fence edge (in-flight writes at fence, post-fence retries)',
    seed: 42,
    keySpace: 10,
    prefill: 10,
    clientOps: 80,
    postSwitchOps: 10,
    deleteProb: 0.3,
    retryProb: 0.6,
    baseChunkSize: 4,
    verbose: false,
  }),
];

console.log('\n================ SUMMARY ================');
let allPassed = true;
for (const r of results) {
  allPassed = allPassed && r.passed;
  console.log(
    `${r.passed ? 'PASS' : 'FAIL'}  ${r.name}  ` +
      `(acked=${r.acked}, fenced-rejects=${r.rejected}, cached-replies=${r.cachedReplies}, dup-msgs=${r.duplicatedMsgs})`,
  );
}
if (results[2].rejected === 0 || results[2].cachedReplies === 0) {
  console.log('WARN: scenario 3 did not exercise fence rejection / cached replies');
  allPassed = false;
}
process.exit(allPassed ? 0 : 1);
