import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  make,
  add,
  sub,
  mul,
  div,
  cmp,
  fromScaledInt,
  roundToScale,
  toString,
  toDecimalString,
  toTerminatingDecimal,
  parseRational,
} from '../src/index.ts';

test('normalises sign and reduces to lowest terms', () => {
  assert.deepEqual(make(2n, 4n), { num: 1n, den: 2n });
  assert.deepEqual(make(3n, -6n), { num: -1n, den: 2n });
  assert.deepEqual(make(-3n, -6n), { num: 1n, den: 2n });
  assert.deepEqual(make(0n, 99n), { num: 0n, den: 1n });
});

test('exact arithmetic on rationals', () => {
  assert.equal(toString(add(make(1n, 3n), make(1n, 6n))), '0.5');
  assert.equal(toString(sub(make(1n, 2n), make(3n, 2n))), '-1');
  assert.equal(toString(mul(make(2n, 3n), make(9n, 4n))), '1.5');
  assert.equal(toString(div(make(1n, 2n), make(3n, 4n))), '2/3');
});

test('exact decimals never touch binary floating point', () => {
  // 0.1 + 0.2 === 0.3 exactly here; the float artefact 0.30000000000000004 stays distinct
  const sum = add(parseRational('0.1'), parseRational('0.2'));
  assert.equal(cmp(sum, parseRational('0.3')), 0);
  assert.equal(cmp(sum, parseRational('0.30000000000000004')), -1);
});

test('big integers differing by 1 stay distinguishable', () => {
  const a = make(2n ** 70n);
  const b = make(2n ** 70n + 1n);
  assert.equal(cmp(a, b), -1);
  assert.equal(cmp(b, a), 1);
  assert.equal(cmp(a, a), 0);
  // far beyond what Number can represent exactly
  assert.equal(Number(2n ** 70n) === Number(2n ** 70n + 1n), true);
});

test('scientific notation parses exactly', () => {
  assert.equal(toString(parseRational('1.5e-3')), '0.0015');
  assert.equal(toString(parseRational('123e2')), '12300');
  assert.equal(toString(parseRational('.25')), '0.25');
});

test('explicit rounding modes on rational -> decimal', () => {
  const v = make(7n, 2n); // 3.5
  assert.equal(toDecimalString(v, 0, 'floor'), '3');
  assert.equal(toDecimalString(v, 0, 'ceil'), '4');
  assert.equal(toDecimalString(v, 0, 'trunc'), '3');
  assert.equal(toDecimalString(v, 0, 'half-up'), '4');
  assert.equal(toDecimalString(v, 0, 'half-even'), '4'); // 4 is even

  const neg = make(-7n, 2n); // -3.5
  assert.equal(toDecimalString(neg, 0, 'floor'), '-4');
  assert.equal(toDecimalString(neg, 0, 'ceil'), '-3');
  assert.equal(toDecimalString(neg, 0, 'trunc'), '-3');
  assert.equal(toDecimalString(neg, 0, 'half-up'), '-4'); // ties away from zero
  assert.equal(toDecimalString(neg, 0, 'half-even'), '-4');

  const tie = make(5n, 2n); // 2.5 -> half-even picks 2
  assert.equal(toDecimalString(tie, 0, 'half-even'), '2');
  assert.equal(toDecimalString(tie, 0, 'half-up'), '3');

  assert.equal(toDecimalString(make(1n, 3n), 5, 'floor'), '0.33333');
  assert.equal(toDecimalString(make(1n, 3n), 5, 'ceil'), '0.33334');
});

test('terminating decimal detection', () => {
  assert.equal(toTerminatingDecimal(make(1n, 8n)), '0.125');
  assert.equal(toTerminatingDecimal(make(1n, 3n)), null);
  assert.equal(toString(make(-5n, 4n)), '-1.25');
});

test('fromScaledInt reduces powers of ten', () => {
  assert.deepEqual(fromScaledInt(1500n, -2), { num: 15n, den: 1n });
  assert.deepEqual(fromScaledInt(1n, -30), { num: 1n, den: 10n ** 30n });
});
