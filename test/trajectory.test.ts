import { test } from 'node:test';
import assert from 'node:assert/strict';
import { KeyframeAnimation } from '../src/animation.ts';
import { Clock } from '../src/clock.ts';
import { Player } from '../src/player.ts';
import { Timeline } from '../src/timeline.ts';

// Deterministic seeded RNG (LCG) so the trajectory is reproducible.
function lcg(seed: number) {
  let state = seed;
  return () => {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    return state / 0x7fffffff;
  };
}

function buildRoot() {
  const animA = new KeyframeAnimation({
    keyframes: [
      { offset: 0, easing: 'ease-in', props: { x: 0 } },
      { offset: 1, props: { x: 100 } },
    ],
    duration: 2,
    iterations: 2,
    direction: 'alternate',
    keyframeEvents: true,
  });
  const animB = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { y: 0 } },
      { offset: 0.5, easing: 'ease-out', props: { y: 40 } },
      { offset: 1, props: { y: 10 } },
    ],
    duration: 1.5,
    keyframeEvents: true,
  });
  const inner = new Timeline().add(animB, 0.25, -1.5);
  return new Timeline().add(animA, 0, 1).add(inner, 1, 1).addPause(2, 0.75).addMarker(3, 'mark');
}

test('auto-generated trajectory: sequential, random-access and direct evaluation agree', () => {
  const root = buildRoot();
  const rand = lcg(42);
  const times = Array.from({ length: 200 }, () => rand() * 6).sort((a, b) => a - b);

  // 1. Sequential advancement.
  const sequential = new Player(root, new Clock(() => 0));
  const sequentialValues = times.map((t) => {
    sequential.advanceTo(t);
    return sequential.value();
  });

  // 2. Random access: same times in shuffled order on a fresh player.
  const shuffledRand = lcg(7);
  const shuffled = [...times].sort(() => shuffledRand() - 0.5);
  const randomAccess = new Player(buildRoot(), new Clock(() => 0));
  const randomValues = new Map<number, Record<string, number>>();
  for (const t of shuffled) {
    randomAccess.seek(t);
    randomValues.set(t, randomAccess.value());
  }

  // 3. Direct evaluation.
  times.forEach((t, i) => {
    assert.deepEqual(sequentialValues[i], randomValues.get(t));
    assert.deepEqual(sequentialValues[i], root.valueAt(t));
  });
});

test('event stream is identical for stepped advancement and a single seek', () => {
  const rand = lcg(1234);
  const steps = [0];
  let acc = 0;
  while (acc < 6) {
    acc += rand() * 0.4;
    steps.push(Math.min(acc, 6));
  }
  const stepped = new Player(buildRoot(), new Clock(() => 0));
  for (const t of steps) stepped.advanceTo(t);

  const jumped = new Player(buildRoot(), new Clock(() => 0));
  jumped.seek(6);

  const summarize = (events: { name: string; time: number; direction: number }[]) =>
    events.map((e) => `${e.name}@${e.time}(${e.direction})`);
  assert.deepEqual(summarize(stepped.events), summarize(jumped.events));
  assert.ok(stepped.events.length > 0);
});
