import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ticks } from '../src/time.ts';
import { KeyframeTrack } from '../src/keyframes.ts';
import { Animation } from '../src/animation.ts';
import { Timeline } from '../src/timeline.ts';
import { Player } from '../src/player.ts';
import { sortForDelivery } from '../src/events.ts';
import type { TimelineEvent } from '../src/events.ts';
import { mulberry32, pick, intBetween } from './rng.util.ts';

function buildRoot(): Timeline {
  const slide = new Animation(
    'slide',
    [
      new KeyframeTrack('x', [
        { at: 0, value: 0, event: 'x0' },
        { at: ticks(300), value: 30, event: 'x300' },
        { at: ticks(1000), value: 100, event: 'x1000' },
      ]),
    ],
    { duration: ticks(1000), iterations: 3, direction: 'alternate', fill: 'both' },
  );
  const spin = new Animation(
    'spin',
    [
      new KeyframeTrack('r', [
        { at: 0, value: 0, event: 'r0' },
        { at: ticks(400), value: 180, event: 'r400' },
        { at: ticks(800), value: 360, event: 'r800' },
      ]),
    ],
    { duration: ticks(800), iterations: 4, fill: 'both' },
  );
  const group = new Timeline('group');
  group.add({ child: slide, offset: ticks(200), rate: 1 });
  group.add({
    child: spin,
    offset: ticks(-100),
    rate: -1.5,
    pauses: [{ start: ticks(600), duration: ticks(300) }],
  });
  const root = new Timeline('root');
  root.add({ child: group, offset: ticks(50), rate: 2 });
  root.addEvent('mark', ticks(500));
  return root;
}

test('auto-generated time trajectories match direct evaluation', () => {
  const rng = mulberry32(20260926);
  const root = buildRoot();
  let now = 0;
  const player = new Player(root, () => now);
  const fresh = new Player(root, () => 0);
  let rate = 1;
  let scale = 1;
  let playing = true;
  let expected = 0; // exact integer-tick model of the player's clock
  for (let step = 0; step < 600; step++) {
    const op = pick(rng, ['advance', 'advance', 'advance', 'pause', 'resume', 'seek', 'rate', 'scale'] as const);
    switch (op) {
      case 'advance': {
        const d = intBetween(rng, 0, 90);
        now += d;
        if (playing) expected += Math.round(d * 1000 * rate * scale);
        break;
      }
      case 'pause':
        if (playing) {
          player.pause();
          playing = false;
        }
        break;
      case 'resume':
        if (!playing) {
          player.resume();
          playing = true;
        }
        break;
      case 'seek': {
        const target = ticks(intBetween(rng, -500, 4000));
        player.seek(target);
        expected = target;
        break;
      }
      case 'rate':
        rate = pick(rng, [0.5, 1, 1.5, 2, -0.5, -1] as const);
        player.setPlaybackRate(rate);
        break;
      case 'scale':
        scale = pick(rng, [0.5, 1, 2, 4] as const);
        player.setTimeScale(scale);
        break;
    }
    // The anchored clock never drifts from the exact model...
    assert.equal(player.currentTicks, expected, `tick drift at step ${step} (${op})`);
    // ...sequential advancement equals direct evaluation at the same tick...
    assert.deepStrictEqual(player.sample(), player.sampleAt(expected), `sample mismatch at step ${step}`);
    // ...and random access from a fresh player agrees too.
    fresh.seek(expected);
    assert.deepStrictEqual(fresh.sample(), player.sample(), `random access mismatch at step ${step}`);
  }
});

test('crossing intervals concatenate exactly (forward and backward)', () => {
  const rng = mulberry32(777);
  const root = buildRoot();
  for (let trial = 0; trial < 100; trial++) {
    const points = [0, 0, 0]
      .map(() => ticks(intBetween(rng, -1000, 5000)))
      .sort((a, b) => a - b);
    const [t0, t1, t2] = points;
    // Forward: (t0, t1] + (t1, t2] == (t0, t2]
    const ab: TimelineEvent[] = [];
    const bc: TimelineEvent[] = [];
    const ac: TimelineEvent[] = [];
    root.collectEvents(t0, t1, ab, 'root');
    root.collectEvents(t1, t2, bc, 'root');
    root.collectEvents(t0, t2, ac, 'root');
    assert.deepStrictEqual(
      sortForDelivery([...ab, ...bc], t0, t2),
      sortForDelivery(ac, t0, t2),
      `forward concat failed for ${t0},${t1},${t2}`,
    );
    // Backward: [t1, t0) + [t2, t1) == [t2, t0)
    const ba: TimelineEvent[] = [];
    const cb: TimelineEvent[] = [];
    const ca: TimelineEvent[] = [];
    root.collectEvents(t0, t1, ba, 'root');
    root.collectEvents(t1, t2, cb, 'root');
    root.collectEvents(t0, t2, ca, 'root');
    assert.deepStrictEqual(
      sortForDelivery([...ba, ...cb], t0, t2),
      sortForDelivery(ca, t0, t2),
      `backward concat failed for ${t0},${t1},${t2}`,
    );
  }
});

test('stepping through boundaries never re-fires an event', () => {
  const root = buildRoot();
  const rng = mulberry32(42);
  let now = 0;
  const stepped = new Player(root, () => now);
  stepped.seek(0);
  const seen = new Map<string, number>();
  const all: TimelineEvent[] = [];
  while (now < 4000) {
    now += intBetween(rng, 1, 200);
    for (const e of stepped.drainEvents()) {
      const key = `${e.path}|${e.name}|${e.at}`;
      seen.set(key, (seen.get(key) ?? 0) + 1);
      all.push(e);
    }
  }
  for (const [key, count] of seen) {
    assert.equal(count, 1, `event fired ${count} times: ${key}`);
  }
  const single: TimelineEvent[] = [];
  root.collectEvents(0, ticks(4000), single, 'root');
  assert.deepStrictEqual(
    sortForDelivery(all, 0, 1),
    sortForDelivery(single, 0, 1),
    'stepped delivery differs from single-interval delivery',
  );
});

test('hot-swap invalidates old events and keeps the position resumable', () => {
  const fade = new Animation(
    'fade',
    [
      new KeyframeTrack('opacity', [
        { at: 0, value: 0 },
        { at: ticks(500), value: 0.5, event: 'old-half' },
        { at: ticks(1000), value: 1 },
      ]),
    ],
    { duration: ticks(1000), fill: 'both' },
  );
  let now = 0;
  const player = new Player(fade, () => now);
  player.seek(0);
  now = 250;
  assert.equal(player.sample().opacity, 0.25);
  const versionBefore = fade.version;
  const positionBefore = player.currentTicks;

  fade.replaceTrack(
    new KeyframeTrack('opacity', [
      { at: 0, value: 10, event: 'new-start' },
      { at: ticks(500), value: 20, event: 'new-half' },
      { at: ticks(1000), value: 30 },
    ]),
  );

  assert.ok(fade.version > versionBefore);
  // Playback position survives the swap and playback can simply resume.
  assert.equal(player.currentTicks, positionBefore);
  // Sampling immediately reflects the new keyframes.
  assert.equal(player.sampleAt(ticks(500)).opacity, 20);
  // Old events are invalid: only new-version events are delivered.
  now = 600;
  const events = player.drainEvents();
  assert.deepStrictEqual(events.map((e) => e.name), ['new-half']);
  assert.ok(events.every((e) => e.version === fade.version));
  const full: TimelineEvent[] = [];
  fade.collectEvents(-1, ticks(1000), full, 'fade');
  assert.deepStrictEqual(full.map((e) => e.name), ['new-start', 'new-half']);
});
