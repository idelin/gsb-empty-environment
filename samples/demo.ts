// Runnable sample: builds the fixture header, applies a transaction,
// undoes/redoes it, and maps a selection across versions.
// Run: node --experimental-strip-types samples/demo.ts
import {
  HeaderStore,
  mapSelection,
  referenceVisibleColumns,
  type HeaderSnapshot,
} from '../src/index.ts';
import { fixtureState, fixtureColumns } from '../fixtures/header.fixture.ts';

function render(snapshot: HeaderSnapshot): string {
  if (snapshot.rows === 0) return '(empty header)';
  const widths = fixtureColumns();
  const colWidth = (col: number): number => {
    const vc = snapshot.visibleColumns[col];
    const base = vc ? (widths.get(vc.columnId)?.width ?? 10) : 10;
    return Math.max(base, 8);
  };
  const lines: string[] = [];
  for (let r = 0; r < snapshot.rows; r++) {
    let line = '';
    for (let c = 0; c < snapshot.columnCount; c++) {
      const cellId = snapshot.grid[r][c];
      const cell = snapshot.cells.find((x) => x.id === cellId)!;
      const isAnchor = cell.row === r && cell.col === c;
      const spanWidth =
        Array.from({ length: cell.colSpan }, (_, i) => colWidth(cell.col + i))
          .reduce((a, b) => a + b, 0) + (cell.colSpan - 1) * 3;
      const text = isAnchor ? ` ${cell.label}` : '';
      line += `[${text.padEnd(spanWidth - 2).slice(0, spanWidth - 2)}] `;
      c += cell.colSpan - 1;
    }
    lines.push(line);
  }
  return lines.join('\n');
}

function showMapping(snapshot: HeaderSnapshot): string {
  return snapshot.visibleColumns
    .map(
      (vc) =>
        `data[${vc.dataIndex}] <- ${vc.columnId} (owner: ${vc.ownerGroupId}` +
        `${vc.collapsedGroupId ? `, collapsed: ${vc.collapsedGroupId}` : ''})`,
    )
    .join('\n');
}

const store = new HeaderStore(fixtureState());
console.log('== v0 initial ==');
console.log(render(store.snapshot));
console.log(showMapping(store.snapshot));

const v0 = store.snapshot;
const selection = { anchor: { row: 1, col: 0 }, focus: { row: 1, col: 1 } };
console.log('\nselected: the two leaves under "basic" (row 1, cols 0-1)');

store.commit([
  { type: 'hide', columnIds: ['n1', 'n2'] }, // empties group "nested"
  { type: 'collapse', groupId: 'metrics' },
  { type: 'moveColumn', columnId: 'solo', toGroupId: 'basic', index: 0 },
]);
console.log('\n== v1 transaction: hide n1+n2, collapse "metrics", move "solo" into "basic" ==');
console.log(render(store.snapshot));
console.log(showMapping(store.snapshot));

const mapped = mapSelection(v0, store.snapshot, selection);
console.log(`\nselection mapped v0 -> v1: ${mapped.status}`);
if (mapped.status !== 'invalidated') {
  for (const c of mapped.candidates) {
    console.log(`  candidate ${c.cellId} at row ${c.row}, col ${c.col}`);
  }
}

// Independent reference check for this exact edit sequence.
const reference = referenceVisibleColumns(fixtureState(), [
  [
    { type: 'hide', columnIds: ['n1', 'n2'] },
    { type: 'collapse', groupId: 'metrics' },
    { type: 'moveColumn', columnId: 'solo', toGroupId: 'basic', index: 0 },
  ],
], 1);
const actual = store.snapshot.visibleColumns.map((vc) => ({
  columnId: vc.columnId,
  ownerGroupId: vc.ownerGroupId,
}));
console.log(`\nreference check: ${JSON.stringify(actual) === JSON.stringify(reference) ? 'MATCH' : 'MISMATCH'}`);

store.undo();
console.log('\n== undo -> back to v0 ==');
console.log(render(store.snapshot));
store.redo();
console.log('\n== redo -> back to v1 ==');
console.log(render(store.snapshot));
