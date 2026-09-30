import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Engine } from '../src/engine.ts';
import { constraint } from '../src/constraint.ts';

function makeEngine(): Engine {
  const e = new Engine();
  e.setMeasurement('temp', '20', '1');
  e.setMeasurement('press', '5', '0.5');
  e.registerConstraintSet('safety', [
    constraint('temp-cap', { temp: '1' }, 'le', '22'),
    constraint('press-cap', { press: '1' }, 'le', '6'),
  ]);
  return e;
}

test('judgment is cached while measurements are unchanged', () => {
  const e = makeEngine();
  const j1 = e.evaluate('safety');
  const j2 = e.evaluate('safety');
  assert.equal(j1.kind, 'pass'); // temp in [19,21] <= 22, press in [4.5,5.5] <= 6
  assert.strictEqual(j1, j2); // same cached object, no recomputation
  assert.equal(e.history().length, 0);
});

test('modifying a measurement incrementally revokes old evidence, keeps history', () => {
  const e = makeEngine();
  const before = e.evaluate('safety');
  assert.equal(before.kind, 'pass');

  e.setMeasurement('temp', '21.5', '1'); // now temp in [20.5, 22.5] crosses 22
  const after = e.evaluate('safety');
  assert.equal(after.kind, 'mixed');

  const hist = e.history();
  assert.equal(hist.length, 1);
  assert.equal(hist[0].judgment.kind, 'pass'); // old verdict preserved
  assert.match(hist[0].supersededBy, /temp.*version 2/);
  assert.equal(hist[0].deps.get('temp'), 1); // judged against version 1
});

test('further updates append to history without losing earlier judgments', () => {
  const e = makeEngine();
  e.evaluate('safety');
  e.setMeasurement('temp', '21.5', '1');
  e.evaluate('safety');
  e.setMeasurement('press', '7', '0.5'); // press in [6.5,7.5] > 6 -> fail
  const j = e.evaluate('safety');
  assert.equal(j.kind, 'fail');
  assert.deepEqual(
    e.history().map((h) => h.judgment.kind),
    ['pass', 'mixed'],
  );
});

test('unrelated measurement updates do not revoke a judgment', () => {
  const e = makeEngine();
  e.setMeasurement('humidity', '40', '5');
  const j1 = e.evaluate('safety');
  e.setMeasurement('humidity', '41', '5'); // not referenced by 'safety'
  const j2 = e.evaluate('safety');
  assert.strictEqual(j1, j2);
  assert.equal(e.history().length, 0);
});

test('re-registering a constraint set archives the previous judgment', () => {
  const e = makeEngine();
  e.evaluate('safety');
  e.registerConstraintSet('safety', [constraint('temp-cap', { temp: '1' }, 'le', '19')]);
  const j = e.evaluate('safety');
  // temp in [19,21] vs closed cap 19: max 21 > 19, min 19 <= 19 -> mixed
  assert.equal(j.kind, 'mixed');
  assert.equal(e.history().length, 1);
});

test('unknown error propagates to review/mixed, never silently to pass', () => {
  const e = new Engine();
  e.setMeasurement('x', '5', 'unknown');
  e.registerConstraintSet('s', [constraint('cap', { x: '1' }, 'le', '100')]);
  assert.equal(e.evaluate('s').kind, 'mixed');
});

test('budget is part of cache validity (no stale verdict across budgets)', () => {
  const e = new Engine();
  e.setMeasurement('x', '0.5', '0.5');
  e.setMeasurement('y', '0.5', '0.5');
  e.registerConstraintSet('joint', [
    constraint('sum-lo', { x: '1', y: '1' }, 'le', '1'),
    constraint('sum-hi', { x: '1', y: '1' }, 'ge', '2'),
  ]);
  assert.equal(e.evaluate('joint').kind, 'fail'); // cached with default budget
  assert.equal(e.evaluate('joint', { maxCombinations: 0 }).kind, 'pending'); // not the cached fail
  assert.equal(e.evaluate('joint').kind, 'fail'); // default budget again
});
