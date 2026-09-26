import { SchemaInference, validate, type InferenceReport } from '../src/index.ts';

function show(title: string): void {
  console.log('\n=== ' + title + ' ===');
}

function summarize(report: InferenceReport): void {
  console.log(`status: ${report.status}, retained samples: ${report.sampleCount} [${report.retainedSampleIds.join(', ')}]`);
  console.log('\ndecision points (ranked by sample support, then parsimony):');
  for (const dp of report.decisionPoints) {
    const path = dp.path === '' ? '(root)' : dp.path;
    console.log(`  ${path} [${dp.dimension}]`);
    for (const opt of dp.options) {
      const flag = opt.inductive ? 'inductive' : 'safe     ';
      console.log(
        `    #${opt.rank} ${opt.label}  support=${opt.support.consistent}/${opt.support.outOf}  ${flag}`,
      );
      if (opt.distinguisher) {
        console.log(
          `       distinguish from wider option with: ${JSON.stringify(opt.distinguisher.instance)} -> ${opt.distinguisher.note}`,
        );
      }
    }
  }
  console.log('\nunknowns:');
  for (const u of report.unknowns) console.log('  - ' + u);
  if (report.unknowns.length === 0) console.log('  (none)');
  console.log('\nprimary schema (composition of top-ranked options):');
  console.log(JSON.stringify(report.primary, null, 2));
  console.log('\nverification: ' + JSON.stringify({ accepted: report.verification.accepted, rejected: report.verification.rejected }));
  console.log('\nguarantees (implemented & checked only):');
  for (const g of report.guarantees) console.log('  - ' + g);
  console.log('\nsample-inductive (NOT universal guarantees):');
  for (const i of report.inductions) console.log('  - ' + i);
}

const inference = new SchemaInference();
inference.addSample('u1', { name: 'ada', age: 36, email: 'ada@x.dev', tags: ['admin', 'ops'] });
inference.addSample('u2', { name: 'grace', age: 41, tags: [] });
inference.addSample('u3', { name: 'linus', age: 55, email: null, tags: ['ops'] });

show('1. inference from 3 samples');
summarize(inference.report());

show('2. add outlier sample, then retract it: range extends and shrinks back');
inference.addSample('outlier', { name: 'x', age: 400, tags: ['a'] });
const withOutlier = inference.report();
const rangeWith = withOutlier.decisionPoints.find((d) => d.path === '/age' && d.dimension === 'range');
console.log('with outlier:    ' + rangeWith?.options[0].label + '  (' + rangeWith?.options[0].rationale + ')');
inference.retractSample('outlier');
const without = inference.report();
const rangeWithout = without.decisionPoints.find((d) => d.path === '/age' && d.dimension === 'range');
console.log('after retraction: ' + rangeWithout?.options[0].label + '  (' + rangeWithout?.options[0].rationale + ')');

show('3. validation errors carry instance path AND rule path');
const errors = validate({ name: 'n', age: 3.5, extra: 'never seen in samples' }, without.primary);
for (const e of errors) {
  console.log(`  keyword=${e.keyword}  instancePath=${e.instancePath}  rulePath=${e.rulePath}  message=${e.message}`);
}
console.log('note: the never-observed key "extra" is accepted (open-world, not permanently forbidden)');

show('4. empty sample set -> unknown, not a fabricated schema');
const empty = new SchemaInference();
const emptyReport = empty.report();
console.log(`status: ${emptyReport.status}`);
console.log('primary schema: ' + JSON.stringify(emptyReport.primary));
console.log('unknowns: ' + JSON.stringify(emptyReport.unknowns));
