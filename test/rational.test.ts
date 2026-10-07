import {
  add,
  cmp,
  div,
  formatRational,
  fromNumberExact,
  makeRational,
  mul,
  parseDecimalString,
  pow10,
  rational,
  sub,
  toDecimalString,
  toNumber,
} from '../src/rational.js';
import { assert, assertRationalEqual, assertThrows, lcg, test } from './harness.js';

test('rational: normalization reduces and fixes signs', () => {
  assertRationalEqual(makeRational(2n, 4n), makeRational(1n, 2n), '2/4 reduces');
  assertRationalEqual(makeRational(1n, -2n), makeRational(-1n, 2n), 'sign moves to numerator');
  assertRationalEqual(makeRational(-3n, -6n), makeRational(1n, 2n), 'double negative');
  assertRationalEqual(makeRational(0n, 7n), makeRational(0n, 1n), 'zero is canonical');
  assertThrows(() => makeRational(1n, 0n), RangeError, 'zero denominator');
});

test('rational: arithmetic is exact', () => {
  const half = makeRational(1n, 2n);
  const third = makeRational(1n, 3n);
  assertRationalEqual(add(half, third), makeRational(5n, 6n), '1/2 + 1/3');
  assertRationalEqual(sub(half, third), makeRational(1n, 6n), '1/2 - 1/3');
  assertRationalEqual(mul(half, third), makeRational(1n, 6n), '1/2 * 1/3');
  assertRationalEqual(div(half, third), makeRational(3n, 2n), '1/2 / 1/3');
  assert(cmp(third, half) === -1 && cmp(half, third) === 1 && cmp(half, half) === 0, 'cmp');
  assertThrows(() => div(half, makeRational(0n)), RangeError, 'division by zero');
});

test('rational: fromNumberExact round-trips doubles exactly', () => {
  const rand = lcg(42);
  for (let i = 0; i < 1000; i++) {
    const x = (rand() - 0.5) * 10 ** Math.floor(rand() * 40 - 20);
    assert(toNumber(fromNumberExact(x)) === x, `exact round-trip for ${x}`);
  }
  assertRationalEqual(fromNumberExact(0.5), makeRational(1n, 2n), '0.5 is exact');
  assert(cmp(fromNumberExact(0.1), makeRational(1n, 10n)) !== 0, 'binary 0.1 is not 1/10');
  assertThrows(() => fromNumberExact(NaN), RangeError, 'NaN rejected');
  assertThrows(() => fromNumberExact(Infinity), RangeError, 'Infinity rejected');
});

test('rational: decimal parsing and shortest number coercion', () => {
  assertRationalEqual(parseDecimalString('1.25e3'), makeRational(1250n), '1.25e3');
  assertRationalEqual(parseDecimalString('1e-3'), makeRational(1n, 1000n), '1e-3');
  assertRationalEqual(parseDecimalString('-0.5'), makeRational(-1n, 2n), '-0.5');
  assertRationalEqual(parseDecimalString('.25'), makeRational(1n, 4n), '.25');
  assertRationalEqual(rational(0.1), makeRational(1n, 10n), 'rational(0.1) is exactly 1/10');
  assertRationalEqual(rational('0.5'), makeRational(1n, 2n), 'string coercion');
  assertRationalEqual(rational(7n), makeRational(7n, 1n), 'bigint coercion');
  assertThrows(() => parseDecimalString('abc'), SyntaxError, 'garbage rejected');
  assertThrows(() => parseDecimalString(''), SyntaxError, 'empty rejected');
});

test('rational: exact decimal formatting', () => {
  assert(toDecimalString(makeRational(1n, 8n)) === '0.125', '1/8');
  assert(toDecimalString(makeRational(10n, 4n)) === '2.5', '10/4');
  assert(toDecimalString(makeRational(-7n, 2n)) === '-3.5', '-7/2');
  assert(toDecimalString(makeRational(4n, 2n)) === '2', '4/2');
  assert(toDecimalString(makeRational(1n, 3n)) === null, '1/3 does not terminate');
  assert(formatRational(makeRational(1n, 3n)) === '1/3', 'fraction fallback');
  assert(formatRational(makeRational(0n)) === '0', 'zero');
  assert(
    formatRational(pow10(-30)) === '0.000000000000000000000000000001',
    'tiny power of ten',
  );
});
