import { test } from 'node:test';
import assert from 'node:assert/strict';
import { normalCdf } from '../src/math.ts';

test('normalCdf matches known standard normal values', () => {
  assert.ok(Math.abs(normalCdf(0, 0, 1) - 0.5) < 1e-7);
  assert.ok(Math.abs(normalCdf(1.96, 0, 1) - 0.9750021) < 1e-4);
  assert.ok(Math.abs(normalCdf(-1.96, 0, 1) - 0.0249979) < 1e-4);
  assert.ok(Math.abs(normalCdf(3, 0, 1) - 0.9986501) < 1e-4);
});

test('normalCdf shifts with mean and scales with stdDeviation', () => {
  assert.ok(Math.abs(normalCdf(200, 100, 50) - normalCdf(2, 0, 1)) < 1e-12);
});
