import test from 'node:test';
import assert from 'node:assert/strict';
import { VirtualList, type RowInput } from '../src/virtualList.ts';
import { fixtureMetrics, textNode } from '../src/fixtures.ts';

const EST = 50;

function makeList(count: number, viewport = 200): VirtualList {
  const rows: RowInput[] = [];
  for (let i = 0; i < count; i++) {
    rows.push({ id: `r${i}`, node: textNode(`row ${i}`) });
  }
  return new VirtualList(
    { estimatedHeight: EST, viewportHeight: viewport, measureWidth: 320, metrics: fixtureMetrics() },
    rows,
  );
}

test('inserting 100px above the anchor shifts scrollTop by exactly +100', () => {
  const list = makeList(10);
  list.scrollTo(100); // top of r2
  assert.equal(list.scrollTop, 100);
  list.insertRows(0, [
    { id: 'x1', node: textNode('x1') },
    { id: 'x2', node: textNode('x2') },
  ]); // 2 * 50 = 100px above the anchor
  assert.equal(list.scrollTop, 200);
  assert.equal(list.visibleRange().ids[0], 'r2');
});

test('insert below the anchor leaves scrollTop untouched', () => {
  const list = makeList(10);
  list.scrollTo(100);
  list.insertRows(5, [{ id: 'x1', node: textNode('x1') }]);
  assert.equal(list.scrollTop, 100);
});

test('deleting the anchor row selects its successor', () => {
  const list = makeList(10);
  list.scrollTo(100); // anchor r2
  list.removeRows(['r2']);
  assert.equal(list.scrollTop, 100); // r3 slid into r2's slot
  assert.equal(list.visibleRange().ids[0], 'r3');
});

test('deleting the anchor and everything below falls back to the predecessor', () => {
  const list = makeList(10);
  list.scrollTo(250); // anchor r5
  list.removeRows(['r5', 'r6', 'r7', 'r8', 'r9']);
  // fallback target is r4, the predecessor; remaining content (250px) is
  // shorter than the restored scrollTop, so the scroll clamps to maxScroll
  // and pins r4 to the bottom of the viewport.
  assert.equal(list.scrollTop, 50);
  assert.deepEqual(list.visibleRange().ids, ['r1', 'r2', 'r3', 'r4']);
});

test('measurement correction above the anchor shifts scrollTop by the delta', () => {
  const list = makeList(10);
  list.scrollTo(250); // top of r5
  const token = list.requestMeasurement('r1');
  assert.equal(list.applyMeasurement('r1', 130, token), true); // +80 above
  assert.equal(list.scrollTop, 330);
  assert.equal(list.visibleRange().ids[0], 'r5');
});

test('stale (late) measurements are discarded', () => {
  const list = makeList(10);
  list.scrollTo(250);
  const oldToken = list.requestMeasurement('r1');
  const newToken = list.requestMeasurement('r1'); // re-measured, e.g. width change
  assert.equal(list.applyMeasurement('r1', 500, oldToken), false); // late old result
  assert.equal(list.scrollTop, 250);
  assert.equal(list.applyMeasurement('r1', 130, newToken), true);
  assert.equal(list.scrollTop, 330);
});

test('measurements for deleted rows are discarded', () => {
  const list = makeList(5);
  const token = list.requestMeasurement('r2');
  list.removeRows(['r2']);
  assert.equal(list.applyMeasurement('r2', 90, token), false);
});

test('reorder keeps the anchor visual position', () => {
  const list = makeList(10);
  list.scrollTo(250); // anchor r5
  list.moveRow('r0', 8); // move a row from above to below the anchor
  assert.equal(list.scrollTop, 200);
  assert.equal(list.visibleRange().ids[0], 'r5');
});

