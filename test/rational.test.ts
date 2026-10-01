import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  divRound,
  rational,
  addR,
  mulR,
  cmpR,
  ratToString,
  ratToDecimal,
  ROUNDING_MODES,
  type RoundingMode,
} from '../src/rational.ts';

test('divRound: every mode, positive and negative, ties included', () => {
  const cases: [bigint, bigint, Record<RoundingMode, bigint>][] = [
    [7n, 2n, { floor: 3n, ceil: 4n, trunc: 3n, 'nearest-half-up': 4n, 'nearest-half-away': 4n, 'nearest-half-even': 4n }],
    [-7n, 2n, { floor: -4n, ceil: -3n, trunc: -3n, 'nearest-half-up': -3n, 'nearest-half-away': -4n, 'nearest-half-even': -4n }],
    [5n, 2n, { floor: 2n, ceil: 3n, trunc: 2n, 'nearest-half-up': 3n, 'nearest-half-away': 3n, 'nearest-half-even': 2n }],
    [-5n, 2n, { floor: -3n, ceil: -2n, trunc: -2n, 'nearest-half-up': -2n, 'nearest-half-away': -3n, 'nearest-half-even': -2n }],
    [9n, 4n, { floor: 2n, ceil: 3n, trunc: 2n, 'nearest-half-up': 2n, 'nearest-half-away': 2n, 'nearest-half-even': 2n }],
    [11n, 4n, { floor: 2n, ceil: 3n, trunc: 2n, 'nearest-half-up': 3n, 'nearest-half-away': 3n, 'nearest-half-even': 3n }],
    [-9n, 4n, { floor: -3n, ceil: -2n, trunc: -2n, 'nearest-half-up': -2n, 'nearest-half-away': -2n, 'nearest-half-even': -2n }],
    [8n, 4n, { floor: 2n, ceil: 2n, trunc: 2n, 'nearest-half-up': 2n, 'nearest-half-away': 2n, 'nearest-half-even': 2n }],
  ];
  for (const [n, d, expected] of cases) {
    for (const mode of ROUNDING_MODES) {
      assert.equal(divRound(n, d, mode), expected[mode], `divRound(${n}, ${d}, ${mode})`);
    }
  }
  assert.throws(() => divRound(1n, 0n, 'floor'), RangeError);
});

test('rational: normalization, arithmetic, comparison', () => {
  assert.deepEqual(rational(6n, -8n), { n: -3n, d: 4n });
  assert.deepEqual(rational(0n, 5n), { n: 0n, d: 1n });
  assert.deepEqual(rational(12n, 18n), { n: 2n, d: 3n });
  assert.throws(() => rational(1n, 0n), RangeError);
  assert.equal(ratToString(addR(rational(1n, 3n), rational(1n, 6n))), '1/2');
  assert.equal(ratToString(mulR(rational(2n, 3n), rational(9n, 4n))), '3/2');
  assert.equal(cmpR(rational(1n, 3n), rational(2n, 7n)), 1);
  assert.equal(cmpR(rational(-1n, 2n), rational(1n, 2n)), -1);
});

test('ratToDecimal: exact decimal rendering, no Number involved', () => {
  assert.equal(ratToDecimal(rational(1n, 3n), 4), '0.3333');
  assert.equal(ratToDecimal(rational(8008n, 5n), 4), '1601.6000');
  assert.equal(ratToDecimal(rational(-1n, 8n), 3), '-0.125');
  assert.equal(ratToDecimal(rational(1n, 2n), 0), '0'); // 0.5 -> half-even -> 0
  assert.equal(ratToDecimal(rational(3n, 2n), 0), '2'); // 1.5 -> half-even -> 2
});

test('huge magnitudes stay exact (beyond 2^53)', () => {
  const big = 2n ** 60n;
  const r = rational(big * 3n, 7n);
  const q = divRound(r.n, r.d, 'floor');
  assert.equal(q, (big * 3n) / 7n);
  assert.equal(ratToString(mulR(rational(big), rational(1n, big))), '1');
});
