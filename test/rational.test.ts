import { test } from 'node:test';
import assert from 'node:assert/strict';
import { add, cmp, neg, parseRational, rat, sub, toString, ZERO } from '../src/rational.ts';

test('rationals normalize and reduce', () => {
  assert.deepEqual(rat(4, 6), { num: 2n, den: 3n });
  assert.deepEqual(rat(-3, -9), { num: 1n, den: 3n });
  assert.deepEqual(rat(5, -10), { num: -1n, den: 2n });
});

test('exact arithmetic across denominators', () => {
  const third = rat(1, 3);
  const sixth = rat(1, 6);
  assert.equal(toString(add(third, sixth)), '1/2');
  assert.equal(toString(sub(third, sixth)), '1/6');
  assert.equal(toString(neg(third)), '-1/3');
  // 1/3 + 1/3 + 1/3 === 1 exactly (floats would give 0.9999999999999999)
  assert.equal(toString(add(add(third, third), third)), '1');
});

test('comparison is exact at threshold boundaries', () => {
  assert.equal(cmp(rat(199, 2), rat(99, 1)), 1);
  assert.equal(cmp(rat(80, 1), rat(80, 1)), 0);
  assert.equal(cmp(rat(-1, 3), ZERO), -1);
});

test('weight literal parsing', () => {
  assert.deepEqual(parseRational(7), { num: 7n, den: 1n });
  assert.deepEqual(parseRational('-3/4'), { num: -3n, den: 4n });
  assert.deepEqual(parseRational({ num: 10, den: 4 }), { num: 5n, den: 2n });
  assert.throws(() => parseRational(1.5));
  assert.throws(() => parseRational('1/0'));
});
