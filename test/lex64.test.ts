import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  canonicalId64FromNumberLexeme,
  canonicalId64FromText,
  id64FromBigInt,
} from '../src/lex64.ts';
import { DecodeError } from '../src/types.ts';

test('id64 接受规范十进制字符串', () => {
  assert.equal(canonicalId64FromText('0'), '0');
  assert.equal(canonicalId64FromText('-1'), '-1');
  assert.equal(canonicalId64FromText('9007199254740993'), '9007199254740993');
  assert.equal(canonicalId64FromText('-9223372036854775808'), '-9223372036854775808');
});

test('id64 字符串拒绝前导零、科学计数法、-0 和歧义词法', () => {
  for (const bad of ['01', '-01', '1e3', '1.0', '-0', ' 1', '1 ', '0x1', '1_000', '']) {
    assert.throws(() => canonicalId64FromText(bad), DecodeError, bad);
  }
});

test('id64 数字词法只接受精确安全整数的规范形式', () => {
  assert.equal(canonicalId64FromNumberLexeme('42'), '42');
  assert.equal(canonicalId64FromNumberLexeme('-9007199254740991'), '-9007199254740991');
  for (const bad of ['1e3', '1.0', '01', '-0', '9007199254740993', '1.5', '-00']) {
    assert.throws(() => canonicalId64FromNumberLexeme(bad), DecodeError, bad);
  }
});

test('id64 超有符号 64 位范围拒绝；字符串可承载超安全整数', () => {
  assert.equal(canonicalId64FromText('9223372036854775807'), '9223372036854775807');
  assert.throws(() => canonicalId64FromText('9223372036854775808'), DecodeError);
  assert.throws(() => canonicalId64FromText('-9223372036854775809'), DecodeError);
});

test('id64 BigInt 与字符串入口规范结果一致', () => {
  assert.equal(id64FromBigInt(9007199254740993n), canonicalId64FromText('9007199254740993'));
  assert.equal(id64FromBigInt(-1n), canonicalId64FromText('-1'));
});
