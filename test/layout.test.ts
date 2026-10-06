import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ChatLayout } from '../src/layout.ts';
import type { BatchOp, LayoutItem } from '../src/layout.ts';
import { RefLayout } from './ref-model.ts';
import { mulberry32, pickInt } from './random.ts';

const ESTIMATE = 40;

function items(...ids: string[]): LayoutItem[] {
  return ids.map((id) => ({ id }));
}

function makeLayout(ids: string[], estimate = ESTIMATE): ChatLayout {
  const layout = new ChatLayout({ estimatedHeight: estimate });
  layout.applyBatch([{ type: 'prepend', items: items(...ids) }]);
  return layout;
}

test('empty layout locates nothing and clamps scroll', () => {
  const layout = new ChatLayout();
  assert.equal(layout.count, 0);
  assert.equal(layout.totalHeight, 0);
  assert.deepEqual(layout.locate(0), { index: -1, offsetIn: 0 });
  assert.equal(layout.captureAnchor(100), null);
  assert.equal(layout.update([{ type: 'prepend', items: items('a') }], 250), 50);
});

test('unmeasured items use the estimate, locate walks prefix sums', () => {
  const layout = makeLayout(['a', 'b', 'c']);
  assert.equal(layout.totalHeight, 120);
  assert.equal(layout.offsetOf('c'), 80);
  assert.deepEqual(layout.locate(0), { index: 0, offsetIn: 0 });
  assert.deepEqual(layout.locate(39), { index: 0, offsetIn: 39 });
  assert.deepEqual(layout.locate(40), { index: 1, offsetIn: 0 });
  assert.deepEqual(layout.locate(119), { index: 2, offsetIn: 39 });
  assert.deepEqual(layout.locate(120), { index: 2, offsetIn: 40 });
  assert.deepEqual(layout.locate(9999), { index: 2, offsetIn: 40 });
  assert.deepEqual(layout.locate(-50), { index: 0, offsetIn: 0 });
});

test('on-demand measurement overrides estimate and shifts later offsets', () => {
  const layout = makeLayout(['a', 'b', 'c']);
  assert.equal(layout.isMeasured('b'), false);
  assert.equal(layout.measure('b', 100), true);
  assert.equal(layout.isMeasured('b'), true);
  assert.equal(layout.totalHeight, 180);
  assert.equal(layout.offsetOf('c'), 140);
  assert.equal(layout.measure('missing', 10), false);
  layout.unmeasure('b');
  assert.equal(layout.totalHeight, 120);
});

test('prepend keeps first visible item and its inner offset', () => {
  const layout = makeLayout(['a', 'b', 'c', 'd', 'e']);
  const scroll = layout.offsetOf('c') + 17;
  const next = layout.update(
    [{ type: 'prepend', items: items('x', 'y') }],
    scroll,
  );
  assert.equal(next, scroll + 2 * ESTIMATE);
  const located = layout.locate(next);
  assert.equal(layout.idAt(located.index), 'c');
  assert.equal(located.offsetIn, 17);
});

test('height correction above the anchor shifts scroll by the delta', () => {
  const layout = makeLayout(['a', 'b', 'c', 'd']);
  const scroll = layout.offsetOf('c') + 5;
  const next = layout.update(
    [
      { type: 'measure', id: 'a', height: 90 },
      { type: 'measure', id: 'b', height: 10 },
    ],
    scroll,
  );
  assert.equal(next, scroll + 50 - 30);
  assert.equal(layout.idAt(layout.locate(next).index), 'c');
});

test('height correction at or below the anchor leaves scroll unchanged', () => {
  const layout = makeLayout(['a', 'b', 'c', 'd']);
  const scroll = layout.offsetOf('b') + 10;
  const next = layout.update(
    [
      { type: 'measure', id: 'c', height: 200 },
      { type: 'measure', id: 'b', height: 60 },
    ],
    scroll,
  );
  assert.equal(next, scroll);
});

test('anchor offset clamps when the anchor item itself shrinks', () => {
  const layout = makeLayout(['a', 'b', 'c']);
  const scroll = layout.offsetOf('b') + 35;
  const next = layout.update([{ type: 'measure', id: 'b', height: 20 }], scroll);
  assert.equal(next, layout.offsetOf('b') + 20);
});

test('removed anchor falls back to its original successor top', () => {
  const layout = makeLayout(['a', 'b', 'c', 'd']);
  const scroll = layout.offsetOf('b') + 15;
  const next = layout.update(
    [
      { type: 'remove', ids: ['b'] },
      { type: 'prepend', items: items('z') },
    ],
    scroll,
  );
  assert.equal(next, layout.offsetOf('c'));
  assert.equal(layout.idAt(layout.locate(next).index), 'c');
  assert.equal(layout.locate(next).offsetIn, 0);
});

