import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational, isqrt } from '../src/rational.ts';
import { Decimal } from '../src/decimal.ts';

const R = Rational.make;
const D = Decimal.from;

test('构造与约分', () => {
  assert.equal(R(2n, 4n).toString(), '1/2');
  assert.equal(R(-1n, -2n).toString(), '1/2');
  assert.equal(R(3n, -4n).toString(), '-3/4');
  assert.equal(R(0n, 7n).toString(), '0');
  assert.throws(() => R(1n, 0n), /分母为零/);
});

test('四则运算精确', () => {
  assert.equal(R(1n, 3n).add(R(1n, 6n)).toString(), '1/2');
  assert.equal(R(1n, 3n).sub(R(1n, 2n)).toString(), '-1/6');
  assert.equal(R(2n, 3n).mul(R(9n, 4n)).toString(), '3/2');
  assert.equal(R(1n, 2n).div(R(3n, 4n)).toString(), '2/3');
  assert.equal(R(1n, 3n).cmp(R(1n, 4n)), 1);
  assert.equal(R(2n, 4n).cmp(R(1n, 2n)), 0);
});

test('Decimal 互转', () => {
  assert.equal(Rational.from(D('0.125')).toString(), '1/8');
  assert.equal(Rational.from(D('1200')).toString(), '1200');
  assert.equal(R(1n, 8n).toDecimalExact()!.toString(), '0.125');
  assert.equal(R(1n, 3n).toDecimalExact(), null);
  assert.equal(R(1n, 3n).toDecimal(5, 'half-even').toString(), '0.33333');
  assert.equal(R(2n, 3n).toDecimal(1, 'half-even').toString(), '0.7');
  // 恰为一半的十进制：1/8 = 0.125，2 位有效 half-even → 0.12
  assert.equal(R(1n, 8n).toDecimal(2, 'half-even').toString(), '0.12');
});

test('isqrt 精确（含大数）', () => {
  assert.equal(isqrt(0n), 0n);
  assert.equal(isqrt(3n), 1n);
  assert.equal(isqrt(10n ** 50n), 10n ** 25n);
  assert.equal(isqrt(10n ** 50n - 1n), 10n ** 25n - 1n);
});

test('sqrt：正确舍入', () => {
  assert.equal(R(4n, 1n).sqrt(6, 'half-even').toString(), '2');
  assert.equal(R(4n, 9n).sqrt(3, 'half-even').toString(), '0.667');
  assert.equal(R(2n, 1n).sqrt(6, 'half-even').toString(), '1.41421');
  assert.equal(R(1n, 4n).sqrt(6, 'half-even').toString(), '0.5');
  assert.throws(() => R(-1n, 1n).sqrt(6, 'half-even'), /负数/);
});
