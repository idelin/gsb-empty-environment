import { test } from 'node:test';
import assert from 'node:assert/strict';
import { KeyframeAnimation } from '../src/animation.ts';

const linearTrack = [
  { offset: 0, props: { x: 0 } },
  { offset: 1, props: { x: 100 } },
];

test('keyframes + easing combine: eased interpolation between offsets', () => {
  const anim = new KeyframeAnimation({
    keyframes: [
      { offset: 0, easing: 'ease-in', props: { x: 0 } },
      { offset: 1, props: { x: 100 } },
    ],
    duration: 2,
  });
  assert.equal(anim.valueAt(0).x, 0);
  assert.equal(anim.valueAt(1).x, 25); // easeInQuad(0.5) = 0.25
  assert.equal(anim.valueAt(2).x, 100);
});

test('held segments before the first and after the last offset', () => {
  const anim = new KeyframeAnimation({
    keyframes: [
      { offset: 0.25, props: { x: 10 } },
      { offset: 0.75, props: { x: 20 } },
    ],
    duration: 4,
  });
  assert.equal(anim.valueAt(0).x, 10);
  assert.equal(anim.valueAt(2).x, 15);
  assert.equal(anim.valueAt(4).x, 20);
});

test('repetition and direction: alternate reverses odd iterations by time', () => {
  const anim = new KeyframeAnimation({
    keyframes: linearTrack,
    duration: 1,
    iterations: 2,
    direction: 'alternate',
  });
  assert.equal(anim.valueAt(0.25).x, 25);
  assert.equal(anim.valueAt(1.25).x, 75); // mirrored, not value-swapped
  assert.equal(anim.valueAt(1.75).x, 25);
});

test('reverse direction is exact time reversal of the forward curve', () => {
  const keyframes = [
    { offset: 0, easing: 'ease-in', props: { x: 0 } },
    { offset: 1, props: { x: 100 } },
  ];
  const forward = new KeyframeAnimation({ keyframes, duration: 2 });
  const reversed = new KeyframeAnimation({ keyframes, duration: 2, direction: 'reverse' });
  for (const t of [0, 0.3, 0.5, 1.1, 1.9, 2]) {
    assert.equal(reversed.valueAt(t).x, forward.valueAt(2 - t).x);
  }
  // A value-swap approximation (swap endpoints, keep easing forward) would
  // give 100 * (1 - 0.25^2) = 93.75 at t=0.5; exact time reversal gives 56.25.
  assert.equal(reversed.valueAt(0.5).x, 56.25);
});

test('exact period boundary does not skip an extra round', () => {
  const anim = new KeyframeAnimation({ keyframes: linearTrack, duration: 1, iterations: 2 });
  // active time == totalDuration resolves to the end state of the last
  // iteration (x=100), not the start of a non-existent third round (x=0).
  assert.equal(anim.valueAt(2).x, 100);
  assert.equal(anim.valueAt(2.5).x, 100);
  // Mid-animation exact boundary starts the next round at progress 0.
  const three = new KeyframeAnimation({ keyframes: linearTrack, duration: 1, iterations: 3 });
  assert.equal(three.valueAt(1).x, 0);
  assert.equal(three.valueAt(1.5).x, 50);
});

test('negative delay starts the animation partway', () => {
  const anim = new KeyframeAnimation({ keyframes: linearTrack, duration: 1, delay: -0.5 });
  assert.equal(anim.valueAt(0).x, 50);
  assert.equal(anim.valueAt(0.25).x, 75);
  assert.equal(anim.valueAt(0.5).x, 100);
});

test('zero duration is defined: start state before delay, end state at/after', () => {
  const anim = new KeyframeAnimation({ keyframes: linearTrack, duration: 0, delay: 1 });
  assert.equal(anim.valueAt(0.999).x, 0);
  assert.equal(anim.valueAt(1).x, 100);
  assert.equal(anim.valueAt(1000).x, 100);
});

test('infinite iterations keep cycling', () => {
  const anim = new KeyframeAnimation({
    keyframes: linearTrack,
    duration: 1,
    iterations: Infinity,
  });
  assert.equal(anim.valueAt(100.5).x, 50);
  assert.equal(anim.valueAt(1e6 + 0.25).x, 25);
});

test('keyframe events fire per iteration at exact local times', () => {
  const anim = new KeyframeAnimation({
    keyframes: linearTrack,
    duration: 1,
    iterations: 2,
    keyframeEvents: true,
  });
  const events = anim.eventsInInterval(0, 2);
  // offset 0 of iteration 0 sits exactly on the interval start: excluded.
  // The two events at t=1 keep deterministic insertion (iteration-major) order.
  assert.deepEqual(
    events.map((e) => [e.time, (e.data as { offset: number }).offset]),
    [
      [1, 1],
      [1, 0],
      [2, 1],
    ],
  );
  // Crossing the exact total-duration boundary fires no extra round.
  assert.deepEqual(anim.eventsInInterval(2, 3), []);
});
