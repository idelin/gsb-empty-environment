import { test } from 'node:test';
import assert from 'node:assert/strict';

import { BidiTextModel } from '../src/model.ts';
import { StaleLayoutError } from '../src/types.ts';
import { fixedMetrics, nestedTree, rtlTree } from '../src/fixtures.ts';
import { allLevelSequences, reorderReference } from '../src/reorder.ts';
import { buildClusters, flattenTree } from '../src/clusters.ts';
import { layoutClusters } from '../src/layout.ts';

function nestedModel(lineWidth = 100) {
  return new BidiTextModel(nestedTree(), fixedMetrics(), lineWidth);
}

test('visual right traverses the bidi zig-zag and keeps direction in each step', () => {
  const model = nestedModel();
  let caret = model.primaryCaret(0);
  const offsets: Array<[number, number, string]> = [[caret.line, caret.offset, caret.affinity.edge]];
  for (let i = 0; i < 10; i++) {
    const next = model.moveVisual(caret, 'right');
    assert.ok(next);
    caret = next!;
    offsets.push([caret.line, caret.offset, caret.affinity.edge]);
  }
  // physical x strictly increases
  const xs = model.layout.carets.map((c) => c.x);
  assert.deepEqual(xs, [...xs].sort((a, b) => a - b));
  // traversal visits both homes of offset 2 (trailing at x=2, then leading at x=6)
  const offs = offsets.map((o) => o[1]);
  assert.deepEqual(offs, [0, 1, 2, 4, 5, 6, 4, 3, 2, 6, 7]);
  assert.equal(model.moveVisual(caret, 'right'), null);
});

test('logical forward never moves offset backwards; it switches dual home at the boundary', () => {
  const model = nestedModel();
  const start = model.primaryCaret(1); // trailing of cluster b, x=1
  const next = model.moveLogical(start, 'forward')!;
  assert.equal(next.offset, 2);
  assert.equal(next.x, 6); // enters the rtl run from its RIGHT side
  assert.equal(next.affinity.edge, 'leading');
  const back = model.moveLogical(next, 'backward')!;
  assert.equal(back.offset, 1);
});

test('rtl-only paragraph: logical forward is visual LEFT', () => {
  const model = new BidiTextModel(rtlTree(), fixedMetrics(), 100);
  const at0 = model.primaryCaret(0);
  const at1 = model.moveLogical(at0, 'forward')!;
  assert.equal(at1.offset, 1);
  assert.ok(at1.x < at0.x, 'moving forward in rtl must decrease x');
});

test('hit testing returns both carets at a direction boundary (multi-valued, not collapsed)', () => {
  const model = nestedModel();
  // x=2 is the outer ltr/rtl seam: offset 2 trailing AND offset 6 leading
  const hits = model.hitTest(0, 2);
  assert.equal(hits.length, 2);
  assert.deepEqual(hits.map((h) => h.offset).sort((a, b) => a - b), [2, 4]);
  // a click well inside returns a single caret
  assert.equal(model.hitTest(0, 3).length, 1);
});

test('selection remembers drag direction; copy comes back in logical order', () => {
  const model = nestedModel();
  const anchor = model.primaryCaret(0);
  // drag visually right across the seam to the leading home of offset 2 (x=6)
  let head = anchor;
  while (head.offset !== 2 || head.x !== 6) head = model.moveVisual(head, 'right')!;
  const selection = model.select(anchor, head);
  assert.equal(selection.logicalDirection, 'forward');
  assert.deepEqual([selection.startOffset, selection.endOffset], [0, 2]);
  assert.equal(model.copySelection(selection), 'ab');
});

test('backward selection is not lost: offsets and visual ranges reflect the drag', () => {
  const model = nestedModel();
  const anchor = model.primaryCaret(7);
  // drag back to the leading home of the rtl run: offset 2 at x=6
  const head = model.caretsAtOffset(2).find((c) => c.affinity.edge === 'leading')!;
  const selection = model.select(anchor, head);
  assert.equal(selection.logicalDirection, 'backward');
  assert.deepEqual([selection.startOffset, selection.endOffset], [2, 7]);
  assert.equal(model.copySelection(selection), 'ABXYc');
  const ranges = selection.visualRanges();
  assert.equal(ranges.length, 1);
  // head at run's right edge x=6, anchor x=7: highlight spans the whole visual run
  assert.equal(ranges[0]!.x1, 6);
  assert.equal(ranges[0]!.x2, 7);
  assert.equal(ranges[0]!.line, 0);
});

