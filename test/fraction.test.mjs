import assert from 'node:assert/strict';
import { test } from './harness.mjs';
import {
  makeFraction,
  fractionFromInt,
  fracAdd,
  fracSub,
  fracMul,
  fracDiv,
  fracNeg,
  fracCompare,
  fracMin,
  fracMax,
  fracToString,
} from '../dist/fraction.js';

test('fraction: normalization reduces and fixes sign', () => {
  assert.equal(fracToString(makeFraction(6n, 8n)), '3/4');
  assert.equal(fracToString(makeFraction(1n, -2n)), '-1/2');
  assert.equal(fracToString(makeFraction(-3n, -9n)), '1/3');
  assert.equal(fracToString(makeFraction(0n, 7n)), '0');
  assert.equal(fracToString(makeFraction(10n, 5n)), '2');
});

test('fraction: zero denominator throws', () => {
  assert.throws(() => makeFraction(1n, 0n));
});

test('fraction: arithmetic is exact', () => {
  const a = makeFraction(1n, 3n);
  const b = makeFraction(1n, 6n);
  assert.equal(fracToString(fracAdd(a, b)), '1/2');
  assert.equal(fracToString(fracSub(a, b)), '1/6');
  assert.equal(fracToString(fracMul(a, b)), '1/18');
  assert.equal(fracToString(fracDiv(a, b)), '2');
  assert.equal(fracToString(fracNeg(a)), '-1/3');
});

test('fraction: comparison across denominators', () => {
  const a = makeFraction(2n, 3n);
  const b = makeFraction(5n, 7n);
  assert.equal(fracCompare(a, b), -1);
  assert.equal(fracCompare(b, a), 1);
  assert.equal(fracCompare(a, makeFraction(4n, 6n)), 0);
  assert.equal(fracToString(fracMin(a, b)), '2/3');
  assert.equal(fracToString(fracMax(a, b)), '5/7');
});

test('fraction: large integers stay exact', () => {
  const big = 123456789012345678901234567890n;
  const f = makeFraction(big, 2n);
  assert.equal(fracToString(fracAdd(f, f)), big.toString());
  assert.equal(fracToString(fractionFromInt(-42n)), '-42');
});
