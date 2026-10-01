import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rat } from '../src/rational.ts';

test('exact arithmetic beyond 2^53 without Number', () => {
  const big = Rat.of(2n ** 80n);
  const squared = big.mul(big);
  assert.equal(squared.toString(), (2n ** 160n).toString());
  const third = Rat.of(1n, 3n);
  const sum = third.add(third).add(third);
  assert.ok(sum.eq(Rat.one()));
});

test('decimal strings parse exactly, never via float', () => {
  assert.ok(Rat.parse('0.1').eq(Rat.of(1n, 10n)));
  assert.ok(Rat.parse('3.14').eq(Rat.of(157n, 50n)));
  const huge = Rat.parse('1180591620717411303424.5'); // 2^70 + 1/2
  assert.ok(huge.eq(Rat.of(2n ** 71n + 1n, 2n)));
  assert.ok(Rat.parse('-2/5').eq(Rat.of(-2n, 5n)));
});

test('normalization keeps denominator positive and reduced', () => {
  const r = Rat.of(6n, -8n);
  assert.equal(r.n, -3n);
  assert.equal(r.d, 4n);
});

test('floor and ceil on negative rationals', () => {
  const r = Rat.of(-7n, 2n); // -3.5
  assert.equal(r.floor(), -4n);
  assert.equal(r.ceil(), -3n);
  assert.equal(Rat.of(4n).floor(), 4n);
});

test('comparison and sign', () => {
  assert.ok(Rat.of(1n, 3n).lt(Rat.of(1n, 2n)));
  assert.ok(Rat.of(-2n, 4n).eq(Rat.of(-1n, 2n)));
  assert.equal(Rat.of(-5n).sign(), -1);
});

test('decimal string rendering is exact', () => {
  assert.equal(Rat.of(1n, 4n).toDecimalString(), '0.25');
  assert.equal(Rat.of(-1n, 4n).toDecimalString(), '-0.25');
  assert.equal(Rat.of(1n, 3n).toDecimalString(), '1/3');
  assert.equal(Rat.of(7n).toDecimalString(), '7');
});
