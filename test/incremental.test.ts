import { test } from 'node:test';
import assert from 'node:assert/strict';
import { loadPolicy } from '../src/policy.ts';
import { evaluate } from '../src/evaluator.ts';
import { IncrementalEngine } from '../src/incremental.ts';
import { toString } from '../src/rational.ts';

const schema = {
  age: { type: 'int', min: 0, max: 120 },
  tier: { type: 'enum', values: ['free', 'pro', 'enterprise'] },
  verified: { type: 'bool' },
  strikes: { type: 'int', min: 0, max: 3 },
} as const;

function makePolicy() {
  return loadPolicy({
    schema,
    thresholds: [
      { grade: 'A', min: 80 },
      { grade: 'B', min: 50 },
      { grade: 'C', min: 0 },
    ],
    rules: [
      { id: 'adult', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 40 },
      { id: 'pro', when: { op: 'in', attr: 'tier', values: ['pro', 'enterprise'] }, weight: 30 },
      { id: 'verified', when: { op: 'bool', attr: 'verified', value: true }, weight: 15 },
      { id: 'penalty', when: { op: 'cmp', attr: 'strikes', cmp: 'gte', value: 1 }, weight: -20 },
      { id: 'banned', when: { op: 'cmp', attr: 'strikes', cmp: 'gte', value: 3 }, hardReject: true },
    ],
  });
}

function summarize(r: ReturnType<typeof evaluate>) {
  return {
    status: r.status,
    outcomes: r.outcomes
      .map((o) => (o.kind === 'rejected' ? 'rejected' : `grade:${o.grade}`))
      .sort(),
    bounds: [toString(r.scoreBounds.min), toString(r.scoreBounds.max)],
    scores: r.reachableScores.map(toString),
  };
}

test('incremental updates match full recompute after each correction', () => {
  const policy = makePolicy();
  const engine = new IncrementalEngine(policy);
  engine.setAttributes({ age: 30 });

  const steps: Array<Record<string, number | string | boolean | null>> = [
    { tier: 'pro' },          // fill in a missing attribute
    { verified: true },       // fill another
    { strikes: 1 },           // penalty applies
    { strikes: 3 },           // correction: now hard-rejected
    { strikes: 0 },           // correction: rejection lifted
    { age: 10 },              // correction: adult bonus lost
    { tier: null },           // correction: attribute becomes unknown again
  ];

  let attrs: Record<string, number | string | boolean> = { age: 30 };
  for (const step of steps) {
    engine.update(step);
    attrs = { ...attrs };
    for (const [k, v] of Object.entries(step)) {
      if (v === null) delete attrs[k];
      else attrs[k] = v;
    }
    const viaEngine = summarize(engine.evaluate());
    const viaFull = summarize(evaluate(policy, attrs));
    assert.deepEqual(viaEngine, viaFull, `mismatch after update ${JSON.stringify(step)}`);
  }
});

test('only rules reading a changed attribute are re-evaluated', () => {
  const policy = makePolicy();
  const engine = new IncrementalEngine(policy);
  engine.setAttributes({ age: 30, tier: 'pro', verified: true, strikes: 0 });
  const baseline = engine.getState().reevaluations;

  engine.update({ age: 31 }); // only 'adult' reads age
  assert.equal(engine.getState().reevaluations - baseline, 1);

  engine.update({ strikes: 2 }); // 'penalty' and 'banned' read strikes
  assert.equal(engine.getState().reevaluations - baseline, 3);
});

test('known score is maintained incrementally and exactly', () => {
  const policy = makePolicy();
  const engine = new IncrementalEngine(policy);
  engine.setAttributes({ age: 30, tier: 'pro', verified: true, strikes: 1 });
  assert.equal(toString(engine.getState().knownScore), '65'); // 40+30+15-20
  engine.update({ strikes: 0 });
  assert.equal(toString(engine.getState().knownScore), '85');
  engine.update({ verified: false });
  assert.equal(toString(engine.getState().knownScore), '70');
});

test('hard reject appears and lifts incrementally', () => {
  const policy = makePolicy();
  const engine = new IncrementalEngine(policy);
  engine.setAttributes({ age: 30, tier: 'enterprise', verified: true, strikes: 0 });
  assert.equal(engine.evaluate().status, 'definite');
  assert.equal(engine.getState().knownRejectFired, false);

  engine.update({ strikes: 3 });
  let r = engine.evaluate();
  assert.equal(r.status, 'definite');
  assert.equal(r.outcome?.kind, 'rejected');

  engine.update({ strikes: 1 });
  r = engine.evaluate();
  assert.equal(r.status, 'definite');
  assert.equal(r.outcome?.kind, 'grade');
});

test('out-of-domain corrections are rejected', () => {
  const policy = makePolicy();
  const engine = new IncrementalEngine(policy);
  engine.setAttributes({ age: 30 });
  assert.throws(() => engine.update({ age: 200 }), /out-of-domain/);
  assert.throws(() => engine.update({ nosuch: 1 }), /not declared/);
  assert.throws(() => engine.update({ tier: 'gold' }), /out-of-domain/);
});