test('removed anchor skips removed successors', () => {
  const layout = makeLayout(['a', 'b', 'c', 'd', 'e']);
  const scroll = layout.offsetOf('b') + 15;
  const next = layout.update([{ type: 'remove', ids: ['b', 'c', 'd'] }], scroll);
  assert.equal(next, layout.offsetOf('e'));
});

test('removed anchor without successor anchors to predecessor bottom', () => {
  const layout = makeLayout(['a', 'b', 'c', 'd']);
  layout.measure('c', 70);
  const scroll = layout.offsetOf('d') + 10;
  const next = layout.update([{ type: 'remove', ids: ['d'] }], scroll);
  assert.equal(next, layout.offsetOf('c') + 70);
  const located = layout.locate(next);
  assert.equal(layout.idAt(located.index), 'c');
  assert.equal(located.offsetIn, 70);
});

test('predecessor bottom uses its corrected height from the same batch', () => {
  const layout = makeLayout(['a', 'b', 'c']);
  const scroll = layout.offsetOf('c') + 10;
  const next = layout.update(
    [
      { type: 'remove', ids: ['c'] },
      { type: 'measure', id: 'b', height: 120 },
    ],
    scroll,
  );
  assert.equal(next, layout.offsetOf('b') + 120);
});

test('removing everything collapses scroll to zero', () => {
  const layout = makeLayout(['a', 'b']);
  const next = layout.update([{ type: 'remove', ids: ['a', 'b'] }], 50);
  assert.equal(next, 0);
  assert.equal(layout.count, 0);
});

test('visibleRange spans the viewport', () => {
  const layout = makeLayout(['a', 'b', 'c', 'd', 'e']);
  layout.measure('b', 100);
  assert.deepEqual(layout.visibleRange(0, 40), { startIndex: 0, endIndex: 1 });
  assert.deepEqual(layout.visibleRange(45, 80), { startIndex: 1, endIndex: 1 });
  assert.deepEqual(layout.visibleRange(45, 120), { startIndex: 1, endIndex: 2 });
  assert.deepEqual(layout.visibleRange(0, 0), { startIndex: -1, endIndex: -1 });
});

test('duplicate ids are rejected', () => {
  const layout = makeLayout(['a']);
  assert.throws(() => layout.applyBatch([{ type: 'prepend', items: items('a') }]));
  assert.throws(() =>
    layout.applyBatch([{ type: 'prepend', items: items('b', 'b') }]),
  );
});

test('invalid heights are rejected', () => {
  const layout = makeLayout(['a']);
  assert.throws(() => layout.measure('a', -1), RangeError);
  assert.throws(() => layout.measure('a', Number.NaN), RangeError);
  assert.throws(() => new ChatLayout({ estimatedHeight: -5 }), RangeError);
});

test('zero-height items are skipped by locate but keep their slots', () => {
  const layout = makeLayout(['a', 'b', 'c']);
  layout.measure('a', 0);
  layout.measure('b', 30);
  layout.measure('c', 0);
  assert.equal(layout.totalHeight, 30);
  assert.deepEqual(layout.locate(0), { index: 1, offsetIn: 0 });
  assert.deepEqual(layout.locate(30), { index: 2, offsetIn: 0 });
  assert.equal(layout.offsetOf('c'), 30);
});

function randomBatch(
  rand: () => number,
  liveIds: string[],
  nextId: () => string,
): BatchOp[] {
  const ops: BatchOp[] = [];
  const opCount = pickInt(rand, 1, 6);
  for (let i = 0; i < opCount; i++) {
    const kind = pickInt(rand, 0, 5);
    if (kind === 0 || liveIds.length === 0) {
      const fresh = Array.from({ length: pickInt(rand, 1, 4) }, nextId);
      ops.push({ type: 'prepend', items: items(...fresh) });
    } else if (kind === 1) {
      const fresh = Array.from({ length: pickInt(rand, 1, 3) }, nextId);
      ops.push({
        type: 'insert',
        index: pickInt(rand, 0, liveIds.length),
        items: items(...fresh),
      });
    } else if (kind === 2) {
      const shuffled = [...liveIds].sort(() => rand() - 0.5);
      const ids = shuffled.slice(0, pickInt(rand, 1, liveIds.length));
      ops.push({ type: 'remove', ids });
    } else if (kind === 3) {
      const id = liveIds[pickInt(rand, 0, liveIds.length - 1)];
      ops.push({ type: 'measure', id, height: pickInt(rand, 0, 200) });
    } else if (kind === 4) {
      const id = liveIds[pickInt(rand, 0, liveIds.length - 1)];
      ops.push({ type: 'unmeasure', id });
    } else {
      const id = liveIds[pickInt(rand, 0, liveIds.length - 1)];
      ops.push({ type: 'measure', id, height: ESTIMATE });
    }
  }
  return ops;
}

