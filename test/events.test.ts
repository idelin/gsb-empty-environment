import { test } from 'node:test';
import assert from 'node:assert/strict';
import { KeyframeAnimation } from '../src/animation.ts';
import { Clock } from '../src/clock.ts';
import { Player } from '../src/player.ts';
import { Timeline } from '../src/timeline.ts';

function buildRoot() {
  const animA = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { x: 0 } },
      { offset: 1, props: { x: 100 } },
    ],
    duration: 2,
    keyframeEvents: true,
  });
  const animB = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { y: 0 } },
      { offset: 1, props: { y: 10 } },
    ],
    duration: 1,
    keyframeEvents: true,
  });
  const inner = new Timeline().add(animB, 0, -1);
  const root = new Timeline().add(animA, 0, 1).add(inner, 2, 1).addPause(1, 0.5).addMarker(1, 'mid');
  return { root };
}

const summarize = (events: { name: string; time: number; direction: number }[]) =>
  events.map((e) => `${e.name}@${e.time}(${e.direction})`);

test('cross-interval semantics: forward (t0, t1], backward [t1, t0)', () => {
  const anim = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { x: 0 } },
      { offset: 1, props: { x: 1 } },
    ],
    duration: 4,
  });
  anim.addEvent(0, 'zero');
  anim.addEvent(2, 'two');
  anim.addEvent(4, 'four');
  assert.deepEqual(anim.eventsInInterval(0, 4).map((e) => e.name), ['two', 'four']);
  // Backward travel delivers in the order the playhead encounters events.
  assert.deepEqual(anim.eventsInInterval(4, 0).map((e) => e.name), ['two', 'zero']);
  assert.deepEqual(anim.eventsInInterval(2, 2), []);
});

test('boundary events are delivered exactly once across adjacent advances', () => {
  const anim = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { x: 0 } },
      { offset: 1, props: { x: 1 } },
    ],
    duration: 4,
  });
  anim.addEvent(1, 'boundary');
  const player = new Player(anim, new Clock(() => 0));
  assert.deepEqual(player.advanceTo(1).map((e) => e.name), ['boundary']);
  assert.deepEqual(player.advanceTo(2), []); // not re-fired
  // Crossing it again backwards is a new crossing: delivered with direction -1.
  const back = player.advanceTo(1);
  assert.deepEqual(back.map((e) => e.name), ['boundary']);
  assert.equal(back[0].direction, -1);
  assert.deepEqual(player.advanceTo(1), []); // no movement, no delivery
});

test('nested events map to root input time across pauses and negative rates', () => {
  const { root } = buildRoot();
  const player = new Player(root, new Clock(() => 0));
  const delivered = player.advanceTo(3.5);
  assert.deepEqual(summarize(delivered), [
    'mid@1(1)', // marker at playhead 1
    'keyframe@2.5(1)', // animA offset-1 keyframe, playhead 2 -> input 2.5
    'keyframe@3.5(-1)', // animB offset-0 keyframe, crossed backwards
  ]);
});

test('sequential small steps and one big seek deliver the same event stream', () => {
  const stepped = new Player(buildRoot().root, new Clock(() => 0));
  for (const t of [0.7, 1.2, 2.5, 3.5]) stepped.advanceTo(t);
  const jumped = new Player(buildRoot().root, new Clock(() => 0));
  jumped.seek(3.5);
  assert.deepEqual(summarize(stepped.events), summarize(jumped.events));
});

test('boundary events do not re-fire when advancing past a period edge', () => {
  const { root } = buildRoot();
  const player = new Player(root, new Clock(() => 0));
  assert.deepEqual(summarize(player.advanceTo(2.5)), ['mid@1(1)', 'keyframe@2.5(1)']);
  assert.deepEqual(summarize(player.advanceTo(3.5)), ['keyframe@3.5(-1)']);
  assert.deepEqual(player.advanceTo(3.5), []); // no movement, no events
});

test('events carry the path of the node that produced them', () => {
  const { root } = buildRoot();
  const player = new Player(root, new Clock(() => 0));
  const delivered = player.advanceTo(3.5);
  assert.equal(delivered[0].path, 'timeline');
  assert.equal(delivered[1].path, 'timeline/child[0]');
  assert.equal(delivered[2].path, 'timeline/child[1]/child[0]');
});
