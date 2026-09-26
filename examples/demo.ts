import { VirtualList, type RowInput } from '../src/virtualList.ts';
import { fixtureMetrics, imageNode, stackNode, textNode } from '../src/fixtures.ts';

const metrics = fixtureMetrics();
const EST = 50;

function show(list: VirtualList, label: string): void {
  const range = list.visibleRange();
  console.log(
    `${label}\n  scrollTop=${list.scrollTop} total=${list.totalHeight()} version=${list.version}` +
      `\n  visible: ${range.ids.join(', ')}`,
  );
}

// --- build a list: plain rows + a collapsible group, mixed content -------
const rows: RowInput[] = [];
for (let i = 0; i < 6; i++) rows.push({ id: `top-${i}`, node: textNode(`header row ${i}`) });
for (let i = 0; i < 4; i++) {
  rows.push({
    id: `msg-${i}`,
    groupId: 'thread',
    node: stackNode([textNode(`message ${i}: aaaa aaaa aaaa aaaa`), imageNode(64, 30)], 4),
  });
}
for (let i = 0; i < 6; i++) rows.push({ id: `tail-${i}`, node: textNode(`tail row ${i}`) });

const list = new VirtualList(
  { estimatedHeight: EST, viewportHeight: 200, measureWidth: 120, metrics },
  rows,
);
show(list, '1. initial (all rows at estimated height 50)');

// --- rule: insert 100px above the anchor -> scrollTop += 100 -------------
list.scrollTo(250);
console.log(`\n2. anchor row at scrollTop=250 is '${list.visibleRange().ids[0]}'`);
list.insertRows(0, [
  { id: 'new-1', node: textNode('inserted 1') },
  { id: 'new-2', node: textNode('inserted 2') },
]);
show(list, '   after inserting 2x50px=100px above the anchor (scrollTop 250 -> 350)');

// --- async measurement with fixture glyph metrics ------------------------
const anchorBefore = list.visibleRange().ids[0];
await Promise.all([...list.visibleRowIds()].map((id) => list.measureAsync(id)));
show(list, `3. after async real measurement (anchor '${anchorBefore}' kept in place)`);

// --- rule: late stale measurement is discarded ---------------------------
const staleToken = list.requestMeasurement('top-0');
const freshToken = list.requestMeasurement('top-0');
const staleApplied = list.applyMeasurement('top-0', 500, staleToken);
const freshApplied = list.applyMeasurement('top-0', 40, freshToken);
console.log(`\n4. stale token applied? ${staleApplied}; fresh token applied? ${freshApplied}`);

// --- rule: deleting the anchor selects its successor ---------------------
const doomed = list.visibleRange().ids[0];
list.removeRows([doomed]);
show(list, `5. deleted anchor '${doomed}' -> successor took its place`);

// --- collapsed groups ----------------------------------------------------
list.setCollapsed('thread', true);
show(list, '6. collapsed group "thread" (its rows leave the index)');
list.setCollapsed('thread', false);
show(list, '7. expanded again');

// --- reverse scroll + jump ------------------------------------------------
list.scrollBy(-10000);
console.log(`\n8. reverse scroll to the very top: scrollTop=${list.scrollTop}`);
list.jumpToId('tail-4');
show(list, '9. jumpToId(tail-4)');
