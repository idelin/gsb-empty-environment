import { test } from 'node:test';
import assert from 'node:assert/strict';
import { KeyframeAnimation } from '../src/animation.ts';
import { Timeline } from '../src/timeline.ts';

test('reduced motion yields a deterministic constant output for any time', () => {
  const anim = new KeyframeAnimation({
    keyframes: [
      { offset: 0, easing: 'ease-in', props: { x: 0 } },
      { offset: 1, props: { x: 100 } },
    ],
    duration: 2,
    iterations: 3,
    direction: 'alternate',
    reducedMotion: true,
  });
  const end = anim.endState();
  for (const t of [-100, -0.5, 0, 0.3, 1.7, 5.999, 6, 1e6]) {
    assert.deepEqual(anim.valueAt(t), end);
    assert.deepEqual(anim.valueAt(t), anim.valueAt(t));
  }
  // alternate with 3 iterations ends on a forward iteration: x = 100.
  assert.equal(end.x, 100);
});

test('reduced motion suppresses event delivery', () => {
  const anim = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { x: 0 } },
      { offset: 1, props: { x: 1 } },
    ],
    duration: 1,
    keyframeEvents: true,
    reducedMotion: true,
  });
  anim.addEvent(0.5, 'mid');
  assert.deepEqual(anim.eventsInInterval(0, 10), []);
  const tl = new Timeline().add(anim, 0, 1).addMarker(0.5, 'marker');
  tl.reducedMotion = true;
  assert.deepEqual(tl.collectEvents(0, 10), []);
});
