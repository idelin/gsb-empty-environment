import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { metricsV1, metricsV2, metricsV3 } from '../fixtures/metrics.ts';
import { englishParagraph } from '../fixtures/trees.ts';
import { allText, lineWidth } from './helpers.ts';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => { resolve = res; });
  return { promise, resolve };
}

test('late-arriving metrics trigger an incremental re-layout', () => {
  const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 120, maxLines: 2, mode: 'end' });
  const before = allText(engine.lastLayout);
  assert.equal(engine.lastLayout.metricVersion, 1);

  const result = engine.applyMetrics(metricsV2);
  assert.equal(result.applied, true);
  assert.equal(engine.lastLayout.metricVersion, 2);
  assert.notEqual(allText(engine.lastLayout), before, 'layout recomputed with the new font');
});

test('stale metric versions never overwrite a newer layout', () => {
  const engine = new TruncationEngine(englishParagraph, metricsV2, { width: 120, maxLines: 2, mode: 'end' });
  const before = engine.lastLayout;
  const result = engine.applyMetrics(metricsV1); // older version
  assert.equal(result.applied, false);
  assert.equal(result.reason, 'stale-version');
  assert.equal(engine.lastLayout, before, 'layout object untouched');
  assert.equal(engine.lastLayout.metricVersion, 2);
});

test('out-of-order async metric deliveries cannot clobber newer layout', async () => {
  const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 120, maxLines: 2, mode: 'end' });
  const slow = deferred<typeof metricsV2>();
  const fast = deferred<typeof metricsV3>();

  const slowDelivery = engine.deliverMetrics(slow.promise); // issued first
  const fastDelivery = engine.deliverMetrics(fast.promise); // issued second

  fast.resolve(metricsV3); // newer request completes first
  const fastResult = await fastDelivery;
  assert.equal(fastResult.applied, true);
  assert.equal(engine.lastLayout.metricVersion, 3);

  slow.resolve(metricsV2); // stale request completes late
  const slowResult = await slowDelivery;
  assert.equal(slowResult.applied, false);
  assert.equal(slowResult.reason, 'superseded-request');
  assert.equal(engine.lastLayout.metricVersion, 3, 'old measurement did not overwrite');
});

test('duplicate async delivery of the same font is applied once', async () => {
  const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 120, maxLines: 2, mode: 'end' });
  const first = deferred<typeof metricsV2>();
  const second = deferred<typeof metricsV2>();
  const d1 = engine.deliverMetrics(first.promise);
  const d2 = engine.deliverMetrics(second.promise);
  second.resolve(metricsV2);
  first.resolve(metricsV2);
  assert.equal((await d2).applied, true);
  assert.equal((await d1).applied, false);
  assert.equal(engine.lastLayout.metricVersion, 2);
});

test('continuous width changes re-layout every time with consistent invariants', () => {
  const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 200, maxLines: 2, mode: 'end' });
  const seen = new Set<string>();
  for (let w = 200; w >= 40; w -= 7) {
    const r = engine.setWidth(w);
    assert.equal(r.metricVersion, 1, 'metrics unchanged by width sweep');
    assert.ok(r.lines.length <= 2, `lines fit at width ${w}`);
    assert.ok(r.lines.length >= 1);
    for (let i = 0; i < r.lines.length; i++) {
      assert.ok(lineWidth(r, i) <= w, `line ${i} within ${w}px`);
    }
    if (r.truncated) assert.ok(r.ellipsis, 'truncated output always has its ellipsis');
    seen.add(allText(r));
  }
  assert.ok(seen.size > 5, 'layout actually responds to width changes');
});