function assertConsistent(layout: ChatLayout, ref: RefLayout, rand: () => number): void {
  assert.equal(layout.count, ref.count, 'count');
  assert.equal(layout.totalHeight, ref.totalHeight(), 'totalHeight');
  const total = ref.totalHeight();
  const samples = [-25, -1, 0, 1, total / 3, total / 2, total - 1, total, total + 1, total + 500];
  for (let i = 0; i < 8; i++) samples.push(rand() * (total + 100) - 50);
  for (const offset of samples) {
    const got = layout.locate(offset);
    const want = ref.locate(offset);
    if (want.id === null) {
      assert.equal(got.index, -1, `locate(${offset}) on empty`);
    } else {
      assert.equal(layout.idAt(got.index), want.id, `locate(${offset}) id`);
      assert.equal(got.offsetIn, want.offsetIn, `locate(${offset}) offsetIn`);
    }
  }
}

test('continuous random batches match the full prefix-sum reference model', () => {
  const rand = mulberry32(20261007);
  const layout = new ChatLayout({ estimatedHeight: ESTIMATE });
  const ref = new RefLayout(ESTIMATE);
  let counter = 0;
  const nextId = () => `item-${counter++}`;

  for (let round = 0; round < 400; round++) {
    const liveIds: string[] = [];
    for (let i = 0; i < layout.count; i++) liveIds.push(layout.idAt(i));
    const ops = randomBatch(rand, liveIds, nextId);
    layout.applyBatch(ops);
    ref.applyBatch(ops);
    for (let i = 0; i < ref.count; i++) {
      const id = layout.idAt(i);
      assert.equal(layout.offsetOf(id), ref.offsetOf(id), `offsetOf(${id}) round ${round}`);
      assert.equal(layout.heightOf(id), ref.heightOf(id), `heightOf(${id}) round ${round}`);
      assert.equal(layout.isMeasured(id), ref.isMeasured(id), `isMeasured(${id}) round ${round}`);
    }
    assertConsistent(layout, ref, rand);
  }
});

test('scroll anchoring matches reference across continuous batches and boundary scrolls', () => {
  const rand = mulberry32(112233);
  const layout = new ChatLayout({ estimatedHeight: ESTIMATE });
  const ref = new RefLayout(ESTIMATE);
  let counter = 0;
  const nextId = () => `msg-${counter++}`;
  let scroll = 0;

  for (let round = 0; round < 400; round++) {
    const liveIds: string[] = [];
    for (let i = 0; i < layout.count; i++) liveIds.push(layout.idAt(i));
    const ops = randomBatch(rand, liveIds, nextId);

    const total = layout.totalHeight;
    const boundary = [0, 1, total / 2, Math.max(0, total - 1), total, total + 250, -100];
    scroll = boundary[pickInt(rand, 0, boundary.length - 1)];
    if (rand() < 0.5) scroll = rand() * (total + 200) - 100;

    const anchorBefore = layout.captureAnchor(scroll);
    const got = layout.update(ops, scroll);
    const want = ref.update(ops, scroll);
    assert.equal(got, want, `scroll round ${round}`);
    scroll = got;

    if (anchorBefore && layout.has(anchorBefore.id)) {
      const expectedOffset = Math.min(anchorBefore.offsetIn, layout.heightOf(anchorBefore.id));
      assert.equal(
        layout.offsetOf(anchorBefore.id) + expectedOffset,
        scroll,
        `anchor position round ${round}`,
      );
      if (expectedOffset < layout.heightOf(anchorBefore.id)) {
        const located = layout.locate(scroll);
        assert.equal(layout.idAt(located.index), anchorBefore.id, `anchor kept round ${round}`);
        assert.equal(located.offsetIn, expectedOffset, `anchor offset round ${round}`);
      }
    }
    assertConsistent(layout, ref, rand);
  }
});

test('anchor deletion fallback matches reference under random removals', () => {
  const rand = mulberry32(555);
  for (let trial = 0; trial < 300; trial++) {
    const ids = Array.from({ length: pickInt(rand, 1, 12) }, (_, i) => `t${trial}-${i}`);
    const layout = makeLayout(ids);
    const ref = new RefLayout(ESTIMATE);
    ref.applyBatch([{ type: 'prepend', items: items(...ids) }]);
    for (const id of ids) {
      if (rand() < 0.4) {
        const height = pickInt(rand, 0, 150);
        layout.applyBatch([{ type: 'measure', id, height }]);
        ref.applyBatch([{ type: 'measure', id, height }]);
      }
    }
    const scroll = rand() * (layout.totalHeight + 40) - 20;
    const doomed = ids.filter(() => rand() < 0.5);
    const ops: BatchOp[] = [{ type: 'remove', ids: doomed }];
    assert.equal(layout.update(ops, scroll), ref.update(ops, scroll), `trial ${trial}`);
  }
});
