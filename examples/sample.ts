/**
 * End-to-end sample: definite grading, undecided grading with witnesses,
 * correlated unknowns, hard reject, duplicate-id handling and incremental
 * recompute after attribute corrections.
 */
import { loadPolicy } from '../src/policy.ts';
import { evaluate, naiveIndependentBounds } from '../src/evaluator.ts';
import { IncrementalEngine } from '../src/incremental.ts';
import { toString } from '../src/rational.ts';
import type { EvalResult } from '../src/evaluator.ts';

function show(title: string, r: EvalResult): void {
  console.log(`\n=== ${title} ===`);
  console.log(`status:            ${r.status}`);
  console.log(`score bounds:      [${toString(r.scoreBounds.min)}, ${toString(r.scoreBounds.max)}]`);
  console.log(`reachable scores:  {${r.reachableScores.map(toString).join(', ')}}`);
  console.log(`unknown variables: [${r.unknownVariables.join(', ')}]  (${r.assignmentsExplored} joint assignments)`);
  for (const o of r.outcomes) {
    if (o.kind === 'rejected') {
      console.log(`  outcome: REJECTED            witness: ${JSON.stringify(o.witness)}`);
    } else {
      console.log(`  outcome: grade ${o.grade} (score ${toString(o.score)})  witness: ${JSON.stringify(o.witness)}`);
    }
  }
}

const schema = {
  age: { type: 'int', min: 0, max: 120 },
  tier: { type: 'enum', values: ['free', 'pro', 'enterprise'] },
  verified: { type: 'bool' },
  strikes: { type: 'int', min: 0, max: 3 },
} as const;

const policy = loadPolicy({
  schema,
  thresholds: [
    { grade: 'A', min: 80 },
    { grade: 'B', min: 50 },
    { grade: 'C', min: 0 },
  ],
  rules: [
    { id: 'adult', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 40 },
    { id: 'paid-tier', when: { op: 'in', attr: 'tier', values: ['pro', 'enterprise'] }, weight: 30 },
    { id: 'verified', when: { op: 'bool', attr: 'verified', value: true }, weight: 15 },
    { id: 'strike-penalty', when: { op: 'cmp', attr: 'strikes', cmp: 'gte', value: 1 }, weight: '-25/2' },
    { id: 'banned', when: { op: 'cmp', attr: 'strikes', cmp: 'gte', value: 3 }, hardReject: true },
    // identical duplicate of 'verified' — deduplicated, counted once
    { id: 'verified', when: { op: 'bool', attr: 'verified', value: true }, weight: 15 },
  ],
});
console.log(`policy loaded: ${policy.rules.length} rules (identical duplicate id deduplicated)`);

try {
  loadPolicy({
    schema,
    thresholds: [{ grade: 'X', min: 0 }],
    rules: [
      { id: 'clash', when: { op: 'const', value: true }, weight: 1 },
      { id: 'clash', when: { op: 'const', value: true }, weight: 2 },
    ],
  });
} catch (e) {
  console.log(`conflicting duplicate id rejected: ${(e as Error).message}`);
}

// 1. everything known -> definite
show(
  'all attributes known',
  evaluate(policy, { age: 30, tier: 'pro', verified: true, strikes: 0 }),
);

// 2. tier missing -> undecided across grades, with witnesses
show(
  'tier unknown (undecided, witnesses per grade)',
  evaluate(policy, { age: 30, verified: true, strikes: 0 }),
);

// 3. hard reject cannot be offset by score
show(
  'strikes = 3 (hard reject beats any score)',
  evaluate(policy, { age: 45, tier: 'enterprise', verified: true, strikes: 3 }),
);

// 4. correlated unknowns: exact bounds vs unsound per-term extremes
const corrPolicy = loadPolicy({
  schema: { x: { type: 'int', min: 0, max: 1 } },
  thresholds: [
    { grade: 'hi', min: 5 },
    { grade: 'mid', min: -5 },
    { grade: 'lo', min: -100 },
  ],
  rules: [
    { id: 'up', when: { op: 'cmp', attr: 'x', cmp: 'gte', value: 1 }, weight: 10 },
    { id: 'down', when: { op: 'cmp', attr: 'x', cmp: 'gte', value: 1 }, weight: -10 },
  ],
});
const corr = evaluate(corrPolicy, {});
show('shared unknown x (correlated exact bounds)', corr);
const naive = naiveIndependentBounds(corrPolicy, {});
console.log(`  (unsound per-term extremes would claim [${toString(naive.min)}, ${toString(naive.max)}] and report undecided)`);

// 5. incremental recompute after attribute corrections
console.log('\n=== incremental recompute after corrections ===');
const engine = new IncrementalEngine(policy);
engine.setAttributes({ age: 30, tier: 'pro' });
console.log(`start {age:30, tier:pro}:            ${engine.evaluate().status}, knownScore=${toString(engine.getState().knownScore)}, unknowns=[${engine.evaluate().unknownVariables}]`);
engine.update({ verified: true });
console.log(`+ verified=true:                     ${engine.evaluate().status}, knownScore=${toString(engine.getState().knownScore)}`);
engine.update({ strikes: 1 });
console.log(`+ strikes=1 (penalty -25/2):         ${engine.evaluate().status}, knownScore=${toString(engine.getState().knownScore)}`);
engine.update({ strikes: 3 });
const rejected = engine.evaluate();
console.log(`~ strikes=3 (corrected):             ${rejected.status}, outcome=${rejected.outcome?.kind}`);
engine.update({ strikes: 0 });
const lifted = engine.evaluate();
console.log(`~ strikes=0 (corrected again):       ${lifted.status}, outcome=${lifted.outcome?.kind === 'grade' ? `grade ${lifted.outcome.grade}` : lifted.outcome?.kind}`);
console.log(`rule re-evaluations performed:       ${engine.getState().reevaluations} (only affected rules)`);
