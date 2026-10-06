import assert from 'node:assert/strict';
import test from 'node:test';
import { DownsampleEngine, downsample, verifyEnvelope } from '../src/index.ts';
import type { DataPoint } from '../src/index.ts';

function series(values: Array<number | null>, startTime = 0): DataPoint[] {
  return values.map((value, i) => ({ time: startTime + i, value }));
}

test('zoom levels share segment summaries (coarse levels merge finer ones)', () => {
  const values = Array.from({ length: 64 }, (_, i) => (i * 37) % 11);
  const engine = new DownsampleEngine(series(values), { baseColumnWidth: 1, origin: 0 });

  engine.downsample(0);
  assert.deepEqual(engine.getCachedLevels(0, 64), [0]);
  engine.downsample(3); // must merge 0 -> 1 -> 2 -> 3 through the shared summary
  assert.deepEqual(engine.getCachedLevels(0, 64), [0, 1, 2, 3]);

  // Merged level-3 output equals a direct standalone computation at width 8.
  const viaEngine = engine.downsample(3);
  const direct = downsample(series(values), { columnWidth: 8, origin: 0 });
  assert.deepEqual(
    viaEngine.map((p) => [p.index, p.roles.slice().sort()]),
    direct.map((p) => [p.index, p.roles.slice().sort()]),
  );
  assert.deepEqual(
    verifyEnvelope(series(values), viaEngine, { columnWidth: 8, origin: 0 }),
    [],
  );
});

test('local edit updates column extremes and breakpoints, keeps untouched summaries', () => {
  // Two segments: indices 0..7 and 12..19, separated by missing values.
  const values: Array<number | null> = [
    1, 2, 3, 4, 5, 6, 7, 8,
    null, null, null, null,
    10, 11, 12, 13, 14, 15, 16, 17,
  ];
  const engine = new DownsampleEngine(series(values), { baseColumnWidth: 2, origin: 0 });
  engine.downsample(0);
  assert.deepEqual(engine.getCachedLevels(0, 8), [0]);
  assert.deepEqual(engine.getCachedLevels(12, 20), [0]);

  // Local edit inside segment 2 only: changes a column extreme.
  engine.updateValues(14, [999]);
  assert.equal(engine.version, 1);
  assert.deepEqual(engine.getCachedLevels(0, 8), [0], 'untouched segment keeps its summary');
  assert.deepEqual(engine.getCachedLevels(12, 20), [], 'edited segment summary invalidated');
  const after = engine.downsample(0);
  assert.ok(after.some((p) => p.index === 14 && p.value === 999 && p.roles.includes('max')));

  // Edit introducing a missing value moves breakpoints: segment 2 splits.
  engine.updateValues(16, [null]);
  const split = engine.downsample(0);
  const segOf = (index: number) => split.find((p) => p.index === index)?.segmentId;
  assert.notEqual(segOf(15), segOf(17), 'segments split at the new gap');
  assert.ok(split.some((p) => p.index === 15 && p.roles.includes('last')));
  assert.ok(split.some((p) => p.index === 17 && p.roles.includes('first')));
  assert.deepEqual(
    verifyEnvelope(series(values.map((v, i) => (i === 14 ? 999 : i === 16 ? null : v))),
      split, { columnWidth: 2, origin: 0 }),
    [],
  );
});

test('stale async zoom task never overwrites a newer data version', async () => {
  const values = Array.from({ length: 32 }, (_, i) => i % 5);
  const engine = new DownsampleEngine(series(values), { baseColumnWidth: 1, origin: 0 });

  // v0: schedule a zoom task, then edit before it completes.
  const staleTask = engine.scheduleDownsample(2);
  engine.updateValues(3, [500]); // v1: changes column extremes
  const freshTask = engine.scheduleDownsample(2);
  assert.equal(engine.pendingTasks, 2);

  // Complete out of order: the newer task finishes first, the stale one last.
  engine.flushPending('lifo');
  const [staleResult, freshResult] = await Promise.all([staleTask, freshTask]);

  assert.equal(staleResult.version, 0);
  assert.equal(staleResult.stale, true);
  assert.equal(freshResult.version, 1);
  assert.equal(freshResult.stale, false);

  // The applied result is the fresh one, computed from the edited data.
  const applied = engine.getLatestResult(2);
  assert.ok(applied);
  assert.deepEqual(applied, freshResult.result);
  assert.ok(applied.some((p) => p.value === 500), 'new extreme must be applied');
  assert.notDeepEqual(applied, staleResult.result);
});

test('async result computed before an edit is discarded when completed after it', async () => {
  const engine = new DownsampleEngine(series([1, 2, 3, 4]), { baseColumnWidth: 1, origin: 0 });
  const task = engine.scheduleDownsample(0);
  engine.updateValues(0, [42]);
  engine.flushPending('fifo');
  const result = await task;
  assert.equal(result.stale, true);
  assert.equal(engine.getLatestResult(0), undefined, 'stale result must not be applied');
});

test('viewport queries reuse shared summaries and match full-domain output', () => {
  const values = Array.from({ length: 128 }, (_, i) => Math.sin(i / 5) * 20 + (i % 3));
  const points = series(values);
  const engine = new DownsampleEngine(points, { baseColumnWidth: 1, origin: 0 });

  const full = engine.downsample(2);
  const view = engine.downsample(2, { fromTime: 30, toTime: 70 });
  assert.deepEqual(engine.getCachedLevels(0, 128), [0, 1, 2], 'one summary serves both queries');

  const fullIdx = new Set(full.map((p) => p.index));
  for (const p of view) {
    if (p.roles.includes('edge-before') || p.roles.includes('edge-after')) continue;
    // Column extremes must match the full-domain result; first/last of the
    // visible portion are viewport-specific extras.
    if (p.roles.includes('min') || p.roles.includes('max')) {
      assert.ok(fullIdx.has(p.index), `viewport extreme ${p.index} not in full result`);
    }
  }
  assert.ok(view.some((p) => p.roles.includes('edge-before')));
  assert.ok(view.some((p) => p.roles.includes('edge-after')));
  assert.deepEqual(
    verifyEnvelope(points, view, { columnWidth: 4, origin: 0, fromTime: 30, toTime: 70 }),
    [],
  );
});
