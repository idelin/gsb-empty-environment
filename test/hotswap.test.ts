import { test } from 'node:test';
import assert from 'node:assert/strict';
import { KeyframeAnimation } from '../src/animation.ts';
import { Clock } from '../src/clock.ts';
import { Player } from '../src/player.ts';

const k1 = [
  { offset: 0, props: { x: 0 } },
  { offset: 1, props: { x: 100 } },
];
const k2 = [
  { offset: 0, props: { x: 0 } },
  { offset: 0.5, props: { x: 500 } },
  { offset: 1, props: { x: 0 } },
];

test('hot-swapping keyframes invalidates old events and resumes position', () => {
  const anim = new KeyframeAnimation({ keyframes: k1, duration: 2, keyframeEvents: true });
  anim.addEvent(1, 'checkpoint');
  const player = new Player(anim, new Clock(() => 0));
  player.advanceTo(1.5);
  assert.deepEqual(
    player.events.map((e) => e.name),
    ['checkpoint'],
  );
  assert.equal(player.value().x, 75);
  const versionBefore = anim.keyframesVersion;
  anim.setKeyframes(k2);
  assert.equal(anim.keyframesVersion, versionBefore + 1);
  // Play position is untouched: the same local time reads the new track.
  assert.equal(player.position, 1.5);
  assert.equal(player.value().x, 250); // k2 at progress 0.75
  // The old 'checkpoint' event (registered against k1) is invalidated; the
  // new track's keyframe events fire instead.
  const delivered = player.advanceTo(2);
  assert.deepEqual(
    delivered.map((e) => [e.name, (e.data as { offset: number }).offset]),
    [['keyframe', 1]],
  );
  assert.ok(!player.events.some((e) => e.name === 'checkpoint' && e.time > 1.5));
});

test('events registered against an older keyframe generation are dropped', () => {
  const anim = new KeyframeAnimation({ keyframes: k1, duration: 2 });
  anim.addEvent(0.5, 'old');
  anim.setKeyframes(k2);
  anim.addEvent(0.5, 'new');
  assert.deepEqual(
    anim.eventsInInterval(0, 2).map((e) => e.name),
    ['new'],
  );
});