test('collapsed groups hide rows and remove their height', () => {
  const list = new VirtualList(
    { estimatedHeight: EST, viewportHeight: 200, measureWidth: 320, metrics: fixtureMetrics() },
    [
      { id: 'a', node: textNode('a') },
      { id: 'g1', node: textNode('g1'), groupId: 'g' },
      { id: 'g2', node: textNode('g2'), groupId: 'g' },
      { id: 'b', node: textNode('b') },
    ],
  );
  assert.equal(list.totalHeight(), 200);
  list.setCollapsed('g', true);
  assert.equal(list.totalHeight(), 100);
  assert.deepEqual([...list.visibleRowIds()], ['a', 'b']);
  list.setCollapsed('g', false);
  assert.equal(list.totalHeight(), 200);
  assert.deepEqual([...list.visibleRowIds()], ['a', 'g1', 'g2', 'b']);
});

test('collapsing the group containing the anchor moves to the next visible row', () => {
  const rows: RowInput[] = [
    { id: 'a', node: textNode('a') },
    { id: 'g1', node: textNode('g1'), groupId: 'g' },
    { id: 'g2', node: textNode('g2'), groupId: 'g' },
  ];
  for (const id of ['b', 'c', 'd', 'e', 'f']) rows.push({ id, node: textNode(id) });
  const list = new VirtualList(
    { estimatedHeight: EST, viewportHeight: 200, measureWidth: 320, metrics: fixtureMetrics() },
    rows,
  );
  list.jumpToId('g1');
  assert.equal(list.visibleRange().ids[0], 'g1');
  list.setCollapsed('g', true);
  assert.equal(list.visibleRange().ids[0], 'b');
  assert.equal(list.scrollTop, 50);
});

test('zero-height rows never break lookup or visible range', () => {
  const list = makeList(10);
  for (const id of ['r1', 'r2', 'r3']) {
    const token = list.requestMeasurement(id);
    assert.equal(list.applyMeasurement(id, 0, token), true);
  }
  // offsets: r0:0, r1..r4:50, r5:100, r6:150, ...
  list.scrollTo(50); // exactly on top of the zero-height run
  const range = list.visibleRange();
  assert.equal(range.ids[0], 'r4'); // bottom-most row starting at offset 50
  assert.deepEqual(range.ids, ['r4', 'r5', 'r6', 'r7']);
  list.scrollBy(-10);
  assert.equal(list.visibleRange().ids[0], 'r0');
  assert.ok(list.visibleRange().ids.includes('r1')); // zero-height rows still listed
});

test('reverse scrolling and jumps clamp to valid range', () => {
  const list = makeList(10); // total 500, viewport 200 -> maxScroll 300
  list.jumpToId('r8');
  assert.equal(list.scrollTop, 300); // clamped to maxScroll
  list.scrollBy(-120);
  assert.equal(list.scrollTop, 180);
  list.scrollBy(-9999);
  assert.equal(list.scrollTop, 0);
  list.scrollBy(9999);
  assert.equal(list.scrollTop, 300);
  list.jumpToIndex(2);
  assert.equal(list.scrollTop, 100);
});

test('empty list stays at rest', () => {
  const list = makeList(0);
  assert.equal(list.totalHeight(), 0);
  assert.equal(list.scrollTop, 0);
  assert.deepEqual(list.visibleRange().ids, []);
  list.scrollBy(100);
  assert.equal(list.scrollTop, 0);
});

test('async measurement applies fixture-computed heights', async () => {
  const list = new VirtualList(
    { estimatedHeight: EST, viewportHeight: 200, measureWidth: 40, metrics: fixtureMetrics() },
    [{ id: 'a', node: textNode('aa aa aa aa') }], // 2 lines at 40px -> 40px
  );
  const applied = await list.measureAsync('a');
  assert.equal(applied, true);
  assert.equal(list.totalHeight(), 40);
});

test('width change invalidates in-flight measurements', async () => {
  const list = new VirtualList(
    { estimatedHeight: EST, viewportHeight: 200, measureWidth: 320, metrics: fixtureMetrics() },
    [{ id: 'a', node: textNode('aaaa aaaa aaaa aaaa') }],
  );
  const staleToken = list.requestMeasurement('a');
  list.setMeasureWidth(60); // one 32px word per line -> 4 lines -> 80px
  assert.equal(list.applyMeasurement('a', 999, staleToken), false);
  const applied = await list.measureAsync('a');
  assert.equal(applied, true);
  assert.equal(list.totalHeight(), 80);
});
