/**
 * Runnable sample: node examples/demo.ts
 */

import {
  aggregate,
  applyCorrections,
  evaluate,
  formatDecimal,
  formatPercent,
  formatPp,
  kidneyStoneCase,
  mergeAggregates,
  metricRef,
  missingField,
  obs,
  ppChange,
  ppChangeNode,
  present,
  ratio,
  ratioOf,
  rational,
  relChange,
  relChangeNode,
  retractedField,
  simpsonAnalysis,
  source,
  sum,
  toExactString,
  verifyCorrections,
  verifyHierarchicalMerging,
  verifySliceMerging,
  type Observation,
  type RatioValue,
} from '../src/index.ts';

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function showRatio(r: RatioValue): string {
  switch (r.kind) {
    case 'value':
      return `value ${toExactString(r.value)} (${formatPercent(r.value, 2, 'half-even')})`;
    case 'zero-denominator':
      return `zero-denominator (numerator sum ${r.numerator})`;
    case 'missing':
      return `missing (numerator=${r.missingNumerator}, denominator=${r.missingDenominator})`;
  }
}

const metric = metricRef('conversion-rate', 'v2');

// 1. Aggregate raw quantities first: 1/2 merged with 9/10 is 10/12,
//    NOT the average of 50% and 90% (which would be 70%).
show('1. sum raw quantities, never average percentages');
const merged = aggregate([obs('r1', metric, 1n, 2n), obs('r2', metric, 9n, 10n)]);
console.log(`sums: ${merged.numeratorSum}/${merged.denominatorSum}`); // 10/12
const mergedRatio = ratioOf(merged);
if (mergedRatio.kind === 'value') {
  console.log(`ratio: ${toExactString(mergedRatio.value)} = ${formatPercent(mergedRatio.value, 2, 'half-even')}`);
}
console.log('naive average of 50% and 90% would be 70% -- deliberately not used');

// 2. Missing propagation, denominator retraction, zero denominator vs true zero.
show('2. missing / retracted / zero-denominator / true zero');
const withMissing: Observation[] = [
  obs('a', metric, 3n, 10n),
  { id: 'b', metric, numerator: missingField(), denominator: present(5n) },
];
console.log('missing numerator ->', showRatio(ratioOf(aggregate(withMissing))));
const withRetraction: Observation[] = [
  obs('a', metric, 3n, 10n),
  { id: 'b', metric, numerator: present(4n), denominator: retractedField() },
];
const retractedAgg = aggregate(withRetraction);
console.log(
  `retracted denominator excluded: sums ${retractedAgg.numeratorSum}/${retractedAgg.denominatorSum}, unpaired=${retractedAgg.unpaired}`,
);
console.log('zero denominator ->', showRatio(ratioOf(aggregate([obs('z', metric, 5n, 0n)]))));
console.log('true zero ratio ->', showRatio(ratioOf(aggregate([obs('t', metric, 0n, 7n)]))));

// 3. Percentage points vs relative percent; growth from a zero base.
show('3. pp change vs relative change');
const prev = ratioOf(aggregate([obs('p', metric, 1n, 2n)])); // 50%
const next = ratioOf(aggregate([obs('n', metric, 3n, 5n)])); // 60%
const pp = ppChange(next, prev);
const rel = relChange(next, prev);
if (pp.kind === 'defined') console.log(`pp change:    ${formatPp(pp.value, 2, 'half-even')} (unit: pp)`);
if (rel.kind === 'defined') console.log(`relative chg: ${formatDecimal(rel.value, 2, 'half-even')}% (unit: percent)`);
const zeroBase = ratioOf(aggregate([obs('z', metric, 0n, 10n)])); // true zero
console.log('relative change from old value 0 ->', JSON.stringify(relChange(next, zeroBase)));
const ppFromZero = ppChange(next, zeroBase);
if (ppFromZero.kind === 'defined') {
  console.log(`pp change from 0 is still defined: ${formatPp(ppFromZero.value, 2, 'half-even')}`);
}

