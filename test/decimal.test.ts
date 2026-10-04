import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Dec } from '../src/decimal.ts';

test('parse/print round-trip without Number for big values', () => {
  const big = '123456789012345678901234567890.5';
  assert.equal(Dec.fromString(big).toString(), big);
  assert.equal(Dec.fromString('-0.001').toString(), '-0.001');
  assert.equal(Dec.fromString('1e5').toString(), '100000');
  assert.equal(Dec.fromString('2.50').toString(), '2.5');
  assert.equal(Dec.fromString('.75').toString(), '0.75');
});

test('add and mul are exact', () => {
  assert.equal(Dec.add(Dec.fromString('0.1'), Dec.fromString('0.2')).toString(), '0.3');
  assert.equal(Dec.mul(Dec.fromString('1.1'), Dec.fromString('1.1')).toString(), '1.21');
  assert.equal(Dec.sub(Dec.fromString('0.3'), Dec.fromString('0.1')).toString(), '0.2');
});

test('division rounds in the requested direction', () => {
  const one = Dec.one;
  const three = Dec.fromString('3');
  assert.equal(Dec.div(one, three, 5, 'floor').toString(), '0.33333');
  assert.equal(Dec.div(one, three, 5, 'ceil').toString(), '0.33334');
  const negOne = Dec.fromString('-1');
  assert.equal(Dec.div(negOne, three, 5, 'floor').toString(), '-0.33334');
  assert.equal(Dec.div(negOne, three, 5, 'ceil').toString(), '-0.33333');
});

test('half-even rounds ties to even', () => {
  assert.equal(Dec.div(Dec.one, Dec.fromString('8'), 2, 'half-even').toString(), '0.12');
  assert.equal(Dec.div(Dec.fromString('3'), Dec.fromString('8'), 2, 'half-even').toString(), '0.38');
});

test('round applies direction to exact values', () => {
  const x = Dec.fromString('0.12345');
  assert.equal(Dec.round(x, 2, 'floor').toString(), '0.12');
  assert.equal(Dec.round(x, 2, 'ceil').toString(), '0.13');
  assert.equal(Dec.round(Dec.fromString('-0.12345'), 2, 'floor').toString(), '-0.13');
  assert.equal(Dec.round(Dec.fromString('-0.12345'), 2, 'ceil').toString(), '-0.12');
});

test('big mantissas stay exact (no Number intermediary)', () => {
  const a = Dec.fromString('9'.repeat(40));
  assert.equal(Dec.add(a, Dec.one).toString(), '1' + '0'.repeat(40));
  const p = Dec.mul(a, a);
  assert.equal(p.toString(), ((10n ** 40n - 1n) ** 2n).toString());
});

test('division by zero throws', () => {
  assert.throws(() => Dec.div(Dec.one, Dec.zero, 5, 'floor'), /division by zero/);
});
