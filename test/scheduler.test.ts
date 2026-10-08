import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TimeSeries } from '../src/series.ts';
import { ZoomScheduler } from '../src/scheduler.ts';
import { queryViewport } from '../src/viewport.ts';

function manualScheduler(series: TimeSeries) {
  const queue: Array<() => void> = [];
  const scheduler = new ZoomScheduler(series, (task) => queue.push(task));
  const flush = (): void => {
    while (queue.length) queue.shift()!();
  };
  return { scheduler, flush, queue };
}

const vp = { t0: 0, t1: 9, width: 4 };

test('a stale async task never overwrites a newer data version', async () => {
  const s = new TimeSeries([0, 1, 2, 3, 4, 5, 6, 7, 8, 9], [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
  const { scheduler, flush, queue } = manualScheduler(s);
  const p1 = scheduler.request(vp); // captures version 0
  s.setValue(5, 500); // version 1 while the task is in flight
  flush();
  const r1 = await p1;
  assert.equal(r1.status, 'stale');
  assert.equal(r1.version, 0);
  assert.equal(scheduler.cached(vp), undefined, 'stale result must not be cached');

  const p2 = scheduler.request(vp); // captures version 1
  flush();
  const r2 = await p2;
  assert.equal(r2.status, 'fresh');
  assert.equal(r2.version, 1);
  assert.deepEqual(scheduler.cached(vp), r2.points);
  assert.ok(r2.points.some((p) => p.value === 500), 'new data visible after re-request');
});

test('cache hits serve the current version without rescheduling work', async () => {
  const s = new TimeSeries([0, 1, 2, 3], [1, 2, 3, 4]);
  const { scheduler, flush, queue } = manualScheduler(s);
  const p1 = scheduler.request(vp);
  flush();
  await p1;
  const again = await scheduler.request(vp);
  assert.equal(again.status, 'fresh');
  assert.equal(queue.length, 0, 'no new task scheduled for a cache hit');
  s.setValue(0, -1); // bump version: cache entry becomes invalid
  assert.equal(scheduler.cached(vp), undefined);
  const p2 = scheduler.request(vp);
  assert.equal(queue.length, 1, 'mutation invalidates the cache');
  flush();
  const r2 = await p2;
  assert.equal(r2.status, 'fresh');
  assert.ok(r2.points.some((p) => p.value === -1));
});

test('concurrent requests for the same viewport share one computation', async () => {
  const s = new TimeSeries([0, 1, 2, 3], [1, 2, 3, 4]);
  const { scheduler, flush, queue } = manualScheduler(s);
  const a = scheduler.request(vp);
  const b = scheduler.request(vp);
  assert.equal(queue.length, 1);
  flush();
  const [ra, rb] = await Promise.all([a, b]);
  assert.equal(ra.status, 'fresh');
  assert.equal(rb.status, 'fresh');
  assert.deepEqual(ra.points, rb.points);
});

test('different zoom levels share the series and cache independently', async () => {
  const s = new TimeSeries(
    Array.from({ length: 1000 }, (_, i) => i),
    Array.from({ length: 1000 }, (_, i) => Math.sin(i / 50) * 100),
  );
  const { scheduler, flush } = manualScheduler(s);
  const wide = scheduler.request({ t0: 0, t1: 999, width: 100 });
  const zoomed = scheduler.request({ t0: 400, t1: 600, width: 50 });
  flush();
  const [rw, rz] = await Promise.all([wide, zoomed]);
  assert.equal(rw.status, 'fresh');
  assert.equal(rz.status, 'fresh');
  assert.deepEqual(rw.points, queryViewport(s, 0, 999, 100));
  assert.deepEqual(rz.points, queryViewport(s, 400, 600, 50));
});

test('prune drops entries computed from older versions', async () => {
  const s = new TimeSeries([0, 1, 2, 3], [1, 2, 3, 4]);
  const { scheduler, flush } = manualScheduler(s);
  const p = scheduler.request(vp);
  flush();
  await p;
  assert.ok(scheduler.cached(vp));
  s.setValue(1, 20);
  scheduler.prune();
  const p2 = scheduler.request(vp);
  flush();
  const r2 = await p2;
  assert.equal(r2.version, s.version);
});
