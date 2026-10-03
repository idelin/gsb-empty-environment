import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SimpleTemporalProblem } from '../src/stp.ts';

test('bounded variable reports its interval', () => {
  const stp = new SimpleTemporalProblem();
  const x = stp.addVariable();
  stp.addBounds(x, 5, 10);
  const r = stp.solve();
  assert.equal(r.consistent, true);
  assert.equal(r.earliest[x], 5);
  assert.equal(r.latest[x], 10);
});

test('difference constraints tighten through chains', () => {
  const stp = new SimpleTemporalProblem();
  const x = stp.addVariable();
  const y = stp.addVariable();
  stp.addBounds(x, 0, 100);
  stp.addBounds(y, 0, 100);
  stp.addConstraint(x, y, 7); // y - x <= 7
  stp.addConstraint(y, x, -3); // x - y <= -3  =>  y - x >= 3
  const r = stp.solve();
  assert.equal(r.consistent, true);
  // y in [x+3, x+7]; with x in [0,100]: y in [3, 100], x in [0, 97]
  assert.equal(r.earliest[y], 3);
  assert.equal(r.latest[y], 100);
  assert.equal(r.latest[x], 97);
});

test('negative cycle means inconsistency', () => {
  const stp = new SimpleTemporalProblem();
  const x = stp.addVariable();
  stp.addConstraint(stp.reference, x, 3); // x <= 3
  stp.addConstraint(x, stp.reference, -5); // x >= 5
  assert.equal(stp.solve().consistent, false);
});

test('unconstrained variable is only bounded by the reference', () => {
  const stp = new SimpleTemporalProblem();
  const x = stp.addVariable();
  stp.addConstraint(x, stp.reference, 0); // x >= 0
  const r = stp.solve();
  assert.equal(r.consistent, true);
  assert.equal(r.earliest[x], 0);
  assert.equal(r.latest[x], Infinity);
});
