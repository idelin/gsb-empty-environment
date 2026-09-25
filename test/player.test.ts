import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ticks } from '../src/time.ts';
import { KeyframeTrack } from '../src/keyframes.ts';
import { Animation } from '../src/animation.ts';
import { Player } from '../src/player.ts';

function makeAnimation(): Animation {
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

test('pause/resume accumulates zero drift over many cycles', () => {
  let now = 0;
  const player = new Player(makeAnimation(), () => now);
  let expected = 0;
  for (let i = 0; i < 1000; i++) {
    now += 1; // 1ms of playback
    expected += 1000; // 1ms = 1000 ticks
    const frozen = player.pause();
    assert.equal(frozen, expected);
    now += 37; // paused wall time must not leak into the animation clock
    assert.equal(player.currentTicks, expected);
    player.resume();
    assert.equal(player.currentTicks, expected);
  }
  now += 500;
  expected += 500_000;
  assert.equal(player.currentTicks, expected);
  // Sampled values equal direct evaluation at the same logical time.
  assert.deepStrictEqual(player.sample(), player.sampleAt(expected));
});

test('reduced motion mode is deterministic and time-independent', () => {
  let now = 0;
  const player = new Player(makeAnimation(), () => now);
  player.reducedMotion = true;
  const samples = [0, 250, 999, 5000].map((ms) => {
    now = ms;
    return player.sample();
  });
  for (const s of samples) {
    assert.deepStrictEqual(s, { opacity: 1 });
  }
});

test('timeScale composes multiplicatively with playbackRate', () => {
  let now = 0;
  const player = new Player(makeAnimation(), () => now);
  player.setPlaybackRate(2);
  player.setTimeScale(1.5);
  assert.equal(player.effectiveRate, 3);
  now = 100;
  assert.equal(player.currentTicks, ticks(300));
  player.setPlaybackRate(-1);
  player.setTimeScale(2);
  // Re-anchored at 300ms; now runs backwards at 2x.
  now = 200;
  assert.equal(player.currentTicks, ticks(300) - ticks(200));
});

test('boundary events fire exactly once across drains', () => {
  let now = 0;
  const player = new Player(makeAnimation(), () => now);
  player.seek(0);
  now = 500;
  assert.deepStrictEqual(player.drainEvents().map((e) => e.name), ['half']);
  now = 1000;
  assert.deepStrictEqual(player.drainEvents().map((e) => e.name), ['done']);
  // Draining again at the same position re-fires nothing.
  assert.deepStrictEqual(player.drainEvents(), []);
  now = 1500;
  assert.deepStrictEqual(player.drainEvents(), []);
});

test('a single large drain equals many small drains', () => {
  let now = 0;
  const stepped = new Player(makeAnimation(), () => now);
  stepped.seek(0);
  const names: string[] = [];
  for (const t of [100, 500, 501, 999, 1000]) {
    now = t;
    names.push(...stepped.drainEvents().map((e) => e.name));
  }
  let now2 = 0;
  const jumped = new Player(makeAnimation(), () => now2);
  jumped.seek(0);
  now2 = 1000;
  assert.deepStrictEqual(names, jumped.drainEvents().map((e) => e.name));
});

test('seeking backwards delivers events in reverse crossing order', () => {
  let now = 0;
  const player = new Player(makeAnimation(), () => now);
  player.seek(ticks(1000));
  player.setPlaybackRate(-1);
  now = 1000; // local time travels 1000ms -> 0
  const events = player.drainEvents();
  assert.deepStrictEqual(events.map((e) => e.name), ['half', 'start']);
});
