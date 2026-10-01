import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseDecimal, formatDecimal, normalizeDecimal } from '../src/decimal.ts';

test('parse plain integers and fractions exactly', () => {
  assert.deepEqual(parseDecimal('144'), { coeff: 144n, exp: 0 });
  assert.deepEqual(parseDecimal('0.00000000009'), { coeff: 9n, exp: -11 });
  assert.deepEqual(parseDecimal('.5'), { coeff: 5n, exp: -1 });
  assert.deepEqual(parseDecimal('1.5e3'), { coeff: 15n, exp: 2 });
  assert.deepEqual(parseDecimal('1E-7'), { coeff: 1n, exp: -7 });
  assert.deepEqual(parseDecimal('-0'), { coeff: 0n, exp: 0 });
  assert.deepEqual(parseDecimal('000.500'), { coeff: 500n, exp: -3 });
});

test('parse rejects invalid input', () => {
  for (const bad of ['', 'abc', '1.2.3', '0x10', 'Infinity', 'NaN', '1e', '--1', '1,5']) {
    assert.throws(() => parseDecimal(bad), SyntaxError);
  }
  assert.throws(() => parseDecimal('1e10000000000'), RangeError);
});

test('format renders plain notation both directions', () => {
  assert.equal(formatDecimal({ coeff: 9n, exp: -11 }), '0.00000000009');
  assert.equal(formatDecimal({ coeff: 15n, exp: 2 }), '1500');
  assert.equal(formatDecimal({ coeff: -25n, exp: -1 }), '-2.5');
  assert.equal(formatDecimal({ coeff: 1n, exp: -500 }), '0.' + '0'.repeat(499) + '1');
});

test('parse/format round-trip preserves value (recovery keeps rounding-relevant value)', () => {
  const samples = ['0', '144', '0.00000000009', '6.25', '1.5e3', '1E-7', '123456789123456789.000125'];
  for (const s of samples) {
    const d = parseDecimal(s);
    const back = parseDecimal(formatDecimal(d));
    assert.deepEqual(normalizeDecimal(back), normalizeDecimal(d));
  }
});

test('normalize strips trailing zeros', () => {
  assert.deepEqual(normalizeDecimal({ coeff: 1200000n, exp: -5 }), { coeff: 12n, exp: 0 });
  assert.deepEqual(normalizeDecimal({ coeff: 0n, exp: -9 }), { coeff: 0n, exp: 0 });
});