// 4. Expression graph: multi-level aggregation with provenance.
show('4. expression graph: multi-level aggregation + provenance');
const jan = source('jan', [obs('j1', metric, 1n, 2n), obs('j2', metric, 9n, 10n)]);
const feb = source('feb', [
  obs('f1', metric, 4n, 5n),
  { id: 'f2', metric, numerator: missingField(), denominator: missingField() },
]);
const q1 = sum('q1', [jan, feb]);
const q1Ratio = ratio('q1-ratio', q1);
const q1Result = evaluate(q1Ratio);
if (q1Result.kind === 'ratio') {
  console.log('q1 ratio:', showRatio(q1Result.ratio));
  console.log('missing events:', JSON.stringify(q1Result.prov.missing));
  console.log(
    'contributions:',
    q1Result.prov.contributions
      .map((c) => `${c.observationId}:${c.numerator}/${c.denominator}(${c.status})`)
      .join(' '),
  );
}
const prevQ = ratio('prev-q', source('prev-q-src', [obs('p1', metric, 1n, 4n)]));
const changeGraph = evaluate(ppChangeNode('q1-vs-prev', q1Ratio, prevQ));
if (changeGraph.kind === 'change') {
  console.log('pp-change node (missing propagates):', JSON.stringify(changeGraph.change));
}
const relGraph = evaluate(relChangeNode('rel', ratio('r2', source('s2', [obs('x', metric, 3n, 4n)])), prevQ));
if (relGraph.kind === 'change' && relGraph.change.kind === 'defined') {
  console.log(`rel-change node: ${formatDecimal(relGraph.change.value, 2, 'half-even')}%`);
}

// 5. Simpson's paradox with group-contribution explanation.
show('5. Simpson reversal (kidney stones)');
const analysis = simpsonAnalysis(kidneyStoneCase());
for (const arm of analysis.arms) {
  console.log(
    `arm ${arm.arm}: overall ${formatPercent(arm.overall, 2, 'half-even')} ` +
      `(${arm.groups
        .map(
          (g) =>
            `${g.group} rate=${formatPercent(g.rate, 2, 'half-even')} weight=${formatPercent(g.weight, 2, 'half-even')} contrib=${formatPercent(g.contribution, 2, 'half-even')}`,
        )
        .join(' | ')})`,
  );
}
console.log('group winners:', JSON.stringify(analysis.groupWinners), 'overall winner:', analysis.overallWinner);
console.log('reversal:', analysis.reversal);
for (const line of analysis.narrative) console.log(`  ${line}`);

// 6. Reference verification: arbitrary slices, hierarchy, corrections.
show('6. verification against direct raw-record aggregation');
const records: Observation[] = [
  obs('r1', metric, 1n, 2n),
  obs('r2', metric, 9n, 10n),
  obs('r3', metric, 5n, 8n),
  { id: 'r4', metric, numerator: present(7n), denominator: retractedField() },
  obs('r5', metric, 2n, 3n),
  obs('r6', metric, 11n, 13n),
];
const slices = [records.slice(0, 2), records.slice(2, 4), records.slice(4)];
const sliceCheck = verifySliceMerging(slices);
console.log('slice merge == direct aggregate:', sliceCheck.ok);
const hierarchy = [[slices[0], slices[1]], [slices[2]]];
console.log('hierarchical merge == direct aggregate:', verifyHierarchicalMerging(hierarchy).ok);
const corrections = applyCorrections(records, [
  { type: 'retract-denominator', observationId: 'r2' },
  { type: 'replace', observation: obs('r3', metric, 6n, 8n) },
]);
console.log(
  'corrections (retract r2 denominator, replace r3) verified:',
  verifyCorrections(slices, [
    { type: 'retract-denominator', observationId: 'r2' },
    { type: 'replace', observation: obs('r3', metric, 6n, 8n) },
  ]).ok,
);
const correctedAgg = aggregate(corrections);
console.log(`corrected sums: ${correctedAgg.numeratorSum}/${correctedAgg.denominatorSum}`);

// 7. BigInt exactness beyond 2^53, explicit rounding.
show('7. BigInt exactness + explicit rounding');
const big = rational(9007199254740993n, 2n); // 4503599627370496.5, a tie at scale 0
console.log(`exact: ${toExactString(big)}`);
console.log(`half-up   0dp: ${formatDecimal(big, 0, 'half-up')}`);
console.log(`half-even 0dp: ${formatDecimal(big, 0, 'half-even')}`);
console.log(`floor     0dp: ${formatDecimal(big, 0, 'floor')}`);
console.log(`ceil      0dp: ${formatDecimal(big, 0, 'ceil')}`);
console.log('(Number would collapse 9007199254740993 to 9007199254740992; BigInt does not)');

// 8. Caliber mismatch is rejected.
show('8. caliber (metric version) mismatch');
try {
  mergeAggregates([
    aggregate([obs('a', metricRef('conversion-rate', 'v1'), 1n, 2n)]),
    aggregate([obs('b', metricRef('conversion-rate', 'v2'), 1n, 2n)]),
  ]);
} catch (error) {
  console.log(`rejected: ${(error as Error).message}`);
}
