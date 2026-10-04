import { test } from 'node:test';
import assert from 'node:assert/strict';
import { minNumberString, minBytesOfLen, minStringOfLen } from '../src/values.ts';
import { base64Encode, canonical, compareEncoded } from '../src/canonical.ts';

test('minNumberString picks fewest digits, then lexicographic minimum', () => {
  assert.equal(minNumberString(0n, 0n), '0');
  assert.equal(minNumberString(-10n, 10n), '0');
  assert.equal(minNumberString(5n, 9n), '5');
  assert.equal(minNumberString(10n, 99n), '10');
  assert.equal(minNumberString(-9n, -1n), '-1');
  assert.equal(minNumberString(-100n, -5n), '-5');
  assert.equal(minNumberString(-100n, -50n), '-50');
  assert.equal(minNumberString(51n, 100n), '51');
  assert.equal(minNumberString(9223372036854775808n, 18446744073709551615n), '9223372036854775808');
  assert.equal(minNumberString(-(2n ** 63n), 2n ** 63n - 1n), '0');
});

test('minNumberString is brute-force minimal over small ranges', () => {
  for (const [lo, hi] of [[-150n, 150n], [-5n, 5n], [90n, 1100n], [-1100n, -90n]] as const) {
    let best: string | null = null;
    for (let v = lo; v <= hi; v++) {
      const s = v.toString();
      if (best === null || compareEncoded(s, best) < 0) best = s;
    }
    assert.equal(minNumberString(lo, hi), best, `range [${lo}, ${hi}]`);
  }
});

test('minBytesOfLen is brute-force minimal for n = 0, 1, 2', () => {
  for (const n of [0, 1, 2]) {
    const got = base64Encode(minBytesOfLen(n));
    let best: string | null = null;
    const total = 256 ** n;
    for (let i = 0; i < total; i++) {
      const b = new Uint8Array(n);
      for (let j = 0; j < n; j++) b[j] = (i >> (8 * j)) & 0xff;
      const s = base64Encode(b);
      if (best === null || compareEncoded(s, best) < 0) best = s;
    }
    assert.equal(got, best, `n=${n}`);
  }
  assert.equal(base64Encode(minBytesOfLen(1)), '+A==');
  assert.equal(base64Encode(minBytesOfLen(2)), '++0=');
});

test('minStringOfLen uses spaces (minimal unescaped 1-byte code point)', () => {
  assert.equal(minStringOfLen(0), '');
  assert.equal(minStringOfLen(4), '    ');
  assert.equal(canonical(minStringOfLen(4)), '"    "');
});
