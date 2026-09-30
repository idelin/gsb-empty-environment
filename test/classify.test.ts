import { test } from 'node:test';
import assert from 'node:assert/strict';
import { measurement, boxOf } from '../src/measurement.ts';
import { constraint } from '../src/constraint.ts';
import { classifyConstraint } from '../src/classify.ts';
import { Rational } from '../src/rational.ts';

const box1 = (id: string, v: string, e: string | 'unknown') => boxOf([measurement(id, v, e)]);

test('error interval crossing the threshold is NOT passed by center value', () => {
  const box = box1('x', '10', '2'); // center 10 <= 11, but interval [8,12] crosses
  const v = classifyConstraint(constraint('c', { x: '1' }, 'le', '11'), box);
  assert.equal(v.status, 'review');
  assert.equal(v.max.kind, 'finite');
  if (v.max.kind === 'finite') assert.equal(v.max.v.toString(), '12');
});

test('definite pass and definite fail', () => {
  const box = box1('x', '10', '0.5');
  assert.equal(classifyConstraint(constraint('c', { x: '1' }, 'le', '11'), box).status, 'pass');
  assert.equal(classifyConstraint(constraint('c', { x: '1' }, 'le', '9'), box).status, 'fail');
});

test('exact touch of threshold respects open/closed sense', () => {
  const box = box1('x', '10', '0'); // exact measurement, zero error
  assert.equal(classifyConstraint(constraint('closed', { x: '1' }, 'le', '10'), box).status, 'pass');
  assert.equal(classifyConstraint(constraint('open', { x: '1' }, 'lt', '10'), box).status, 'fail');
  assert.equal(classifyConstraint(constraint('ge', { x: '1' }, 'ge', '10'), box).status, 'pass');
  assert.equal(classifyConstraint(constraint('gt', { x: '1' }, 'gt', '10'), box).status, 'fail');
});

test('unknown error is never treated as zero', () => {
  const box = box1('x', '5', 'unknown');
  const v = classifyConstraint(constraint('c', { x: '1' }, 'le', '1000000'), box);
  assert.equal(v.status, 'review'); // cannot pass: upper bound is +inf
  assert.equal(v.max.kind, 'inf');
  // ...but a constraint that does not involve x is unaffected
  const box2 = boxOf([measurement('x', '5', 'unknown'), measurement('y', '1', '0.1')]);
  assert.equal(classifyConstraint(constraint('cy', { y: '1' }, 'le', '2'), box2).status, 'pass');
});

test('multi-variable linear combination uses exact interval arithmetic', () => {
  const box = boxOf([measurement('x', '1', '0.5'), measurement('y', '2', '0.25')]);
  // 2x + 4y in [2*0.5+4*1.75, 2*1.5+4*2.25] = [8, 12]
  const v = classifyConstraint(constraint('c', { x: '2', y: '4' }, 'le', '11'), box);
  assert.equal(v.status, 'review');
  if (v.min.kind === 'finite') assert.equal(v.min.v.toString(), '8');
  if (v.max.kind === 'finite') assert.equal(v.max.v.toString(), '12');
  // worst-case corner: x=1.5, y=2.25
  assert.equal(v.maxCorner?.get('x')?.toString(), '3/2');
  assert.equal(v.maxCorner?.get('y')?.toString(), '9/4');
});

test('negative coefficients flip the corner', () => {
  const box = boxOf([measurement('x', '1', '0.5')]);
  // -x <= -0.4  <=>  x >= 0.4 ; interval [0.5, 1.5] -> pass
  const v = classifyConstraint(constraint('c', { x: '-1' }, 'le', '-0.4'), box);
  assert.equal(v.status, 'pass');
  assert.equal(v.maxCorner?.get('x')?.toString(), '1/2');
});

test('rational threshold tighter than any decimal approximation', () => {
  // x in [1/3, 1/3] exactly; threshold 1/3 closed vs open
  const box = boxOf([measurement('x', Rational.of(1n, 3n), '0')]);
  const thr = Rational.of(1n, 3n);
  assert.equal(classifyConstraint(constraint('a', { x: '1' }, 'le', thr), box).status, 'pass');
  assert.equal(classifyConstraint(constraint('b', { x: '1' }, 'lt', thr), box).status, 'fail');
});
