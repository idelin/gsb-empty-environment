import test from 'node:test';
import assert from 'node:assert/strict';
import { add, cmp, div, floorBig, make, mul, parse, sub, toDisplayString, toString } from '../src/rational.ts';

test('parse and reduce fractions and decimals exactly', () => {
  assert.equal(toString(parse('2/4')), '1/2');
  assert.equal(toString(parse('1.25')), '5/4');
  assert.equal(toString(parse('-0.005')), '-1/200');
  assert.equal(toString(parse('6/-4')), '-3/2');
});

test('exact arithmetic: 1/3 + 1/6 = 1/2', () => {
  assert.equal(toString(add(parse('1/3'), parse('1/6'))), '1/2');
  assert.equal(toString(mul(parse('2/3'), parse('9/4'))), '3/2');
  assert.equal(toString(div(parse('1/2'), parse('3/4'))), '2/3');
  assert.equal(toString(sub(parse('0.1'), parse('0.3'))), '-1/5');
});

test('huge integers stay exact, never through Number', () => {
  const big = parse('123456789012345678901234567890');
  const one = make(1n);
  assert.equal(toString(add(big, one)), '123456789012345678901234567891');
  // 2^60 + 1 is not representable as a Number; arithmetic must stay exact
  const x = make(2n ** 60n + 1n);
  assert.equal(toString(mul(x, make(2n))), (2n ** 61n + 2n).toString());
  assert.equal(toString(div(big, parse('3'))), '41152263004115226300411522630');
});

test('comparison and floor on negatives', () => {
  assert.equal(cmp(parse('-3/2'), parse('-1')), -1);
  assert.equal(floorBig(parse('-3/2')), -2n);
  assert.equal(floorBig(parse('3/2')), 1n);
  assert.equal(floorBig(parse('-2')), -2n);
});

test('display strings', () => {
  assert.equal(toDisplayString(parse('5')), '5');
  assert.equal(toDisplayString(parse('2.5')), '2.5');
  assert.equal(toDisplayString(parse('1/3')), '1/3');
  assert.equal(toDisplayString(parse('-0.01')), '-0.01');
});
