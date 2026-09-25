import { test } from 'node:test';
import assert from 'node:assert/strict';
import { IDENTITY, SRGB_TO_XYZ, apply, determinant, invert, maxAbsDiff, multiply } from '../src/index.ts';
import type { Mat3 } from '../src/index.ts';

test('inverse of sRGB matrix composes to identity', () => {
  const inv = invert(SRGB_TO_XYZ);
  assert.ok(inv, 'sRGB matrix must be invertible');
  assert.ok(maxAbsDiff(multiply(inv, SRGB_TO_XYZ), IDENTITY) < 1e-9);
  assert.ok(maxAbsDiff(multiply(SRGB_TO_XYZ, inv), IDENTITY) < 1e-9);
});

test('singular matrix is reported, not inverted', () => {
  const singular: Mat3 = [1, 2, 3, 2, 4, 6, 7, 8, 9];
  assert.equal(determinant(singular), 0);
  assert.equal(invert(singular), null);
});

test('apply matches manual matrix-vector product', () => {
  const v: [number, number, number] = [0.2, 0.5, 0.8];
  const out = apply(SRGB_TO_XYZ, v);
  assert.ok(Math.abs(out[1] - (0.212639 * 0.2 + 0.7151687 * 0.5 + 0.0721923 * 0.8)) < 1e-12);
});
