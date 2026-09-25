import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ticks } from '../src/time.ts';
import { KeyframeTrack } from '../src/keyframes.ts';
import { Animation } from '../src/animation.ts';
import { Timeline } from '../src/timeline.ts';
import { Player } from '../src/player.ts';

function fadeAnimation(): Animation {
  return new Animation(
    'fade',
    [
      new KeyframeTrack('opacity', [
        { at: 0, value: 0, event: 'start' },
        { at: ticks(500), value: 0.5, event: 'half' },
        { at: ticks(1000), value: 1, event: 'done' },
      ]),
    ],
    { duration: ticks(1000), fill: 'both' },
  );
}

test('negative child rate plays the child backwards in parent time', () => {
  const fade = fadeAnimation();
  const root = new Timeline('root');
  root.add({ child: fade, rate: -1 });
  // startAt defaults to the child duration: parent t maps to 1000ms - t.
  const player = new Player(root, () => 0);
  assert.deepStrictEqual(player.sampleAt(ticks(250)), { opacity: 0.75 });
  const direct = new Player(fade, () => 0);
  assert.deepStrictEqual(player.sampleAt(ticks(250)), direct.sampleAt(ticks(750)));
});

test('pause segments freeze the child while parent time advances', () => {
  const root = new Timeline('root');
  const spec = {
    child: fadeAnimation(),
    offset: 0,
    pauses: [{ start: ticks(400), duration: ticks(200) }],
  };
  assert.equal(root.childLocalAt(spec, ticks(399)), ticks(399));
  assert.equal(root.childLocalAt(spec, ticks(400)), ticks(400));
  assert.equal(root.childLocalAt(spec, ticks(599)), ticks(400));
  assert.equal(root.childLocalAt(spec, ticks(600)), ticks(400));
  assert.equal(root.childLocalAt(spec, ticks(700)), ticks(500));
});

test('arbitrary seeking and sequential advancement give identical values', () => {
  const slide = new Animation(
    'slide',
    [
      new KeyframeTrack('x', [
        { at: 0, value: 0 },
        { at: ticks(300), value: 30 },
        { at: ticks(1000), value: 100 },
      ]),
    ],
    { duration: ticks(1000), iterations: 3, direction: 'alternate', fill: 'both' },
  );
  const group = new Timeline('group');
  group.add({ child: slide, offset: ticks(200), rate: 1 });
  group.add({
    child: fadeAnimation(),
    offset: ticks(-100),
    rate: -1.5,
    pauses: [{ start: ticks(600), duration: ticks(300) }],
  });
  const root = new Timeline('root');
  root.add({ child: group, offset: ticks(50), rate: 2 });

  const player = new Player(root, () => 0);
  for (let ms = -300; ms <= 3000; ms += 37) {
    const a = player.sampleAt(ticks(ms));
    const b = player.sampleAt(ticks(ms));
    assert.deepStrictEqual(a, b);
  }
});

test('nested events carry paths and respect child offsets', () => {
  const root = new Timeline('root');
  root.add({ child: fadeAnimation(), offset: ticks(100) });
  root.addEvent('root-mark', ticks(250));
  let now = 0;
  const player = new Player(root, () => now);
  player.seek(0);
  now = 600;
  const events = player.drainEvents();
  assert.deepStrictEqual(
    events.map((e) => [e.name, e.path, e.at]),
    [
      ['start', 'root/fade', ticks(100)],
      ['root-mark', 'root', ticks(250)],
      ['half', 'root/fade', ticks(600)],
    ],
  );
});

test('timeline duration accounts for offsets, rates and pauses', () => {
  const root = new Timeline('root');
  root.add({ child: fadeAnimation(), offset: ticks(100) }); // ends at 1100ms
  const group = new Timeline('group');
  group.add({
    child: fadeAnimation(),
    offset: ticks(200),
    rate: 2, // finishes in 500ms of parent time
    pauses: [{ start: ticks(300), duration: ticks(100) }],
  }); // ends at 200 + 500 + 100 = 800ms
  root.add({ child: group, offset: ticks(50) });
  assert.equal(root.duration, ticks(1100));
  assert.equal(group.duration, ticks(800));
});
