import { test } from 'node:test';
import assert from 'node:assert/strict';
import { frameToSamples, samplesToFrame } from '../src/samples.ts';
import { ratToString } from '../src/rational.ts';
import { FPS_29_97, FPS_24 } from '../src/rate.ts';

const SR = 48000n;

test('29.97 fps: one frame is exactly 8008/5 samples (1601.6)', () => {
  const r = frameToSamples(1n, FPS_29_97, SR, 'floor');
  assert.equal(ratToString(r.exact), '8008/5');
  assert.equal(r.samples, 1601n);
  assert.equal(ratToString(r.error), '-3/5'); // 1601 - 1601.6
  const up = frameToSamples(1n, FPS_29_97, SR, 'nearest-half-away');
  assert.equal(up.samples, 1602n);
  assert.equal(ratToString(up.error), '2/5');
});

test('exact boundaries have zero error in every mode', () => {
  // 5 frames at 30000/1001 = 5*8008/5 = 8008 samples exactly
  for (const mode of ['floor', 'ceil', 'trunc', 'nearest-half-up', 'nearest-half-away', 'nearest-half-even'] as const) {
    const r = frameToSamples(5n, FPS_29_97, SR, mode);
    assert.equal(r.samples, 8008n, mode);
    assert.equal(ratToString(r.error), '0', mode);
  }
});

test('samples -> frame with explicit rounding and rational error', () => {
  const down = samplesToFrame(1601n, FPS_29_97, SR, 'floor');
  assert.equal(down.frame, 0n);
  assert.equal(ratToString(down.error), '-8005/8008'); // 0 - 1601*30000/(48000*1001)
  const near = samplesToFrame(1601n, FPS_29_97, SR, 'nearest-half-away');
  assert.equal(near.frame, 1n);
  // 1601 samples is 3/8008 of a frame short of 1
  assert.equal(ratToString(near.error), '3/8008');
});

test('negative samples: floor/trunc/ceil differ exactly as specified', () => {
  assert.equal(samplesToFrame(-1n, FPS_29_97, SR, 'floor').frame, -1n);
  assert.equal(samplesToFrame(-1n, FPS_29_97, SR, 'trunc').frame, 0n);
  assert.equal(samplesToFrame(-1n, FPS_29_97, SR, 'ceil').frame, 0n);
  assert.equal(frameToSamples(-1n, FPS_29_97, SR, 'floor').samples, -1602n);
  assert.equal(frameToSamples(-1n, FPS_29_97, SR, 'ceil').samples, -1601n);
});

test('round-trip consistency: frame -> samples -> frame at 24 fps', () => {
  // 24 fps at 48 kHz is exactly 2000 samples/frame, so round-trip is lossless
  for (let f = 0n; f < 1000n; f += 7n) {
    const s = frameToSamples(f, FPS_24, SR, 'nearest-half-even');
    const back = samplesToFrame(s.samples, FPS_24, SR, 'nearest-half-even');
    assert.equal(back.frame, f);
  }
});

test('big values: no Number intermediary, exact beyond 2^53', () => {
  const f = 2n ** 60n;
  const r = frameToSamples(f, FPS_29_97, SR, 'floor');
  // expected = floor(f * 48000 * 1001 / 30000) computed independently
  const expected = (f * 48000n * 1001n) / 30000n;
  assert.equal(r.samples, expected);
  assert.equal(ratToString(r.exact), `${f * 8008n}/5`);
  // nearest-mode round trip recovers the frame (sub-sample residual << 1/2 frame)
  const back = samplesToFrame(r.samples, FPS_29_97, SR, 'nearest-half-even');
  assert.equal(back.frame, f);
});