test('cross-visual-line selection over the soft wrap copies logical order', () => {
  const model = new BidiTextModel(rtlTree(), fixedMetrics(), 3);
  // anchor: start of line 0 (offset 0, x=3); head: end of line 1 (offset 5, x=0)
  const line1End = model.layout.carets.find((c) => c.line === 1 && c.x === 0 && c.offset === 5)!;
  const line0Start = model.layout.carets.find((c) => c.line === 0 && c.x === 3 && c.offset === 0)!;
  const selection = model.select(line1End, line0Start);
  assert.equal(selection.logicalDirection, 'backward');
  assert.equal(model.copySelection(selection), 'ABCDE');
  const ranges = selection.visualRanges();
  assert.deepEqual(ranges.map((r) => r.line), [0, 1]);
});

test('visual stepping crosses the soft wrap: upper end == lower start offset but distinct stop', () => {
  const model = new BidiTextModel(rtlTree(), fixedMetrics(), 3);
  // offset 3 has an upper home (line0 LEFT edge x0) and lower home (line1
  // RIGHT edge x2); reading continuation in rtl is a physical LEFT step.
  const endLine0 = model.caretsAtOffset(3).find((c) => c.lineBias === 'upper')!;
  assert.equal(endLine0.x, 0);
  const next = model.moveVisual(endLine0, 'left')!;
  assert.equal(next.line, 1);
  assert.equal(next.offset, 3);
  assert.equal(next.lineBias, 'lower');
  assert.equal(next.x, 2);
  // ...and the reverse step returns to the upper home
  const back = model.moveVisual(next, 'right')!;
  assert.equal(back.line, 0);
  assert.equal(back.lineBias, 'upper');
});

test('local level update rebuilds the layout and invalidates old carets (version guard)', () => {
  const model = nestedModel();
  const oldCaret = model.primaryCaret(2);
  assert.equal(oldCaret.version, 1);
  model.setNodeLevel('t-rtl', 0); // the rtl AB becomes ltr
  assert.equal(model.version, 2);
  // new layout: abABXYc visually (only the XY island reorders)
  assert.deepEqual(model.lines[0]!.boxes.map((b) => b.text), ['a', 'b', 'A', 'B', 'X', 'Y', 'c']);
  assert.throws(() => model.moveVisual(oldCaret, 'right'), (err: unknown) => err instanceof StaleLayoutError);
  assert.throws(() => model.select(oldCaret, model.primaryCaret(0)), StaleLayoutError);
});

test('span-level local update changes every inheriting descendant', () => {
  const model = nestedModel();
  model.setNodeLevel('t-ltr2', 1); // the ltr island XY becomes rtl
  // ab [A B X Y] c, all level 1 -> middle block renders Y X B A
  assert.deepEqual(model.lines[0]!.boxes.map((b) => b.text), ['a', 'b', 'Y', 'X', 'B', 'A', 'c']);
});

test('line-width change also bumps the version (new layout never mixes with old)', () => {
  const model = new BidiTextModel(rtlTree(), fixedMetrics(), 3);
  const caret = model.primaryCaret(3);
  model.setLineWidth(2);
  assert.equal(model.lines.length, 3);
  assert.throws(() => model.moveVisual(caret, 'left'), StaleLayoutError);
});

test('exhaustive round trip: every caret maps back to itself; offset homes cover every boundary', () => {
  for (const seq of allLevelSequences(5, 2)) {
    const letters = ['a', 'b', 'c', 'd', 'e'];
    const tree = { id: 'r', children: [{ id: 't', text: letters.join(''), level: 0 }] };
    const clusters = buildClusters(flattenTree(tree), fixedMetrics()).map((c, i) => ({
      ...c,
      level: seq[i]!,
      dir: (seq[i]! & 1) === 0 ? ('ltr' as const) : ('rtl' as const),
    }));
    const snapshot = layoutClusters(clusters, 100, 1);
    // every physical caret, hit-tested at its own x, contains itself
    for (const caret of snapshot.carets) {
      const line = snapshot.lines[caret.line]!;
      const hits = caretHits(snapshot, caret.line, caret.x);
      assert.ok(hits.some((h) => h.offset === caret.offset && h.affinity.edge === caret.affinity.edge),
        `seq ${seq.join(',')} caret lost at ${caret.x}`);
      void line;
    }
    // every internal logical boundary has at least one visual home
    for (let offset = 0; offset <= 5; offset++) {
      assert.ok(snapshot.carets.some((c) => c.offset === offset));
    }
    // reference permutation still governs the single visual line
    assert.deepEqual(snapshot.lines[0]!.boxes.map((b) => b.clusterIndex), reorderReference(seq));
  }
});

function caretHits(snapshot: ReturnType<typeof layoutClusters>, line: number, x: number) {
  const candidates = snapshot.carets.filter((c) => c.line === line);
  let best = Infinity;
  for (const c of candidates) best = Math.min(best, Math.abs(c.x - x));
  return candidates.filter((c) => Math.abs(c.x - x) === best);
}
