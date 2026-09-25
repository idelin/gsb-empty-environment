import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ticks } from '../src/time.ts';
import { KeyframeTrack } from '../src/keyframes.ts';
import { easeInCubic, steps } from '../src/easing.ts';

test('linear interpolation and clamping', () => {
  const track = new KeyframeTrack('x', [
    { at: 0, value: 0 },
    { at: ticks(1000), value: 100 },
    { at: ticks(2000), value: 200 },
  ]);
  assert.equal(track.sample(ticks(500)), 50);
  assert.equal(track.sample(ticks(-10)), 0);
  assert.equal(track.sample(ticks(5000)), 200);
  assert.equal(track.sample(ticks(1500)), 150);
});

test('easing shapes the segment it leaves', () => {
  const track = new KeyframeTrack('x', [
    { at: 0, value: 0, easing: easeInCubic },
    { at: ticks(1000), value: 100 },
  ]);
  assert.equal(track.sample(ticks(500)), 100 * 0.125);
});

test('steps easing jumps discretely', () => {
  const track = new KeyframeTrack('x', [
    { at: 0, value: 0, easing: steps(4) },
    { at: ticks(1000), value: 100 },
  ]);
  assert.equal(track.sample(ticks(300)), 25);
  assert.equal(track.sample(ticks(999)), 75);
  assert.equal(track.sample(ticks(1000)), 100);
});

test('zero-length segment resolves to the next value', () => {
  const track = new KeyframeTrack('x', [
    { at: 0, value: 0 },
    { at: ticks(500), value: 10 },
    { at: ticks(500), value: 20 },
    { at: ticks(1000), value: 30 },
  ]);
  assert.equal(track.sample(ticks(500)), 20);
  assert.equal(track.sample(ticks(499)), 9.98);
});

test('hot-swap replaces keyframes and bumps the version', () => {
  const track = new KeyframeTrack('x', [
    { at: 0, value: 0 },
    { at: ticks(1000), value: 100 },
  ]);
  const v1 = track.version;
  assert.equal(track.sample(ticks(500)), 50);
  track.replaceKeyframes([
    { at: 0, value: 1000 },
    { at: ticks(1000), value: 2000 },
  ]);
  assert.ok(track.version > v1);
  assert.equal(track.sample(ticks(500)), 1500);
});
