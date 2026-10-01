import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  add,
  cmp,
  div,
  equal,
  mul,
  rational,
  sub,
  toExactString,
} from '../src/rational.ts';
import { formatDecimal, formatPercent, roundToDecimalString } from '../src/decimal.ts';

test('normalizes sign and reduces by gcd', () => {
  assert.deepEqual(rational(2n, 4n), { num: 1n, den: 2n });
  assert.deepEqual(rational(3n, -6n), { num: -1n, den: 2n });
  assert.deepEqual(rational(-3n, -6n), { num: 1n, den: 2n });
  assert.deepEqual(rational(0n, 99n), { num: 0n, den: 1n });
});

test('rejects zero denominator', () => {
  assert.throws(() => rational(1n, 0n), RangeError);
});

test('exact arithmetic', () => {
  assert.ok(equal(add(rational(1n, 2n), rational(1n, 3n)), rational(5n, 6n)));
  assert.ok(equal(sub(rational(1n, 2n), rational(1n, 3n)), rational(1n, 6n)));
  assert.ok(equal(mul(rational(2n, 3n), rational(3n, 4n)), rational(1n, 2n)));
  assert.ok(equal(div(rational(1n, 2n), rational(3n, 4n)), rational(2n, 3n)));
  assert.equal(cmp(rational(1n, 3n), rational(2n, 3n)), -1);
  assert.equal(cmp(rational(2n, 3n), rational(2n, 3n)), 0);
  assert.equal(cmp(rational(3n, 3n), rational(2n, 3n)), 1);
});

test('division by zero rational throws', () => {
  assert.throws(() => div(rational(1n), rational(0n)), RangeError);
});

test('stays exact beyond 2^53 without touching Number', () => {
  const big = 9007199254740993n; // Number(9007199254740993n) === 9007199254740992
  const a = rational(big, 3n);
  const b = rational(big, 7n);
  const sum = add(a, b);
  // (big/3 + big/7) = big*10/21, and big*10 is not representable in Number.
  assert.ok(equal(sum, rational(big * 10n, 21n)));
  assert.equal(toExactString(sum), '30023997515803310/7'); // reduced by gcd 3
  // value must not have collapsed to the 2^53-rounded neighbor
  assert.notEqual(sum.num, 90071992547409920n);
});

test('rounding modes are explicit and distinct on ties', () => {
  const half = rational(1n, 2n); // 0.5, tie at scale 0
  assert.equal(roundToDecimalString(half, 0, 'half-up'), '1');
  assert.equal(roundToDecimalString(half, 0, 'half-even'), '0');
  assert.equal(roundToDecimalString(half, 0, 'floor'), '0');
  assert.equal(roundToDecimalString(half, 0, 'ceil'), '1');
  assert.equal(roundToDecimalString(half, 0, 'toward-zero'), '0');
  assert.equal(roundToDecimalString(half, 0, 'away-from-zero'), '1');

  const threeHalves = rational(3n, 2n); // 1.5, tie at scale 0
  assert.equal(roundToDecimalString(threeHalves, 0, 'half-even'), '2'); // 2 is even
  assert.equal(roundToDecimalString(threeHalves, 0, 'half-up'), '2');
});

test('negative rounding follows the declared mode', () => {
  const neg = rational(-1n, 8n); // -0.125
  assert.equal(roundToDecimalString(neg, 2, 'half-up'), '-0.13');
  assert.equal(roundToDecimalString(neg, 2, 'half-even'), '-0.12');
  assert.equal(roundToDecimalString(neg, 2, 'floor'), '-0.13');
  assert.equal(roundToDecimalString(neg, 2, 'ceil'), '-0.12');
  assert.equal(roundToDecimalString(neg, 2, 'toward-zero'), '-0.12');
  assert.equal(roundToDecimalString(neg, 2, 'away-from-zero'), '-0.13');
});

test('no negative zero in output', () => {
  assert.equal(roundToDecimalString(rational(-1n, 100n), 1, 'toward-zero'), '0.0');
});

test('formatDecimal / formatPercent', () => {
  assert.equal(formatDecimal(rational(1n, 3n), 4, 'half-up'), '0.3333');
  assert.equal(formatPercent(rational(5n, 6n), 2, 'half-even'), '83.33%');
  assert.equal(formatPercent(rational(1n, 8n), 1, 'half-up'), '12.5%');
});

test('rounding of huge values stays on BigInt', () => {
  const big = rational(9007199254740993n, 2n); // 4503599627370496.5
  assert.equal(roundToDecimalString(big, 0, 'half-up'), '4503599627370497');
  assert.equal(roundToDecimalString(big, 0, 'half-even'), '4503599627370496');
});
