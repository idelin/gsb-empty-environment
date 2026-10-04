import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import {
  EMPTY, UNKNOWN, ZeroCrossingError, add, containsInterval, containsPoint,
  div, hull, intersect, interval, isEmpty, isUnknown, mul, neg, show, sub,
} from '../src/interval.ts';

const d = (s: string) => Decimal.parse(s);
const iv = (lo: string, hi: string) => interval(d(lo), d(hi));

test('empty and unknown are distinct interval kinds', () => {
  assert.notDeepEqual(EMPTY, UNKNOWN);
  assert.ok(isEmpty(EMPTY) && !isUnknown(EMPTY));
  assert.ok(isUnknown(UNKNOWN) && !isEmpty(UNKNOWN));
  assert.ok(!containsPoint(EMPTY, d('0')));
  assert.ok(containsPoint(UNKNOWN, d('-1e30')));
  // empty is absorbed, unknown dominates
  assert.equal(add(EMPTY, iv('1', '2'), 6), EMPTY);
  assert.ok(isUnknown(add(UNKNOWN, iv('1', '2'), 6)));
  assert.deepEqual(intersect(UNKNOWN, iv('1', '2')), iv('1', '2'));
  assert.deepEqual(hull(EMPTY, iv('1', '2')), iv('1', '2'));
  assert.equal(intersect(iv('1', '2'), iv('3', '4')), EMPTY);
  // inverted bounds normalize to empty
  assert.equal(interval(d('2'), d('1')), EMPTY);
});

test('add/sub round outward to the requested scale', () => {
  const a = iv('0.123456789', '0.123456789');
  const b = iv('0.1', '0.2');
  const s = add(a, b, 4);
  assert.equal(show(s), '[0.2234, 0.3235]'); // 0.223456789 -> down, 0.323456789 -> up
  const t = sub(b, a, 4);
  assert.equal(show(t), '[-0.0235, 0.0766]');
});

test('multiplication by a negative factor flips the bounds', () => {
  const r = mul(iv('1', '2'), iv('-1', '-1'), 6);
  assert.equal(show(r), '[-2.000000, -1.000000]');
  const r2 = mul(iv('-3', '2'), iv('-2', '4'), 6);
  assert.equal(show(r2), '[-12.000000, 8.000000]');
});

test('division rounds outward', () => {
  const [r] = div(iv('1', '1'), iv('3', '3'), 4, 'reject');
  assert.equal(show(r!), '[0.3333, 0.3334]');
});

test('division across zero: reject throws, split yields explicit rays', () => {
  assert.throws(() => div(iv('1', '2'), iv('-1', '3'), 6, 'reject'), ZeroCrossingError);
  const pieces = div(iv('1', '2'), iv('-1', '3'), 6, 'split');
  assert.equal(pieces.length, 2);
  assert.equal(show(pieces[0]!), '[-inf, -1.000000]');
  assert.equal(show(pieces[1]!), '[0.333333, +inf]'); // 1/3 rounded down (outward)
});

test('division across zero: one-sided divisors and zero point', () => {
  const [p] = div(iv('1', '2'), iv('0', '2'), 4, 'split');
  assert.equal(show(p!), '[0.5000, +inf]');
  const [q] = div(iv('1', '2'), iv('-2', '0'), 4, 'split');
  assert.equal(show(q!), '[-inf, -0.5000]');
  const [z] = div(iv('1', '2'), iv('0', '0'), 4, 'split');
  assert.equal(z, EMPTY);
});

test('unbounded operands propagate through mul and div', () => {
  assert.ok(isUnknown(mul(UNKNOWN, iv('1', '2'), 6)));
  assert.equal(show(mul(interval(null, d('-1')), iv('2', '3'), 6)), '[-inf, -2.000000]');
  const [r] = div(iv('1', '1'), interval(d('2'), null), 6, 'reject');
  assert.equal(show(r!), '[0, 0.500000]');
});

test('neg, intersect, hull, containment', () => {
  assert.equal(show(neg(iv('-2', '5'))), '[-5, 2]');
  assert.equal(show(intersect(iv('0', '3'), iv('2', '9'))), '[2, 3]');
  assert.equal(show(hull(iv('0', '1'), iv('5', '9'))), '[0, 9]');
  assert.ok(containsInterval(UNKNOWN, iv('-5', '7')));
  assert.ok(containsInterval(iv('0', '4'), iv('1', '3')));
  assert.ok(!containsInterval(iv('1', '3'), iv('0', '4')));
  assert.ok(containsInterval(iv('0', '4'), EMPTY));
  assert.ok(!containsInterval(EMPTY, iv('0', '1')));
});
