import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseRationalLiteral, parseValue, UnsupportedLiteralError } from '../src/parse.ts';
import { boundsOf, evidenceOf } from '../src/value.ts';
import { equal, rational, toString } from '../src/rational.ts';

test('整数 / 小数 / 分数 / 指数字面量解析为精确有理数', () => {
  assert.ok(equal(parseRationalLiteral('42'), rational(42n)));
  assert.ok(equal(parseRationalLiteral('-3.25'), rational(-13n, 4n)));
  assert.ok(equal(parseRationalLiteral('2/4'), rational(1n, 2n)));
  assert.ok(equal(parseRationalLiteral('1e3'), rational(1000n)));
  assert.ok(equal(parseRationalLiteral('1.5e-2'), rational(3n, 200n)));
  assert.ok(equal(parseRationalLiteral('  +7  '), rational(7n)));
});

test('大数字面量直接进 BigInt，不经过 Number', () => {
  const big = '123456789012345678901234567890';
  assert.equal(toString(parseRationalLiteral(big)), big);
  assert.ok(equal(parseRationalLiteral(big + '.5'), rational(BigInt(big + '5'), 10n)));
});

test('NaN / Infinity 按不支持拒绝', () => {
  for (const bad of ['NaN', 'nan', '-NaN', 'Infinity', '-Infinity', 'inf']) {
    assert.throws(() => parseValue(bad), UnsupportedLiteralError);
  }
});

test('非法字面量拒绝', () => {
  for (const bad of ['', 'abc', '1.2.3', '0x10', '1/2/3', '--1', '1,2']) {
    assert.throws(() => parseValue(bad), UnsupportedLiteralError);
  }
});

test('区间字面量 "c±r" 与 "[lo, hi]"', () => {
  const a = parseValue('0.5±0.1');
  const ba = boundsOf(evidenceOf(a));
  assert.ok(equal(ba.lower, rational(2n, 5n)));
  assert.ok(equal(ba.upper, rational(3n, 5n)));
  const b = parseValue('[1, 2]');
  const bb = boundsOf(evidenceOf(b));
  assert.ok(equal(bb.lower, rational(1n)));
  assert.ok(equal(bb.upper, rational(2n)));
  assert.throws(() => parseValue('1±-0.5'), UnsupportedLiteralError);
  assert.throws(() => parseValue('[2, 1]'), UnsupportedLiteralError);
});

test('1/0 报 RangeError（分母为零）', () => {
  assert.throws(() => parseRationalLiteral('1/0'), RangeError);
});
