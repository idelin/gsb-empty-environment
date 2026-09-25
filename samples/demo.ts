/**
 * End-to-end sample: a rental-application grading policy demonstrating
 * rational weights, hard rejects, threshold bands, correlated unknowns,
 * duplicate rule ids and incremental recompute after corrections.
 *
 * Run with: node samples/demo.ts   (Node >= 22.6, type stripping)
 */
import {
  evaluate,
  normalizePolicy,
  GradingSession,
  REJECT,
  type EvalResult,
  type PolicySpec,
} from '../src/index.ts';

const spec: PolicySpec = {
  attributes: {
    credit: [550, 650, 750],       // credit band, finite set
    employmentYears: [0, 1, 2, 5], // integer condition domain
    defaults: [0, 1],              // 1 = has payment defaults
  },
  rules: [
    { id: 'credit-strong', when: { kind: 'cmp', attr: 'credit', op: 'ge', value: 750 }, weight: '2/3' },
    { id: 'credit-ok', when: { kind: 'in', attr: 'credit', set: [650, 750] }, weight: '1/3' },
    { id: 'tenure', when: { kind: 'cmp', attr: 'employmentYears', op: 'ge', value: 2 }, weight: '1/2' },
    { id: 'junior-penalty', when: { kind: 'cmp', attr: 'employmentYears', op: 'eq', value: 0 }, weight: '-1/4' },
    {
      id: 'fraud-combo',
      when: { kind: 'and', args: [
        { kind: 'cmp', attr: 'defaults', op: 'eq', value: 1 },
        { kind: 'not', arg: { kind: 'cmp', attr: 'credit', op: 'ge', value: 750 } },
      ] },
      hardReject: true,
    },
    // identical duplicate of credit-ok: counted once, not twice
    { id: 'credit-ok', when: { kind: 'in', attr: 'credit', set: [650, 750] }, weight: '1/3' },
  ],
  bands: [
    { grade: 'declined', gte: null },
    { grade: 'standard', gte: '1/2' },
    { grade: 'preferred', gte: 1 },
  ],
};

function show(label: string, r: EvalResult): void {
  console.log(`\n=== ${label} ===`);
  console.log(`status:            ${r.status}`);
  if (r.definiteOutcome) {
    if (r.definiteOutcome.type === 'reject') {
      console.log(`outcome:           REJECT (rules: ${r.definiteOutcome.ruleIds.join(', ')})`);
    } else {
      const score = r.definiteOutcome.score === null ? 'varies' : r.definiteOutcome.score.toString();
      console.log(`outcome:           grade=${r.definiteOutcome.grade} score=${score}`);
    }
  }
  console.log(
    `possible outcomes: ${r.possibleOutcomes.map((o) => (o.type === 'grade' ? o.grade : 'REJECT')).join(', ')}`,
  );
  if (r.scoreBounds) {
    console.log(`score bounds:      [${r.scoreBounds.min}, ${r.scoreBounds.max}]  (exact, correlated)`);
  }
  for (const [key, witness] of r.witnesses) {
    console.log(`witness ${key.padEnd(9)}: ${JSON.stringify(witness)}`);
  }
  console.log(`assignments:       ${r.assignmentsExplored}`);
}

const policy = normalizePolicy(spec);

// 1. Fully known application -> definite grade.
show('fully known application', evaluate(policy, { credit: 650, employmentYears: 2, defaults: 0 }));

// 2. defaults unknown: hard reject may or may not fire -> pending, never midpoint-graded.
show('defaults unknown (uncertain hard reject)', evaluate(policy, { credit: 650, employmentYears: 2 }));

// 3. credit unknown: two rules share the same unknown; bounds stay correlated.
show('credit unknown (shared unknown, correlated bounds)', evaluate(policy, { employmentYears: 5, defaults: 0 }));

// 4. Incremental session: corrections arrive over time.
console.log('\n=== incremental recompute after corrections ===');
const session = new GradingSession(policy, { credit: 550 });
show('initial: only credit=550 known', session.evaluate());
session.setAttributes({ employmentYears: 0, defaults: 1 });
show('after correction: employmentYears=0, defaults=1', session.evaluate());
session.setAttribute('credit', 750); // corrected credit report: 550 -> 750
show('after correction: credit 550 -> 750 (lifts the hard reject)', session.evaluate());
console.log(`session stats:     ${JSON.stringify(session.stats)}`);
session.removeAttribute('defaults'); // defaults becomes unknown again
show('defaults revoked to unknown', session.evaluate());
session.evaluate(); // nothing changed: enumeration reused verbatim
console.log(`after no-op evaluate, stats: ${JSON.stringify(session.stats)}`);

// 5. Duplicate rule id with conflicting content is rejected.
console.log('\n=== conflicting duplicate rule id ===');
try {
  normalizePolicy({
    ...spec,
    rules: [...spec.rules, { id: 'tenure', when: { kind: 'const', value: true }, weight: 99 }],
  });
  console.log('ERROR: conflict was not detected');
} catch (err) {
  console.log(`rejected as expected: ${(err as Error).name}: ${(err as Error).message}`);
}

// 6. Midpoint fallacy guard: interval straddling the "standard" threshold.
console.log('\n=== interval straddling a threshold is pending, not midpoint-graded ===');
const straddle = evaluate(normalizePolicy({
  attributes: { x: [0, 1] },
  rules: [{ id: 'swing', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, weight: 1 }],
  bands: [
    { grade: 'declined', gte: null },
    { grade: 'standard', gte: '1/2' },
  ],
}));
console.log(`scores {0, 1}, midpoint 1/2 would say "standard"; engine says: ${straddle.status}`);
console.log(`possible outcomes: ${straddle.possibleOutcomes.map((o) => (o.type === 'grade' ? o.grade : REJECT)).join(', ')}`);
