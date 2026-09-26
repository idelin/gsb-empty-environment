// 运行样例:node samples/demo.ts
import {
  TruncationEngine, caretFromPoint, selectionRects, copyText, linkFragments,
} from '../src/index.ts';
import {
  metricsV1, metricsV2, metricsWideEllipsis,
  longText, mixedBidi, linked, withEntity, emojiText, conflictText,
} from '../fixtures/fixtures.ts';
import type { LayoutResult } from '../src/index.ts';

function show(title: string, r: LayoutResult) {
  console.log(`\n== ${title} (metrics v${r.metricsVersion}, gen ${r.generation}) ==`);
  for (const line of r.lines) {
    const text = line.items.map((i) => (i.kind === 'atom' ? i.atom.text : '…')).join('');
    console.log(`  |${text}|  (宽 ${line.width})`);
  }
  if (r.truncated) console.log('  [已截断]');
  for (const c of r.conflicts) {
    console.log(`  [冲突] ${c.type}: 需要 ${c.requiredWidth},可用 ${c.availableWidth},` +
      ` 片段 ${c.fragments.map((f) => `${f.id}(${f.requiredWidth})`).join(', ')}`);
  }
  for (const d of r.degradations) {
    console.log(`  [退化] ${d.type}: 省略号宽 ${d.ellipsisWidth} > 行宽 ${d.lineWidth}`);
  }
}

// 1. 多行 + 中间省略 + 关键片段保留
const eng = new TruncationEngine(metricsV1);
show('多行中间省略,关键片段 lazy 保留', eng.layout(longText, {
  lineWidth: 20, maxLines: 2, lineHeight: 10, mode: 'middle',
  keyFragments: [{ id: 'lazy', start: 35, end: 39 }],
}));

// 2. 混合方向片段
show('混合方向:RTL run 视觉反转', eng.layout(mixedBidi, {
  lineWidth: 40, maxLines: 3, lineHeight: 10,
}));

// 3. 链接跨省略区 + 视觉位置映射 + 复制选区
const r3 = eng.layout(linked, {
  lineWidth: 24, maxLines: 1, lineHeight: 10, mode: 'middle',
  keyFragments: [{ id: 'tail', start: 30, end: 37 }],
});
show('链接跨省略区被分成可见片段', r3);
console.log('  链接片段:', JSON.stringify(linkFragments(r3, 'L1')
  .map((f) => ({ 可见: [f.start, f.end], 原范围: [f.linkRange.start, f.linkRange.end] }))));
const atoms = eng.current!.atoms;
console.log('  命中测试 (x=3,y=0) -> 原文偏移', caretFromPoint(r3, atoms, 3, 0, 10));
const sel = selectionRects(r3, 12, 8); // 反向选择
console.log(`  反向选择 anchor=${sel.anchor} focus=${sel.focus} reversed=${sel.reversed}` +
  ` 复制=${JSON.stringify(copyText(eng.current!.accessibleName, sel.anchor, sel.focus))}`);
console.log('  完整可访问名称:', JSON.stringify(eng.current!.accessibleName));

// 4. 不可拆实体 + emoji 组合簇
show('实体 @channel 不可拆', eng.layout(withEntity, {
  lineWidth: 12, maxLines: 5, lineHeight: 10, mode: 'end',
}));
show('emoji 组合簇不切开', eng.layout(emojiText, {
  lineWidth: 5, maxLines: 4, lineHeight: 10, mode: 'end',
}));

// 5. 字体度量晚到:增量重排,旧测量不覆盖新布局
const eng2 = new TruncationEngine(metricsV1);
const before = eng2.layout(longText, { lineWidth: 40, maxLines: 4, lineHeight: 10, mode: 'end' });
show('度量 v1(估算)', before);
const late = eng2.updateMetrics(metricsV2);
console.log(`\n晚到度量 v2 接受=${late.accepted},增量重排:复用 ${eng2.stats.reused} 项,新算 ${eng2.stats.computed} 项`);
show('度量 v2(真实字体,更宽)', late.result!);
const stale = eng2.updateMetrics(metricsV1);
console.log(`旧度量 v1 再投递:接受=${stale.accepted}(被拒绝,布局停留 gen ${eng2.current!.result.generation})`);

// 6. 显式冲突与退化
show('关键片段互相超容量', eng.layout(conflictText, {
  lineWidth: 20, maxLines: 1, lineHeight: 10, mode: 'middle',
  keyFragments: [
    { id: 'head', start: 0, end: 10 },
    { id: 'tail', start: 44, end: 54 },
  ],
}));
const eng3 = new TruncationEngine(metricsWideEllipsis);
show('省略号本身超宽', eng3.layout(longText, {
  lineWidth: 20, maxLines: 1, lineHeight: 10, mode: 'end',
}));
