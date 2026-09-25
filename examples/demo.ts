import { HeaderStore } from '../src/ops.ts';
import { mapSelection } from '../src/selection.ts';
import { rebuildFromLeafOrder, diffSnapshots, checkInvariants } from '../src/reference.ts';
import { glyphMetrics, sampleDoc } from '../src/fixtures.ts';
import type { HeaderSnapshot } from '../src/types.ts';

function render(snapshot: HeaderSnapshot): string {
  const lines: string[] = [];
  lines.push(`--- snapshot v${snapshot.version} (depth ${snapshot.depth}, width ${snapshot.width}) ---`);
  for (let row = 0; row < snapshot.depth; row++) {
    const cells = snapshot.cells
      .filter((cell) => cell.row === row)
      .sort((a, b) => a.col - b.col)
      .map((cell) => `${cell.title}[${cell.key}] ${cell.rowspan}x${cell.colspan} @r${cell.row}c${cell.col}`);
    lines.push(`row ${row}: ${cells.join(' | ')}`);
  }
  lines.push(
    'data columns: ' +
      snapshot.columns.map((column) => `#${column.dataIndex}=${column.key}(${column.width}px)`).join(' '),
  );
  return lines.join('\n');
}

function verify(store: HeaderStore): void {
  const reference = rebuildFromLeafOrder(store.doc, glyphMetrics, store.version);
  const diffs = diffSnapshots(store.snapshot, reference);
  const problems = checkInvariants(store.snapshot);
  if (diffs.length > 0 || problems.length > 0) {
    throw new Error(`verification failed: ${[...diffs, ...problems].join('; ')}`);
  }
  console.log(`v${store.version}: reference rebuild + invariants OK`);
}

const store = new HeaderStore(sampleDoc(), glyphMetrics);
const v0 = store.snapshot;
console.log(render(v0));
verify(store);

console.log('\n>> transaction: hide c-age, move c-math into g-basic, collapse g-scores');
const v1 = store.commit([
  { type: 'hide', leafId: 'c-age' },
  { type: 'move', leafId: 'c-math', targetGroupId: 'g-basic', index: 1 },
  { type: 'setCollapsed', groupId: 'g-scores', collapsed: true },
]);
console.log(render(v1));
verify(store);

console.log('\n>> map selection {4..5} (c-rank, c-note) from v0 onto v1');
console.log(JSON.stringify(mapSelection(v0, v1, { start: 4, end: 5 })));

console.log('>> map selection {2..2} (c-math, moved across groups) from v0 onto v1');
console.log(JSON.stringify(mapSelection(v0, v1, { start: 2, end: 2 })));

console.log('>> map selection {0..2} from v0 onto v1 (c-age was hidden)');
console.log(JSON.stringify(mapSelection(v0, v1, { start: 0, end: 2 })));

console.log('\n>> undo transaction');
const v2 = store.undo();
console.log(render(v2));
verify(store);

const restored = { ...v2, version: v0.version };
console.log(
  '\nrestored equals v0:',
  JSON.stringify(restored) === JSON.stringify(v0),
);
