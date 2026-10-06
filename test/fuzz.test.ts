import assert from 'node:assert/strict';
import test from 'node:test';
import {
  downsample,
  verifyEnvelope,
  verifyNoGapCrossing,
} from '../src/index.ts';
import type { DataPoint } from '../src/index.ts';

// Deterministic PRNG (LCG) so failures are reproducible.
function lcg(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 2 ** 32;
  };
}

test('randomized series: envelope, ordering and gap safety always hold', () => {
  for (let seed = 1; seed <= 50; seed++) {
    const rand = lcg(seed);
    const length = 1 + Math.floor(rand() * 200);
    const points: DataPoint[] = [];
    let time = 0;
    for (let i = 0; i < length; i++) {
      time += rand() < 0.2 ? 0 : 1 + Math.floor(rand() * 3); // duplicate times
      const value = rand() < 0.15 ? null : Math.floor(rand() * 2000) - 1000; // gaps + spikes
      points.push({ time, value });
    }
    const columnWidth = 1 + Math.floor(rand() * 10);
    const origin = Math.floor(rand() * 5) - 2;

    const sampled = downsample(points, { columnWidth, origin });
    assert.ok(
      sampled.every((p, i) => i === 0 ||
        sampled[i - 1].time < p.time ||
        (sampled[i - 1].time === p.time && sampled[i - 1].index < p.index)),
      `seed ${seed}: output not ordered by (time, index)`,
    );
    assert.deepEqual(
      verifyEnvelope(points, sampled, { columnWidth, origin }), [],
      `seed ${seed}: envelope violated`,
    );
    assert.deepEqual(
      verifyNoGapCrossing(points, sampled), [],
      `seed ${seed}: gap crossed`,
    );

    // Random viewport: column min/max must match the full-domain result.
    const maxTime = points[points.length - 1].time;
    const fromTime = Math.floor(rand() * (maxTime + 1));
    const toTime = fromTime + 1 + Math.floor(rand() * (maxTime - fromTime + 1));
    const view = downsample(points, {
      columnWidth, origin, fromTime, toTime, includeEdgeNeighbors: true,
    });
    const fullIdx = new Set(sampled.map((p) => p.index));
    for (const p of view) {
      if (p.roles.includes('edge-before') || p.roles.includes('edge-after')) continue;
      // first/last of the visible portion are viewport-specific extras;
      // column extremes must be identical to the full-domain selection.
      if (p.roles.includes('min') || p.roles.includes('max')) {
        assert.ok(fullIdx.has(p.index), `seed ${seed}: viewport extreme ${p.index} not in full result`);
      }
    }
    assert.deepEqual(
      verifyEnvelope(points, view, { columnWidth, origin, fromTime, toTime }), [],
      `seed ${seed}: viewport envelope violated`,
    );
    assert.deepEqual(
      verifyNoGapCrossing(points, view), [],
      `seed ${seed}: viewport gap crossed`,
    );
  }
});
