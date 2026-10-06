import assert from 'node:assert/strict';
import test from 'node:test';
import {
  downsample,
  splitIntoRuns,
  verifyEnvelope,
  verifyNoGapCrossing,
} from '../src/index.ts';
import type { DataPoint, SampledPoint } from '../src/index.ts';

function series(values: Array<number | null>, startTime = 0, step = 1): DataPoint[] {
  return values.map((value, i) => ({ time: startTime + i * step, value }));
}

function isSortedByTimeThenIndex(sampled: SampledPoint[]): boolean {
  for (let i = 1; i < sampled.length; i++) {
    const a = sampled[i - 1];
    const b = sampled[i];
    if (a.time > b.time || (a.time === b.time && a.index > b.index)) return false;
  }
  return true;
}

test('keeps per-column min/max plus segment first/last, ordered by time then index', () => {
  const points = series([3, 1, 4, 1, 5, 9, 2, 6]);
  const sampled = downsample(points, { columnWidth: 2, origin: 0 });

  assert.ok(isSortedByTimeThenIndex(sampled));
  assert.deepEqual(verifyEnvelope(points, sampled, { columnWidth: 2, origin: 0 }), []);
  assert.deepEqual(verifyNoGapCrossing(points, sampled), []);

  const rolesOf = (index: number) => sampled.find((p) => p.index === index)?.roles ?? [];
  assert.ok(rolesOf(0).includes('first'));
  assert.ok(rolesOf(7).includes('last'));
  // Column 2 (times 4..5) has min 5 at index 4 and max 9 at index 5.
  assert.ok(rolesOf(4).includes('min'));
  assert.ok(rolesOf(5).includes('max'));
});

test('preserves spikes inside a column (envelope check)', () => {
  const values = Array<number | null>(40).fill(0);
  values[17] = 100;  // spike up
  values[23] = -50;  // spike down
  const points = series(values);
  const sampled = downsample(points, { columnWidth: 4, origin: 0 });

  assert.deepEqual(verifyEnvelope(points, sampled, { columnWidth: 4, origin: 0 }), []);
  const byIndex = new Map(sampled.map((p) => [p.index, p]));
  assert.equal(byIndex.get(17)?.value, 100);
  assert.ok(byIndex.get(17)?.roles.includes('max'));
  assert.equal(byIndex.get(23)?.value, -50);
  assert.ok(byIndex.get(23)?.roles.includes('min'));
});

test('duplicate timestamps keep original index order and full envelope', () => {
  const points: DataPoint[] = [
    { time: 5, value: 2 },
    { time: 5, value: 9 },
    { time: 5, value: 4 },
    { time: 6, value: 1 },
  ];
  const sampled = downsample(points, { columnWidth: 1, origin: 0 });

  assert.ok(isSortedByTimeThenIndex(sampled));
  const atTime5 = sampled.filter((p) => p.time === 5).map((p) => p.index);
  assert.deepEqual(atTime5, [...atTime5].sort((a, b) => a - b));
  assert.deepEqual(verifyEnvelope(points, sampled, { columnWidth: 1, origin: 0 }), []);
  // Min 2 (index 0) and max 9 (index 1) of the duplicated-timestamp column survive.
  assert.ok(sampled.some((p) => p.index === 0 && p.roles.includes('min')));
  assert.ok(sampled.some((p) => p.index === 1 && p.roles.includes('max')));
});

test('single-point segments survive and act as their own first/last/min/max', () => {
  const points = series([null, 7, null, null, 3, 4, null]);
  const sampled = downsample(points, { columnWidth: 1, origin: 0 });

  const single = sampled.find((p) => p.index === 1);
  assert.ok(single);
  for (const role of ['first', 'last', 'min', 'max'] as const) {
    assert.ok(single.roles.includes(role), `missing role ${role}`);
  }
  assert.deepEqual(verifyEnvelope(points, sampled, { columnWidth: 1, origin: 0 }), []);
  assert.deepEqual(verifyNoGapCrossing(points, sampled), []);
});

