import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ticks } from '../src/time.ts';
import {
  activeDurationOf,
  directedTimeAt,
  normalizeTiming,
  phaseAt,
  reducedDirectedTime,
  TRACK_END_SENTINEL,
  TRACK_START_SENTINEL,
} from '../src/timing.ts';

test('negative delay starts the animation partway through', () => {
  const timing = normalizeTiming({ delay: ticks(-500), duration: ticks(1000), fill: 'both' });
  assert.equal(phaseAt(timing, 0), 'active');
  assert.equal(directedTimeAt(timing, 0), ticks(500));
  assert.equal(phaseAt(timing, ticks(-750)), 'before');
  assert.equal(directedTimeAt(timing, ticks(-750)), 0);
  assert.equal(directedTimeAt(timing, ticks(500)), ticks(1000));
});

test('zero duration is defined: start before, end at/after the delay', () => {
  const timing = normalizeTiming({ delay: ticks(100), duration: 0, fill: 'both' });
  assert.equal(phaseAt(timing, ticks(99)), 'before');
  assert.equal(phaseAt(timing, ticks(100)), 'after');
  assert.equal(directedTimeAt(timing, ticks(99)), TRACK_START_SENTINEL);
  assert.equal(directedTimeAt(timing, ticks(100)), TRACK_END_SENTINEL);

  const reversed = normalizeTiming({ delay: ticks(100), duration: 0, fill: 'both', direction: 'reverse' });
  assert.equal(directedTimeAt(reversed, ticks(99)), TRACK_END_SENTINEL);
  assert.equal(directedTimeAt(reversed, ticks(100)), TRACK_START_SENTINEL);
});

test('exact period boundary does not start an extra round', () => {
  const timing = normalizeTiming({
    duration: ticks(1000),
    iterations: 3,
    direction: 'alternate',
    fill: 'both',
  });
  assert.equal(activeDurationOf(timing), ticks(3000));
  // Exactly at the end: still iteration 2 (even -> normal), directed time = duration.
  assert.equal(directedTimeAt(timing, ticks(3000)), ticks(1000));
  // One tick earlier: iteration 2, near its end.
  assert.equal(directedTimeAt(timing, ticks(3000) - 1), ticks(1000) - 1);
  // Interior boundary: reversed iteration 1 starts at the track end, so the
  // curve stays continuous across the period boundary.
  assert.equal(directedTimeAt(timing, ticks(1000)), ticks(1000));
  const normal = normalizeTiming({ duration: ticks(1000), iterations: 3, fill: 'both' });
  assert.equal(directedTimeAt(normal, ticks(1000)), 0);
  // Mid reversed iteration: alternate iteration 1 plays backwards.
  assert.equal(directedTimeAt(timing, ticks(1500)), ticks(500));
});

test('reverse direction is exact time reversal', () => {
  const fwd = normalizeTiming({ duration: ticks(1000), iterations: 1, fill: 'both' });
  const rev = normalizeTiming({ duration: ticks(1000), iterations: 1, fill: 'both', direction: 'reverse' });
  for (let ms = 0; ms <= 1000; ms += 7) {
    const f = directedTimeAt(fwd, ticks(ms));
    const r = directedTimeAt(rev, ticks(ms));
    assert.equal(r, ticks(1000) - (f ?? 0));
  }
});

test('alternate-reverse reverses even iterations', () => {
  const timing = normalizeTiming({
    duration: ticks(1000),
    iterations: 2,
    direction: 'alternate-reverse',
    fill: 'both',
  });
  assert.equal(directedTimeAt(timing, ticks(250)), ticks(750));
  assert.equal(directedTimeAt(timing, ticks(1250)), ticks(250));
});

test('reduced-motion rest state is deterministic', () => {
  const normal = normalizeTiming({ duration: ticks(1000), iterations: 2, fill: 'both' });
  assert.equal(reducedDirectedTime(normal), ticks(1000));
  const reverse = normalizeTiming({ duration: ticks(1000), direction: 'reverse', fill: 'both' });
  assert.equal(reducedDirectedTime(reverse), 0);
  const infinite = normalizeTiming({ duration: ticks(800), iterations: Infinity, fill: 'both' });
  assert.equal(reducedDirectedTime(infinite), ticks(800));
});
