import { failingInput, policyA, policyB, schema } from './fixture.ts';
import { Reducer, type ReductionResult } from './reducer.ts';
import { validate } from './constraints.ts';
import { Oracle } from './oracle.ts';

const JOURNAL = 'artifacts/demo-journal.json';

function printEvidence(r: ReductionResult): void {
  console.log('\nstep  phase         outcome                    op                                      budget');
  console.log('----  ------------  -------------------------  --------------------------------------  ----------');
  for (const e of r.evidence) {
    const verdicts = e.verdicts ? ` [${e.verdicts.a},${e.verdicts.b}]` : '';
    const op = (e.op + verdicts).slice(0, 38).padEnd(38);
    console.log(
      `${String(e.step).padStart(4)}  ${e.phase.padEnd(12)}  ${e.outcome.padEnd(25)}  ${op}  ${e.budget.spent}/${e.budget.max}`,
    );
  }
}

const oracleProbe = new Oracle(policyA, policyB);
console.log('== judgment-diff reducer demo ==');
console.log(`rules: A=${policyA.version} B=${policyB.version}`);
console.log('initial input:', JSON.stringify(failingInput));
console.log('initial verdicts:', JSON.stringify(oracleProbe.evaluate(failingInput)));

const result = new Reducer({ schema, policyA, policyB, budget: 200, journalPath: JOURNAL }).reduce(
  failingInput,
);

printEvidence(result);

console.log('\n== result ==');
console.log('status:            ', result.status);
console.log('budget spent:      ', `${result.budget.spent}/${result.budget.max}`);
console.log('reduced candidate: ', JSON.stringify(result.candidate));
console.log('valid per schema:  ', validate(schema, result.candidate).ok);
console.log(
  'verdicts preserved:',
  JSON.stringify(oracleProbe.evaluate(result.candidate)),
  `(target ${JSON.stringify({ a: true, b: false })})`,
);

if (result.certificate) {
  console.log(`\n== 1-minimal certificate (${result.certificate.length} basic ops, all rejected) ==`);
  for (const c of result.certificate) console.log(`  - ${c.op}  ->  ${c.outcome}`);
}

// Idempotency: reducing the reduced output must be a fixed point.
const second = new Reducer({ schema, policyA, policyB, budget: 200 }).reduce(result.candidate);
const idempotent = JSON.stringify(second.candidate) === JSON.stringify(result.candidate);
console.log('\nidempotent re-run: ', idempotent, `(status ${second.status})`);
console.log('journal written to:', JOURNAL);
