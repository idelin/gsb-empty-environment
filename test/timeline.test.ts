import { test } from 'node:test';
import assert from 'node:assert/strict';
import { KeyframeAnimation } from '../src/animation.ts';
import { Clock } from '../src/clock.ts';
import { Player } from '../src/player.ts';
import { Timeline } from '../src/timeline.ts';

// Shared nested fixture:
//   root: animA (x 0->100 over 2) at 0, inner timeline at 2,
//         pause segment [1, 1.5), marker "mid" at playhead 1
//   inner: animB (y 0->10 over 1) at rate -1 (plays end -> start)
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
  return { animA, animB, inner, root };
}

test('pause segments hold the playhead; mapping is a pure function of input time', () => {
  const { root } = buildRoot();
  assert.equal(root.playhead(0.5), 0.5);
  assert.equal(root.playhead(1), 1);
  assert.equal(root.playhead(1.25), 1); // inside the pause segment
  assert.equal(root.playhead(1.5), 1);
  assert.equal(root.playhead(2), 1.5);
  assert.equal(root.duration, 3.5); // playhead 3 + the 0.5 pause
});

test('overlapping pause segments are rejected', () => {
  const tl = new Timeline().addPause(1, 2);
  assert.throws(() => tl.addPause(2, 1));
  tl.addPause(3, 1); // adjacent is fine
  assert.equal(tl.pauses.length, 2);
});

test('nested timeline with negative rate plays the child end -> start', () => {
  const { root } = buildRoot();
  assert.equal(root.valueAt(0).y, 10); // before the window: first frame in play order
  assert.equal(root.valueAt(2.5).y, 10); // window opens: local time 1
  assert.equal(root.valueAt(3).y, 5); // halfway through the reversed child
  assert.equal(root.valueAt(3.5).y, 0); // window closes: local time 0
  assert.equal(root.valueAt(4).y, 0); // after the window: last frame holds
});

test('values are identical for arbitrary seeking and sequential advancement', () => {
  const { root } = buildRoot();
  const { clock } = { clock: new Clock(() => 0) };
  const player = new Player(root, clock);
  const times = [0, 0.5, 1, 1.25, 1.5, 2, 2.5, 3, 3.5, 4];
  for (const t of times) player.advanceTo(t);
  // Re-walk sequentially and compare against direct evaluation at each time.
  const sequential = new Player(root, clock);
  for (const t of times) {
    sequential.advanceTo(t);
    assert.deepEqual(sequential.value(), root.valueAt(t));
  }
  // Pause segment: every input time inside the pause gives the held value.
  assert.deepEqual(root.valueAt(1.25), root.valueAt(1));
  assert.deepEqual(root.valueAt(1.499), root.valueAt(1));
});

test('time scaling composes exactly: player scale x child rate, no accumulation', () => {
  const anim = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { x: 0 } },
      { offset: 1, props: { x: 1000 } },
    ],
    duration: 100,
  });
  const root = new Timeline().add(anim, 0, 0.5);
  let wall = 0;
  const clock = new Clock(() => wall);
  clock.start();
  const player = new Player(root, clock);
  player.timeScale = 2;
  wall = 10;
  player.tick();
  // root time = 10 * 2 = 20, child local = 20 * 0.5 = 10 -> x = 100
  assert.equal(player.value().x, 100);
  assert.equal(player.value().x, anim.valueAt(10).x);

  // 30 sequential ticks of 1 time unit at scale 0.1: the position is always
  // the exact product clock*timeScale, never a sum of 30 rounded deltas.
  player.timeScale = 0.1;
  for (let i = 0; i < 20; i++) {
    wall += 1;
    player.tick();
  }
  assert.equal(player.position, wall * 0.1);
  assert.deepEqual(player.value(), root.valueAt(wall * 0.1));
});

test('nested pause segments compose through child time mapping', () => {
  const anim = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { x: 0 } },
      { offset: 1, props: { x: 200 } },
    ],
    duration: 2,
  });
  const inner = new Timeline().add(anim, 0, 1).addPause(0.5, 0.5);
  const root = new Timeline().add(inner, 0, 1);
  // The inner pause occupies input [0.5, 1.0): the playhead holds at 0.5.
  assert.equal(root.valueAt(0.75).x, 50); // inside the inner pause: held
  assert.equal(root.valueAt(1).x, 50); // pause just ended, playhead 0.5
  assert.equal(root.valueAt(1.25).x, 75); // playhead 0.75
  assert.equal(root.valueAt(1.5).x, 100); // playhead 1
});
