import { test } from 'node:test';
import assert from 'node:assert/strict';
import { addR, cmpR, mulR, parseRational, rat, subR } from '../src/index.ts';

test('parses exact decimals without Number', () => {
  assert.deepEqual(parseRational('0.1'), { num: 1n, den: 10n });
  assert.deepEqual(parseRational('2.5'), { num: 5n, den: 2n });
  assert.deepEqual(parseRational('.5'), { num: 1n, den: 2n });
  assert.deepEqual(parseRational('-3'), { num: -3n, den: 1n });
});

test('parses fractions', () => {
  assert.deepEqual(parseRational('1/3'), { num: 1n, den: 3n });
  assert.deepEqual(parseRational('-1.5/2'), { num: -3n, den: 4n });
  assert.deepEqual(parseRational('6/4'), { num: 3n, den: 2n });
});

test('big integers stay exact (beyond 2^53, no Number rounding)', () => {
  const r = parseRational('9007199254740993'); // 2^53 + 1
  assert.equal(typeof r.num, 'bigint');
  assert.equal(r.num, 9007199254740993n);
  const big = parseRational('123456789012345678901234567890.5');
  assert.equal(big.num, 246913578024691357802469135781n);
  assert.equal(big.den, 2n);
  const tiny = parseRational('0.3000000000000000000000000000000001');
  assert.equal(tiny.den, 10n ** 34n);
  assert.equal(tiny.num, 3000000000000000000000000000000001n);
});

test('arithmetic is exact', () => {
  assert.deepEqual(addR(rat(1n, 3n), rat(1n, 6n)), { num: 1n, den: 2n });
  assert.deepEqual(subR(rat(1n, 2n), rat(1n, 3n)), { num: 1n, den: 6n });
  assert.deepEqual(mulR(rat(2n, 3n), rat(9n, 4n)), { num: 3n, den: 2n });
  assert.equal(cmpR(rat(1n, 3n), rat(333n, 1000n)), 1);
  assert.equal(cmpR(rat(1n, 4n), rat(25n, 100n)), 0);
});

test('rejects malformed input', () => {
  for (const bad of ['', 'abc', '1.2.3', '1/0', '1/2/3', '1/0.5', '--1', '1e3']) {
    assert.throws(() => parseRational(bad), undefined, bad);
  }
});
