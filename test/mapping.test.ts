import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine, caretFromPoint, selectionRects, copyText, linkFragments } from '../src/index.ts';
import { metricsV1, longText, mixedBidi, linked } from '../fixtures/fixtures.ts';

test('视觉位置映射回原文:每个可见原子中心命中其原文区间', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(longText, { lineWidth: 20, maxLines: 3, lineHeight: 10, mode: 'end' });
  const atoms = eng.current!.atoms;
  for (const [li, line] of r.lines.entries()) {
    for (const item of line.items) {
      const offset = caretFromPoint(r, atoms, item.x + item.width / 2, li * 10, 10);
      if (item.kind === 'atom') {
        assert.ok(
          offset === item.atom.start || offset === item.atom.end,
          `atom [${item.atom.start},${item.atom.end}) hit -> ${offset}`,
        );
      } else {
        const lo = atoms[item.covers.atomStart].start;
        const hi = atoms[item.covers.atomEnd - 1].end;
        assert.ok(offset === lo || offset === hi);
      }
    }
  }
});

test('复制选区得到逻辑序原文;反向选择方向保留', () => {
  const eng = new TruncationEngine(metricsV1);
  eng.layout(linked, { lineWidth: 60, maxLines: 3, lineHeight: 1 });
  const name = eng.current!.accessibleName;
  assert.equal(copyText(name, 7, 13), 'click ');
  // 反向选择:anchor > focus,复制结果一致,方向标记保留
  const sel = selectionRects(eng.current!.result, 13, 7);
  assert.equal(sel.reversed, true);
  assert.equal(copyText(name, sel.anchor, sel.focus), 'click ');
  assert.ok(sel.rects.length > 0);
});

test('RTL 片段:左边缘命中逻辑 end,选择方向保留', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(mixedBidi, { lineWidth: 40, maxLines: 3, lineHeight: 10 });
  const atoms = eng.current!.atoms;
  const rtlItem = r.lines[0].items.find((i) => i.kind === 'atom' && i.atom.dir === 'rtl')!;
  assert.ok(rtlItem.kind === 'atom');
  if (rtlItem.kind !== 'atom') return;
  // 视觉最左的 RTL 原子是逻辑最后的 ם
  const leftmost = r.lines[0].items
    .filter((i) => i.kind === 'atom' && i.atom.dir === 'rtl')
    .sort((a, b) => a.x - b.x)[0];
  assert.ok(leftmost.kind === 'atom' && leftmost.atom.text === 'ם');
  const offLeft = caretFromPoint(r, atoms, rtlItem.x + 0.1, 0, 10);
  assert.equal(offLeft, rtlItem.atom.end);
  // 反向选择跨越 RTL 片段,复制仍是逻辑序
  const name = eng.current!.accessibleName;
  const sel = selectionRects(r, 9, 6);
  assert.equal(sel.reversed, true);
  assert.equal(copyText(name, sel.anchor, sel.focus), name.slice(6, 9));
});

test('链接跨省略区:分成可见片段但指向同一原范围', () => {
  const eng = new TruncationEngine(metricsV1);
  // 关键片段落在链接尾部,迫使链接中段被省略
  const r = eng.layout(linked, {
    lineWidth: 24, maxLines: 1, lineHeight: 1, mode: 'middle',
    keyFragments: [{ id: 'tail', start: 30, end: 37 }],
  });
  assert.equal(r.conflicts.length, 0);
  const frags = linkFragments(r, 'L1');
  assert.ok(frags.length >= 2, `expected split link, got ${frags.length} fragment(s)`);
  for (const f of frags) {
    assert.deepEqual(f.linkRange, { start: 7, end: 37 });
  }
  // 片段互不重叠且都属于原范围
  const sorted = [...frags].sort((a, b) => a.start - b.start);
  for (let i = 1; i < sorted.length; i++) {
    assert.ok(sorted[i].start > sorted[i - 1].end);
  }
});
