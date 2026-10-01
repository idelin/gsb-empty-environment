import { test } from 'node:test';
import assert from 'node:assert/strict';
import { addDecimal, decimal, decimalToRational, decimalToString, divideDecimal } from '../src/decimal.ts';
import { equal, rational } from '../src/rational.ts';

test('显式舍入规则：1/3 保留 2 位', () => {
  assert.deepEqual(divideDecimal(1n, 3n, 2, 'truncate'), { coeff: 33n, scale: 2 });
  assert.deepEqual(divideDecimal(1n, 3n, 2, 'floor'), { coeff: 33n, scale: 2 });
  assert.deepEqual(divideDecimal(1n, 3n, 2, 'ceil'), { coeff: 34n, scale: 2 });
  assert.deepEqual(divideDecimal(1n, 3n, 2, 'half-up'), { coeff: 33n, scale: 2 });
  assert.deepEqual(divideDecimal(1n, 3n, 2, 'half-even'), { coeff: 33n, scale: 2 });
});

test('显式舍入规则：负数时 floor/ceil/truncate 各不相同', () => {
  assert.deepEqual(divideDecimal(-1n, 3n, 2, 'truncate'), { coeff: -33n, scale: 2 });
  assert.deepEqual(divideDecimal(-1n, 3n, 2, 'floor'), { coeff: -34n, scale: 2 });
  assert.deepEqual(divideDecimal(-1n, 3n, 2, 'ceil'), { coeff: -33n, scale: 2 });
});

test('half-up 与 half-even 的平局处理', () => {
  // 1/2 -> 0 位：half-up 远离零得 1，half-even 取偶得 0
  assert.deepEqual(divideDecimal(1n, 2n, 0, 'half-up'), { coeff: 1n, scale: 0 });
  assert.deepEqual(divideDecimal(1n, 2n, 0, 'half-even'), { coeff: 0n, scale: 0 });
  // 3/2 -> 1.5：half-even 取偶得 2
  assert.deepEqual(divideDecimal(3n, 2n, 0, 'half-even'), { coeff: 2n, scale: 0 });
  // 5/2 -> 2.5：half-even 取偶得 2，half-up 得 3
  assert.deepEqual(divideDecimal(5n, 2n, 0, 'half-even'), { coeff: 2n, scale: 0 });
  assert.deepEqual(divideDecimal(5n, 2n, 0, 'half-up'), { coeff: 3n, scale: 0 });
  // 负平局：-1/2 half-up 远离零得 -1
  assert.deepEqual(divideDecimal(-1n, 2n, 0, 'half-up'), { coeff: -1n, scale: 0 });
  assert.deepEqual(divideDecimal(-1n, 2n, 0, 'half-even'), { coeff: 0n, scale: 0 });
});

test('0.1 + 0.2 === 0.3（精确十进制，无浮点误差）', () => {
  const sum = addDecimal(decimal(1n, 1), decimal(2n, 1));
  assert.deepEqual(sum, { coeff: 3n, scale: 1 });
  assert.ok(equal(decimalToRational(sum), rational(3n, 10n)));
});

test('decimalToRational / decimalToString', () => {
  assert.ok(equal(decimalToRational(decimal(-25n, 2)), rational(-1n, 4n)));
  assert.equal(decimalToString(decimal(12345n, 3)), '12.345');
  assert.equal(decimalToString(decimal(-7n, 3)), '-0.007');
  assert.equal(decimalToString(decimal(42n, 0)), '42');
});

test('非法输入被拒绝', () => {
  assert.throws(() => divideDecimal(1n, 0n, 2, 'floor'), RangeError);
  assert.throws(() => decimal(1n, -1), RangeError);
});