test('missing-value gaps split runs so lines never cross them', () => {
  const points = series([1, 2, null, 4, 5, null, null, 8]);
  const sampled = downsample(points, { columnWidth: 1, origin: 0 });

  assert.deepEqual(verifyNoGapCrossing(points, sampled), []);
  const runs = splitIntoRuns(sampled);
  assert.equal(runs.length, 3); // one run per segment
  for (const run of runs) {
    const segmentIds = new Set(run.map((p) => p.segmentId));
    assert.equal(segmentIds.size, 1);
    for (const p of run) assert.ok(p.value !== null && !Number.isNaN(p.value));
  }
  // Segment ids change exactly at run boundaries; no run mixes segments.
  assert.deepEqual(runs.map((r) => r[0].segmentId), [0, 1, 2]);
});

test('every sampled point carries original-index-range evidence', () => {
  const points = series([3, 1, 4, 1, 5, 9, 2, 6]);
  const sampled = downsample(points, { columnWidth: 2, origin: 0 });

  for (const p of sampled) {
    assert.ok(p.evidence.fromIndex <= p.index && p.index <= p.evidence.toIndex);
    // A min/max point stands for its whole column bucket (2 points here).
    if (p.roles.includes('min') || p.roles.includes('max')) {
      assert.ok(p.evidence.toIndex > p.evidence.fromIndex || p.roles.includes('first') && p.roles.includes('last'));
    }
  }
  // Index 4 is min of column 2 (indices 4..5): evidence must cover both.
  const minOfColumn2 = sampled.find((p) => p.index === 4)!;
  assert.deepEqual(minOfColumn2.evidence, { fromIndex: 4, toIndex: 5 });
});

test('viewport query keeps edge neighbors and matches full downsampling', () => {
  const values = Array.from({ length: 100 }, (_, i) => Math.sin(i / 3) * 10 + (i % 7));
  const points = series(values);
  const grid = { columnWidth: 2, origin: 0 };

  const full = downsample(points, grid);
  const view = downsample(points, {
    ...grid, fromTime: 20, toTime: 40, includeEdgeNeighbors: true,
  });

  // Column extremes of the viewport match the full-domain result exactly.
  // (first/last of the visible portion are legitimate viewport-only extras.)
  const fullByIndex = new Map(full.map((p) => [p.index, p]));
  const interior = view.filter(
    (p) => !p.roles.includes('edge-before') && !p.roles.includes('edge-after'),
  );
  assert.ok(interior.length > 0);
  const extremesOf = (sampled: SampledPoint[]) => {
    const map = new Map<string, { min?: number; max?: number }>();
    for (const p of sampled) {
      const entry = map.get(`${p.segmentId}:${p.column}`) ?? {};
      if (p.roles.includes('min')) entry.min = p.index;
      if (p.roles.includes('max')) entry.max = p.index;
      map.set(`${p.segmentId}:${p.column}`, entry);
    }
    return map;
  };
  const fullExtremes = extremesOf(full);
  for (const [key, entry] of extremesOf(interior)) {
    const ref = fullExtremes.get(key);
    assert.ok(ref, `column ${key} missing from full downsampling`);
    assert.deepEqual(entry, ref, `column ${key} extremes differ from full result`);
  }

  // Edge neighbors: adjacent in-segment points just outside the viewport.
  const edgeBefore = view.filter((p) => p.roles.includes('edge-before'));
  const edgeAfter = view.filter((p) => p.roles.includes('edge-after'));
  assert.equal(edgeBefore.length, 1);
  assert.equal(edgeAfter.length, 1);
  const firstVisible = Math.min(...interior.map((p) => p.index));
  const lastVisible = Math.max(...interior.map((p) => p.index));
  assert.equal(edgeBefore[0].index, firstVisible - 1);
  assert.equal(edgeAfter[0].index, lastVisible + 1);
  assert.equal(edgeBefore[0].segmentId, interior[0].segmentId); // same segment, no gap

  // The envelope of the viewport (visible columns) is still exact.
  assert.deepEqual(
    verifyEnvelope(points, view, { ...grid, fromTime: 20, toTime: 40 }),
    [],
  );
});

test('viewport at segment boundary adds no edge neighbor across a gap', () => {
  const points = series([1, 2, 3, null, 5, 6, 7]);
  const view = downsample(points, {
    columnWidth: 1, origin: 0, fromTime: 4, toTime: 7, includeEdgeNeighbors: true,
  });
  // Segment 1 starts exactly at the viewport edge: no in-segment point before it.
  assert.equal(view.filter((p) => p.roles.includes('edge-before')).length, 0);
  assert.deepEqual(verifyNoGapCrossing(points, view), []);
});
