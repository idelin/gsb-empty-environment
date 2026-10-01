import test from 'node:test';
import assert from 'node:assert/strict';
import { srgbByteToLinear, linearToSrgbByte } from '../src/index.ts';

test('sRGB roundtrip is exact for all byte values', () => {
  for (let b = 0; b < 256; b++) {
    assert.equal(linearToSrgbByte(srgbByteToLinear(b)), b);
  }
});

test('sRGB endpoints', () => {
  assert.equal(srgbByteToLinear(0), 0);
  assert.equal(srgbByteToLinear(255), 1);
  assert.equal(linearToSrgbByte(0), 0);
  assert.equal(linearToSrgbByte(1), 255);
});

test('linear midpoint encodes to 188, not 128 (filtering happens in linear space)', () => {
  assert.equal(linearToSrgbByte(0.5), 188);
});
