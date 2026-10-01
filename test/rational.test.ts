import { test } from 'node:test';
import assert from 'node:assert/strict';
import { abs, add, cmp, div, equal, mul, neg, pow10, rational, sub, toString } from '../src/rational.ts';

test('规范化：约分、符号归一、零', () => {
  assert.deepEqual(rational(2n, 4n), { num: 1n, den: 2n });
  assert.deepEqual(rational(1n, -2n), { num: -1n, den: 2n });
  assert.deepEqual(rational(-3n, -9n), { num: 1n, den: 3n });
  assert.deepEqual(rational(0n, 7n), { num: 0n, den: 1n });
  assert.throws(() => rational(1n, 0n), RangeError);
});

test('四则运算保持精确', () => {
  const third = rational(1n, 3n);
  const sixth = rational(1n, 6n);
  assert.ok(equal(add(third, sixth), rational(1n, 2n)));
  assert.ok(equal(sub(third, sixth), sixth));
  assert.ok(equal(mul(third, sixth), rational(1n, 18n)));
  assert.ok(equal(div(third, sixth), rational(2n)));
  assert.ok(equal(neg(third), rational(-1n, 3n)));
  assert.ok(equal(abs(rational(-1n, 3n)), third));
});

test('大整数相差 1 仍可区分（不经过 Number）', () => {
  const a = rational(2n ** 90n);
  const b = rational(2n ** 90n + 1n);
  assert.equal(cmp(a, b), -1);
  assert.equal(cmp(b, a), 1);
  assert.equal(cmp(a, a), 0);
  assert.equal(toString(a), (2n ** 90n).toString());
});

test('无 epsilon：1/3 与其 30 位小数近似严格区分', () => {
  const third = rational(1n, 3n);
  const approx = rational(333333333333333333333333333333n, pow10(30));
  assert.equal(cmp(third, approx), 1);
  assert.equal(cmp(approx, third), -1);
});
