import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { metricsV1 } from '../fixtures/metrics.ts';
import { linkSentence } from '../fixtures/trees.ts';

test('link crossing the ellipsis splits into visible fragments of one original range', () => {
  const engine = new TruncationEngine(linkSentence, metricsV1, { width: 90, maxLines: 1, mode: 'middle' });
  const r = engine.lastLayout;
  assert.equal(r.truncated, true);
  assert.equal(r.linkFragments.length, 1);
  const frag = r.linkFragments[0];
  assert.equal(frag.linkId, 'L1');
  assert.equal(frag.href, 'https://example.com/docs');
  // the ellipsis cuts the link text: two visible pieces, one link
  assert.equal(frag.ranges.length, 2);
  const linkStart = 'Open '.length;
  const linkEnd = linkStart + 'the complete reference documentation'.length;
  assert.equal(frag.ranges[0].start, linkStart);
  assert.equal(frag.ranges[1].end, linkEnd);
  assert.ok(frag.ranges[0].end < frag.ranges[1].start, 'elided gap inside the link');
  // both pieces still point at the same original link range
  for (const range of frag.ranges) {
    assert.ok(range.start >= linkStart && range.end <= linkEnd);
  }
  // every rect belongs to the single link fragment
  assert.ok(frag.rects.length >= 1);
});

test('untruncated link yields one continuous range', () => {
  const engine = new TruncationEngine(linkSentence, metricsV1, { width: 400, maxLines: 1, mode: 'middle' });
  const frag = engine.lastLayout.linkFragments[0];
  assert.equal(frag.ranges.length, 1);
  assert.equal(frag.ranges[0].start, 'Open '.length);
  assert.equal(frag.ranges[0].end, 'Open '.length + 'the complete reference documentation'.length);
});
