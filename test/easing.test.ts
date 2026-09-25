import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  cubicBezier,
  easeInQuad,
  easeOutQuad,
  linear,
  resolveEasing,
  reverseEasing,
  steps,
} from '../src/easing.ts';

test('linear is the identity', () => {
  for (const t of [0, 0.25, 0.5, 0.75, 1]) assert.equal(linear(t), t);
});

test('cubic-bezier hits endpoints and midpoint of the symmetric curve', () => {
  const easeInOut = cubicBezier(0.42, 0, 0.58, 1);
  assert.equal(easeInOut(0), 0);
  assert.equal(easeInOut(1), 1);
  assert.ok(Math.abs(easeInOut(0.5) - 0.5) < 1e-6);
  assert.ok(Math.abs(easeInOut(0.25) - 0.129) < 0.01);
});

test('cubic-bezier rejects out-of-range x values', () => {
  assert.throws(() => cubicBezier(-0.1, 0, 0.5, 1));
  assert.throws(() => cubicBezier(0.1, 0, 1.5, 1));
});

test('steps end/start hold then jump', () => {
  const end = steps(2, 'end');
  assert.equal(end(0.25), 0);
  assert.equal(end(0.5), 0.5);
  assert.equal(end(0.75), 0.5);
  const start = steps(2, 'start');
  assert.equal(start(0.25), 0.5);
  assert.equal(start(0.75), 1);
});

test('reverseEasing mirrors time: reverse(e)(t) === 1 - e(1 - t)', () => {
  const reversed = reverseEasing(easeInQuad);
  for (const t of [0, 0.1, 0.5, 0.9, 1]) {
    assert.equal(reversed(t), 1 - easeInQuad(1 - t));
    assert.ok(Math.abs(reversed(t) - easeOutQuad(t)) < 1e-12);
  }
});

test('resolveEasing accepts names, functions and undefined', () => {
  assert.equal(resolveEasing(undefined), linear);
  assert.equal(resolveEasing('linear'), linear);
  const custom = (t: number) => t;
  assert.equal(resolveEasing(custom), custom);
  assert.throws(() => resolveEasing('nope' as never));
});
