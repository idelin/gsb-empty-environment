// Runnable showcase: node examples/demo.ts
import { TruncationEngine } from '../src/engine.ts';
import { accessibleName, hitTest, selectRange } from '../src/mapping.ts';
import type { LayoutResult } from '../src/types.ts';
import { metricsV1, metricsV2, metricsV3, metricsWideEllipsis } from '../fixtures/metrics.ts';
import {
  combiningMark, conflictingKeys, englishParagraph, entitySentence,
  fileName, hebrewMixed, keyLog, linkSentence, mixedWithEmoji,
} from '../fixtures/trees.ts';

function show(title: string, r: LayoutResult): void {
  console.log(`\n=== ${title} ===`);
  console.log(`paraDir=${r.paraDir} truncated=${r.truncated} metricVersion=${r.metricVersion}`);
  for (const row of r.lines) {
    const text = row.map((it) => it.text).join('');
    const width = row.reduce((sum, it) => sum + it.width, 0);
    console.log(`  |${text}| (${width}px)`);
  }
  if (r.degraded) console.log(`  DEGRADED: ${r.degraded.message}`);
  for (const c of r.conflicts) console.log(`  CONFLICT: ${c.message}`);
  for (const f of r.linkFragments) {
    console.log(`  link ${f.linkId} -> ${f.href} ranges=${JSON.stringify(f.ranges)}`);
  }
}

// 1. multi-line clamp
show('multi-line clamp (end)', new TruncationEngine(englishParagraph, metricsV1, { width: 120, maxLines: 2, mode: 'end' }).lastLayout);

// 2. middle ellipsis
show('middle ellipsis (file name)', new TruncationEngine(fileName, metricsV1, { width: 60, maxLines: 1, mode: 'middle' }).lastLayout);

// 3. key-fragment retention
show('key retention (log line)', new TruncationEngine(keyLog, metricsV1, { width: 100, maxLines: 1, mode: 'key' }).lastLayout);

// 4. conflicting required fragments
show('required fragments in conflict', new TruncationEngine(conflictingKeys, metricsV1, { width: 100, maxLines: 1, mode: 'key' }).lastLayout);

// 5. emoji clusters + unbreakable entity
show('emoji ZWJ cluster', new TruncationEngine(mixedWithEmoji, metricsV1, { width: 40, maxLines: 1, mode: 'end' }).lastLayout);
show('combining mark', new TruncationEngine(combiningMark, metricsV1, { width: 34, maxLines: 1, mode: 'end' }).lastLayout);
show('unbreakable entity', new TruncationEngine(entitySentence, metricsV1, { width: 50, maxLines: 4, mode: 'end' }).lastLayout);

// 6. link split across the ellipsis
const linkLayout = new TruncationEngine(linkSentence, metricsV1, { width: 90, maxLines: 1, mode: 'middle' }).lastLayout;
show('link crossing ellipsis', linkLayout);
console.log(`  accessibleName: ${JSON.stringify(accessibleName(linkLayout))}`);
const copy = selectRange(linkLayout, 0, linkLayout.source.length);
console.log(`  copy full selection: ${JSON.stringify(copy.copiedText)} (visible ranges: ${JSON.stringify(copy.ranges)})`);

// 7. bidi + selection direction
const bidi = new TruncationEngine(hebrewMixed, metricsV1, { width: 300, maxLines: 1, mode: 'end' }).lastLayout;
show('mixed-direction runs', bidi);
const fwd = selectRange(bidi, 4, 7);
const bwd = selectRange(bidi, 7, 4);
console.log(`  select(4,7) backward=${fwd.backward} copied=${JSON.stringify(fwd.copiedText)} rects=${JSON.stringify(fwd.rects)}`);
console.log(`  select(7,4) backward=${bwd.backward} copied=${JSON.stringify(bwd.copiedText)} rects=${JSON.stringify(bwd.rects)}`);
console.log(`  hitTest(line0, x=1) -> source offset ${hitTest(bidi, 0, 1)}`);

// 8. ellipsis wider than the line
show('ellipsis too wide', new TruncationEngine(englishParagraph, metricsWideEllipsis, { width: 40, maxLines: 2, mode: 'end' }).lastLayout);

// 9. late font metrics: incremental re-layout with stale protection
console.log('\n=== incremental re-layout on late metrics ===');
const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 120, maxLines: 2, mode: 'end' });
console.log(`v1 line count: ${engine.lastLayout.lines.length}`);
engine.applyMetrics(metricsV2);
console.log(`v2 applied, line count: ${engine.lastLayout.lines.length}, metricVersion=${engine.lastLayout.metricVersion}`);
const stale = engine.applyMetrics(metricsV1);
console.log(`re-applying v1: applied=${stale.applied} reason=${stale.reason} metricVersion=${engine.lastLayout.metricVersion}`);
const slow = Promise.resolve(metricsV2);
const fast = Promise.resolve(metricsV3);
const d1 = engine.deliverMetrics(slow.then((t) => new Promise<typeof t>((res) => setTimeout(() => res(t), 20))));
const d2 = engine.deliverMetrics(fast);
await d2;
console.log(`fast v3 delivered: metricVersion=${engine.lastLayout.metricVersion}`);
const d1Result = await d1;
console.log(`slow v2 delivered late: applied=${d1Result.applied} reason=${d1Result.reason} metricVersion=${engine.lastLayout.metricVersion}`);

// 10. continuous width sweep
console.log('\n=== width sweep (metricVersion stays 1) ===');
const sweep = new TruncationEngine(fileName, metricsV1, { width: 120, maxLines: 1, mode: 'middle' });
for (const w of [120, 90, 60, 45, 30]) {
  const r = sweep.setWidth(w);
  console.log(`  w=${w}: |${r.lines[0].map((it) => it.text).join('')}|`);
}
