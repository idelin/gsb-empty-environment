import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { splitClusters } from '../src/cluster.ts';
import { metricsV1 } from '../fixtures/metrics.ts';
import { combiningMark, mixedWithEmoji } from '../fixtures/trees.ts';
import { visibleClusters } from './helpers.ts';

const FAMILY = '👨‍👩‍👧‍👦';
const E_ACUTE = 'é';   // e + combining acute, one grapheme cluster
const ACUTE = '́';       // combining acute alone
const ZWJ = '‍';

test('ZWJ emoji sequence is a single cluster', () => {
  const clusters = splitClusters(FAMILY);
  assert.equal(clusters.length, 1);
  assert.equal(clusters[0].text, FAMILY);
});

test('emoji cluster is kept whole when it fits', () => {
  const engine = new TruncationEngine(mixedWithEmoji, metricsV1, { width: 60, maxLines: 1, mode: 'end' });
  const kept = visibleClusters(engine.lastLayout).map((c) => c.text);
  assert.ok(kept.includes(FAMILY));
});

test('emoji cluster is dropped whole, never split, when it does not fit', () => {
  const engine = new TruncationEngine(mixedWithEmoji, metricsV1, { width: 40, maxLines: 1, mode: 'end' });
  const kept = visibleClusters(engine.lastLayout).map((c) => c.text);
  assert.ok(!kept.includes(FAMILY));
  for (const piece of ['👨', '👩', '👧', '👦', ZWJ]) {
    assert.ok(!kept.includes(piece), `no partial emoji fragment ${JSON.stringify(piece)}`);
  }
});

test('combining-mark sequence is never split', () => {
  const wide = new TruncationEngine(combiningMark, metricsV1, { width: 34, maxLines: 1, mode: 'end' });
  const keptWide = visibleClusters(wide.lastLayout).map((c) => c.text);
  assert.ok(keptWide.includes(E_ACUTE), 'e + combining acute kept as one cluster');
  assert.ok(!keptWide.includes('e'));

  const narrow = new TruncationEngine(combiningMark, metricsV1, { width: 30, maxLines: 1, mode: 'end' });
  const keptNarrow = visibleClusters(narrow.lastLayout).map((c) => c.text);
  assert.ok(!keptNarrow.includes(E_ACUTE));
  assert.ok(!keptNarrow.includes('e'));
  assert.ok(!keptNarrow.includes(ACUTE), 'no orphan combining mark');
});
