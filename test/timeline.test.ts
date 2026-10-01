import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Timeline } from '../src/timeline.ts';
import { ntscDropFrame } from '../src/dropframe.ts';
import { ratToString } from '../src/rational.ts';
import { FPS_29_97, FPS_24 } from '../src/rate.ts';

const SR = 48000n;

// Splice across rule versions: a 29.97 drop-frame segment followed by a
// 24 fps non-drop segment.
const tl = new Timeline([
  { name: 'opening', rate: FPS_29_97, rule: ntscDropFrame(30), frames: 1800 },
  { name: 'body', rate: FPS_24, rule: null, frames: 1000 },
]);

test('total frames and segment location', () => {
  assert.equal(tl.totalFrames, 2800n);
  assert.deepEqual(tl.locate(0n), { index: 0, segment: 'opening', localFrame: 0n, label: '00:00:00;00' });
  assert.deepEqual(tl.locate(1799n), { index: 0, segment: 'opening', localFrame: 1799n, label: '00:00:59;29' });
  assert.deepEqual(tl.locate(1800n), { index: 1, segment: 'body', localFrame: 0n, label: '00:00:00:00' });
  assert.deepEqual(tl.locate(2799n), { index: 1, segment: 'body', localFrame: 999n, label: '00:00:41:15' });
  assert.throws(() => tl.locate(2800n), RangeError);
  assert.throws(() => tl.locate(-1n), RangeError);
});

test('each segment labels with its own rule version', () => {
  // opening uses drop-frame ';' labels, body uses non-drop ':' labels
  assert.equal(tl.labelAt(1799n), '00:00:59;29');
  assert.equal(tl.labelAt(1800n), '00:00:00:00');
  // body local 984 = 41s at 24fps, non-drop, no skipped labels
  assert.equal(tl.labelAt(2784n), '00:00:41:00');
});

test('boundary sample positions are exact rationals', () => {
  const bounds = tl.boundarySamples(SR);
  assert.equal(bounds.length, 3);
  assert.equal(ratToString(bounds[0]), '0');
  // 1800 frames * 48000 * 1001 / 30000 = 2882880 exactly
  assert.equal(ratToString(bounds[1]), '2882880');
  // + 1000 frames * 2000 samples = 4882880
  assert.equal(ratToString(bounds[2]), '4882880');
});

test('aligned boundaries carry rational residuals', () => {
  const oneFrame = new Timeline([{ rate: FPS_29_97, frames: 1 }]);
  const aligned = oneFrame.boundarySamplesAligned(SR, 'floor');
  assert.equal(aligned[1].samples, 1601n);
  assert.equal(ratToString(aligned[1].error), '-3/5');
  const up = oneFrame.boundarySamplesAligned(SR, 'ceil');
  assert.equal(up[1].samples, 1602n);
  assert.equal(ratToString(up[1].error), '2/5');
});

test('audio sample alignment across the splice point', () => {
  // exactly on the boundary: first frame of body, zero error
  const at = tl.locateBySamples(2882880n, SR, 'nearest-half-even');
  assert.equal(at.index, 1);
  assert.equal(at.localFrame, 0n);
  assert.equal(at.label, '00:00:00:00');
  assert.equal(ratToString(at.sampleError), '0');
  // near the last frame of opening: frame 1799 sits at exactly 14406392/5 samples
  const before = tl.locateBySamples(2881278n, SR, 'nearest-half-even');
  assert.equal(before.index, 0);
  assert.equal(before.localFrame, 1799n);
  assert.equal(before.label, '00:00:59;29');
  assert.equal(ratToString(before.sampleError), '-2/5');
  // one sample before the boundary rounds forward and carries into the next segment
  const carry = tl.locateBySamples(2882879n, SR, 'nearest-half-even');
  assert.equal(carry.index, 1);
  assert.equal(carry.localFrame, 0n);
  assert.equal(ratToString(carry.sampleError), '-1');
  // inside body: 1999 samples past the boundary -> frame 1 (2000 samples), error -1
  const inside = tl.locateBySamples(2884879n, SR, 'nearest-half-even');
  assert.equal(inside.index, 1);
  assert.equal(inside.localFrame, 1n);
  assert.equal(ratToString(inside.sampleError), '-1');
  assert.equal(ratToString(inside.exactLocalFrames), '1999/2000');
  assert.throws(() => tl.locateBySamples(4882880n, SR, 'floor'), RangeError);
});

test('timeline validation', () => {
  assert.throws(() => new Timeline([]), RangeError);
  assert.throws(() => new Timeline([{ rate: FPS_24, frames: 0 }]), RangeError);
});
