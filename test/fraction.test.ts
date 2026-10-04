import { Fraction, F, floorDiv } from '../src/fraction.js';
import { test, assert } from './harness.js';

test('fraction: normalization and signs', () => {
  assert.strictEqual(F('2/4').toString(), '1/2');
  assert.strictEqual(F('-3/-6').toString(), '1/2');
  assert.strictEqual(F('3/-4').toString(), '-3/4');
  assert.strictEqual(F(0).toString(), '0');
  assert.strictEqual(F(-7).toString(), '-7');
  assert.strictEqual(F(' 5/10 ').toString(), '1/2');
});

test('fraction: arithmetic', () => {
  assert.strictEqual(F('1/2').add(F('1/3')).toString(), '5/6');
  assert.strictEqual(F('1/2').sub(F('3/4')).toString(), '-1/4');
  assert.strictEqual(F('1/4').mul(F('2/3')).toString(), '1/6');
  assert.strictEqual(F('2/3').div(F('4/5')).toString(), '5/6');
  assert.strictEqual(F('3/4').neg().toString(), '-3/4');
  assert.strictEqual(F('-3/4').abs().toString(), '3/4');
});

test('fraction: comparison and sign', () => {
  assert.strictEqual(F('1/2').cmp(F('2/3')), -1);
  assert.strictEqual(F('2/3').cmp(F('1/2')), 1);
  assert.strictEqual(F('2/4').cmp(F('1/2')), 0);
  assert.strictEqual(F('-1/5').sign(), -1);
  assert.strictEqual(F('0/9').sign(), 0);
  assert.strictEqual(F('1/5').sign(), 1);
  assert.strictEqual(F('2/4').equals(F('1/2')), true);
});

test('fraction: floorDiv for grid cells', () => {
  assert.strictEqual(floorDiv(F('-3/2'), F('1')), -2n);
  assert.strictEqual(floorDiv(F('7/2'), F('2')), 1n);
  assert.strictEqual(floorDiv(F('-1/1000000000'), F('1')), -1n);
  assert.strictEqual(floorDiv(F('0'), F('3')), 0n);
});

test('fraction: invalid inputs throw', () => {
  assert.throws(() => F('1/0'));
  assert.throws(() => F(1.5));
  assert.throws(() => F('abc'));
  assert.throws(() => F('1/2/3'));
});
